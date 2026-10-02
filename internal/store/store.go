// Package store is the bbolt access layer for the viewer.
//
// Everything that touches the database lives here so the Fyne code stays a
// pure view. The design point that matters is nested buckets: paths are
// resolved segment by segment (see Path) rather than being flattened into a
// single name, which is what a top-level-only lookup would do.
package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	// ErrEmptyPath is returned when a lookup is attempted without a bucket name.
	ErrEmptyPath = errors.New("empty bucket path")
	// ErrBucketNotFound is returned when a path does not resolve to a bucket.
	ErrBucketNotFound = errors.New("bucket not found")
	// ErrKeyNotFound is returned when a key is missing from its bucket.
	ErrKeyNotFound = errors.New("key not found")
	// ErrReadOnly is returned when a write is attempted on a read-only database.
	ErrReadOnly = errors.New("database is open read-only")
	// ErrClosed is returned when the database has already been closed. A read
	// still in flight when the file is closed would otherwise reach a nil
	// handle inside bbolt and panic.
	ErrClosed = errors.New("database is closed")
)

// view runs fn in a read transaction.
//
// The lock is held for the whole transaction, so Close cannot pull the handle
// out from under a read that has already started. Checking only for a nil
// handle is not enough: the UI runs reads on worker goroutines, and one of them
// can be partway into db.View when the file is closed.
func (s *Store) view(fn func(tx *bolt.Tx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return ErrClosed
	}
	return s.db.View(fn)
}

// update runs fn in a write transaction, refusing a closed or read-only
// database. The lock is held for the duration, as in view.
func (s *Store) update(fn func(tx *bolt.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return ErrClosed
	}
	if !s.writable {
		return ErrReadOnly
	}
	return s.db.Update(fn)
}

const (
	// DefaultChildLimit caps how many entries a single bucket listing returns.
	DefaultChildLimit = 2000
	// DefaultSearchLimit caps how many hits a search collects.
	DefaultSearchLimit = 500
	// DefaultPageSize is how many entries the flat table asks for per page.
	DefaultPageSize = 200

	// maxNestingDepth bounds the recursion in Search. A well-formed file cannot
	// nest buckets more than MaxKeySize deep, but the limit keeps a malformed
	// or hostile file from growing the goroutine stack without end.
	maxNestingDepth = 64
)

// Store is a handle on one bbolt file.
type Store struct {
	// mu guards db against Close running while a transaction is in flight.
	mu       sync.RWMutex
	db       *bolt.DB
	path     string
	writable bool
}

// Open opens path. When writable is false the file is mapped read-only, which
// is the safe default for a viewer: nothing on disk can change, and another
// process holding the file for writing does not block us.
func Open(path string, writable bool) (*Store, error) {
	opts := &bolt.Options{
		ReadOnly: !writable,
		Timeout:  2 * time.Second,
	}
	db, err := bolt.Open(path, 0o600, opts)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, path: path, writable: writable}, nil
}

// Close releases the database. It waits for any transaction still running, so a
// read that has already started finishes rather than faulting.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Path returns the file this store was opened from.
func (s *Store) Path() string { return s.path }

// Writable reports whether the file was opened for writing.
func (s *Store) Writable() bool { return s.writable }

// FileSize returns the size of the database file in bytes, or 0 if it cannot be
// stat'ed.
func (s *Store) FileSize() int64 {
	info, err := os.Stat(s.path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// Node is one entry inside a bucket: either a key/value pair or a sub-bucket.
type Node struct {
	// Name is the key or bucket name. It is a copy and safe to retain.
	Name []byte
	// IsBucket distinguishes a sub-bucket from a key/value pair.
	IsBucket bool
	// Size is the value length in bytes. It is 0 for buckets.
	Size int64
	// HasKids reports whether a bucket holds at least one entry, sub-buckets
	// included. It is false for keys.
	//
	// This comes from the same read as the listing rather than a follow-up
	// query, because the tree needs it to decide each row's expander and icon
	// before deciding to load that bucket. Probing separately would mean one
	// extra transaction per bucket shown, and a bucket drawn with the wrong
	// icon until it happened to be clicked.
	HasKids bool
}

// Display renders a name for the UI. Bytes that are not printable ASCII are
// escaped, because bbolt keys are arbitrary byte strings and a raw key would
// otherwise produce a garbled or invisible tree row.
func (n Node) Display() string { return Display(n.Name) }

// Display renders an arbitrary byte string for the UI, escaping anything that
// is not printable ASCII plus the path separator.
func Display(name []byte) string { return quoteSegment(name) }

// resolve walks p from the transaction root, one segment at a time, and
// returns the bucket it names. This is the only place a bucket path is turned
// into a *bolt.Bucket; keeping it in one function is what makes arbitrary
// nesting depth work.
func resolve(tx *bolt.Tx, p Path) (*bolt.Bucket, error) {
	if !p.Valid() {
		return nil, ErrEmptyPath
	}

	b := tx.Bucket(p[0])
	if b == nil {
		return nil, fmt.Errorf("%w: %s", ErrBucketNotFound, p)
	}
	for _, seg := range p[1:] {
		// Bucket() returns nil when the child is a plain key, not only when it
		// is missing, so this rejects "Users/Config" when Config is a key.
		b = b.Bucket(seg)
		if b == nil {
			return nil, fmt.Errorf("%w: %s", ErrBucketNotFound, p)
		}
	}
	return b, nil
}

// Buckets lists the top-level buckets in key order.
func (s *Store) Buckets() ([]Path, error) {
	out := make([]Path, 0, 16)
	err := s.view(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			out = append(out, NewPath(string(name)))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Roots lists the top-level buckets with the same detail Children gives for a
// bucket's entries, so the tree can draw every visible row correctly without
// having to open the buckets first.
func (s *Store) Roots() ([]Node, error) {
	out := make([]Node, 0, 16)
	err := s.view(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			out = append(out, Node{
				Name:     bytesClone(name),
				IsBucket: true,
				HasKids:  hasEntries(b),
			})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// hasEntries reports whether a bucket holds at least one entry. It costs a
// single cursor seek, which reads one page.
func hasEntries(b *bolt.Bucket) bool {
	k, _ := b.Cursor().First()
	return k != nil
}

// Children lists the direct entries of the bucket at p, in the bucket's own
// byte-sorted order. Buckets and keys are interleaved because that is how
// bbolt stores them. limit caps the listing; the bool result reports that
// there were more entries, so the UI can say so instead of silently
// pretending the bucket ended.
func (s *Store) Children(p Path, limit int) ([]Node, bool, error) {
	if limit <= 0 {
		limit = DefaultChildLimit
	}

	out := make([]Node, 0, 32)
	var truncated bool
	err := s.view(func(tx *bolt.Tx) error {
		b, err := resolve(tx, p)
		if err != nil {
			return err
		}

		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if len(out) >= limit {
				truncated = true
				break
			}

			// Bucket() is the reliable test here. A cursor's value is empty
			// rather than nil for an empty sub-bucket, so a nil check would
			// misreport an empty bucket as a key holding zero bytes.
			if sub := b.Bucket(k); sub != nil {
				out = append(out, Node{
					Name:     bytesClone(k),
					IsBucket: true,
					HasKids:  hasEntries(sub),
				})
				continue
			}
			out = append(out, Node{Name: bytesClone(k), Size: int64(len(v))})
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, truncated, nil
}

// HasChildren reports whether the bucket at p holds at least one entry. It
// costs a single cursor seek, so the tree can decide whether to draw an
// expander for a bucket it has not loaded yet.
func (s *Store) HasChildren(p Path) (bool, error) {
	var has bool
	err := s.view(func(tx *bolt.Tx) error {
		b, err := resolve(tx, p)
		if err != nil {
			return err
		}
		has = hasEntries(b)
		return nil
	})
	if err != nil {
		return false, err
	}
	return has, nil
}

// Value reads key from the bucket at parent. A limit greater than zero caps
// how many bytes are read, and the bool result reports that the value was cut
// short, so the caller can render a truncated preview instead of pulling a
// gigabyte into the window. A limit of 0 reads the whole value.
//
// bbolt's Get returns memory owned by the transaction and valid only for its
// lifetime, so the bytes are always copied out before the transaction closes.
func (s *Store) Value(parent Path, key []byte, limit int) (data []byte, truncated bool, err error) {
	err = s.view(func(tx *bolt.Tx) error {
		b, err := resolve(tx, parent)
		if err != nil {
			return err
		}

		v := b.Get(key)
		if v == nil {
			// Get also returns nil for sub-buckets, so name the real reason.
			if b.Bucket(key) != nil {
				return fmt.Errorf("%w: %s is a bucket", ErrKeyNotFound, key)
			}
			return fmt.Errorf("%w: %s", ErrKeyNotFound, key)
		}

		n := len(v)
		if limit > 0 && n > limit {
			n = limit
			truncated = true
		}
		data = bytesClone(v[:n])
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return data, truncated, nil
}

// DeleteKey removes key from the bucket at parent. Nested buckets are left
// alone; use DeleteBucket for those.
func (s *Store) DeleteKey(parent Path, key []byte) error {
	return s.update(func(tx *bolt.Tx) error {
		b, err := resolve(tx, parent)
		if err != nil {
			return err
		}
		if b.Bucket(key) != nil {
			// Delete would return ErrIncompatibleValue; name the real problem.
			return fmt.Errorf("%w: %s is a bucket", ErrKeyNotFound, key)
		}
		if b.Get(key) == nil {
			return fmt.Errorf("%w: %s", ErrKeyNotFound, key)
		}
		return b.Delete(key)
	})
}

// DeleteBucket removes the bucket at p along with everything inside it.
func (s *Store) DeleteBucket(p Path) error {
	if !p.Valid() {
		return ErrEmptyPath
	}

	return s.update(func(tx *bolt.Tx) error {
		name := p[len(p)-1]
		if len(p) == 1 {
			// A top-level bucket is deleted through the transaction; there is
			// no parent to resolve.
			if tx.Bucket(name) == nil {
				return fmt.Errorf("%w: %s", ErrBucketNotFound, p)
			}
			return tx.DeleteBucket(name)
		}

		parent, err := resolve(tx, p[:len(p)-1])
		if err != nil {
			return err
		}
		if parent.Bucket(name) == nil {
			return fmt.Errorf("%w: %s", ErrBucketNotFound, p)
		}
		return parent.DeleteBucket(name)
	})
}

// Hit is one search result. It carries the full bucket path so results from
// different buckets, and from buckets nested at different depths, stay
// distinguishable.
type Hit struct {
	// Bucket is the path of the bucket holding the key.
	Bucket Path
	// Key is the matching key.
	Key []byte
	// Size is the value length in bytes.
	Size int64
}

// Label renders a hit as "bucket/path: key", for the result list.
func (h Hit) Label() string {
	return fmt.Sprintf("%s: %s", h.Bucket, quoteSegment(h.Key))
}

// Search walks every bucket in the database, at any depth, and returns the keys
// starting with prefix. An empty prefix matches every key. Only keys are
// returned; sub-buckets are descended into rather than reported. The bool
// result reports that the limit cut the walk short.
func (s *Store) Search(prefix []byte, limit int) (hits []Hit, truncated bool, err error) {
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	hits = make([]Hit, 0, 32)
	err = s.view(func(tx *bolt.Tx) error {
		var walk func(b *bolt.Bucket, p Path, depth int) error
		walk = func(b *bolt.Bucket, p Path, depth int) error {
			if depth > maxNestingDepth {
				return fmt.Errorf("buckets nested deeper than %d levels", maxNestingDepth)
			}

			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				if sub := b.Bucket(k); sub != nil {
					if err := walk(sub, p.Child(k), depth+1); err != nil {
						return err
					}
					continue
				}
				if len(prefix) > 0 && !bytes.HasPrefix(k, prefix) {
					continue
				}
				if len(hits) >= limit {
					truncated = true
					return nil
				}
				hits = append(hits, Hit{
					Bucket: p.Clone(),
					Key:    bytesClone(k),
					Size:   int64(len(v)),
				})
			}
			return nil
		}

		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			return walk(b, NewPath(string(name)), 1)
		})
	})
	if err != nil {
		return nil, false, err
	}
	return hits, truncated, nil
}
