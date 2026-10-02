package store

import (
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// maxCountedEntries bounds the total reported by Entries. A file with millions
// of keys would otherwise spend a long time walking just to produce a number
// for the scrollbar; past this many the count reads as "at least N".
const maxCountedEntries = 200000

// Entry is one key/value pair together with the bucket path that holds it.
//
// It carries the full path rather than just a bucket name because a key can sit
// at any depth: "display_name" is a different thing under Users/Profile/Fields
// than under a top-level bucket of its own.
type Entry struct {
	// Bucket is the path of the containing bucket.
	Bucket Path
	// Key is the key within that bucket.
	Key []byte
	// Size is the value length in bytes.
	Size int64
}

// Label is a one-line rendering for display: "bucket/path: key".
func (e Entry) Label() string {
	return fmt.Sprintf("%s: %s", e.Bucket, quoteSegment(e.Key))
}

// Entries lists keys across every bucket in the file, at any depth, one page at
// a time.
//
// The walk is depth-first in byte order, buckets before the keys that surround
// them, so a page is stable as long as the file does not change. offset skips
// entries and limit caps how many are returned; total is the number of entries
// in the file, or maxCountedEntries when the file holds more than that. hitLimit
// reports that the page ran into the limit, which is distinct from the file
// holding exactly that many entries.
//
// Only keys and value sizes are read here. The value bytes are fetched per row
// when it is displayed, so listing a file with large values stays cheap.
func (s *Store) Entries(offset, limit int) (entries []Entry, total int, hitLimit bool, err error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = DefaultPageSize
	}

	entries = make([]Entry, 0, limit)

	err = s.view(func(tx *bolt.Tx) error {
		seen := 0

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

				if seen < offset {
					seen++
					continue
				}
				if len(entries) >= limit {
					// One more key exists past the page, so the caller knows to
					// offer another one.
					hitLimit = true
					return nil
				}

				entries = append(entries, Entry{
					Bucket: p.Clone(),
					Key:    bytesClone(k),
					Size:   int64(len(v)),
				})
				seen++
			}
			return nil
		}

		countErr := tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			return walk(b, NewPath(string(name)), 1)
		})
		if countErr != nil {
			return countErr
		}

		total = 0
		var tally func(b *bolt.Bucket, depth int) error
		tally = func(b *bolt.Bucket, depth int) error {
			if depth > maxNestingDepth {
				return nil
			}
			c := b.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				if total >= maxCountedEntries {
					return nil
				}
				if sub := b.Bucket(k); sub != nil {
					if err := tally(sub, depth+1); err != nil {
						return err
					}
					continue
				}
				total++
			}
			return nil
		}
		return tx.ForEach(func(_ []byte, b *bolt.Bucket) error {
			return tally(b, 1)
		})
	})
	if err != nil {
		return nil, 0, false, err
	}
	return entries, total, hitLimit, nil
}

// DistinctBucketCount returns how many distinct buckets hold at least one key,
// capped at limit so a huge file does not trigger a long walk. It answers
// whether the bucket name is worth showing as a column.
func (s *Store) DistinctBucketCount(limit int) (int, error) {
	if limit <= 0 {
		limit = DefaultPageSize
	}

	seen := make(map[string]bool)
	err := s.view(func(tx *bolt.Tx) error {
		// errStopWalk unwinds the recursion once the cap is reached. Checking
		// only on the way into a bucket is not enough: the walk is depth-first,
		// so the loop that resumed in the parent bucket would keep adding
		// entries after the cap had already been passed.
		var walk func(b *bolt.Bucket, p Path, depth int) error
		walk = func(b *bolt.Bucket, p Path, depth int) error {
			if depth > maxNestingDepth {
				return fmt.Errorf("buckets nested deeper than %d levels", maxNestingDepth)
			}
			if len(seen) >= limit {
				return errStopWalk
			}

			label := p.String()

			c := b.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				if sub := b.Bucket(k); sub != nil {
					if err := walk(sub, p.Child(k), depth+1); err != nil {
						return err
					}
					continue
				}
				if seen[label] {
					// Every further key in this bucket shares the label.
					continue
				}
				seen[label] = true
				if len(seen) >= limit {
					return errStopWalk
				}
			}
			return nil
		}

		walkErr := tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			return walk(b, NewPath(string(name)), 1)
		})
		if errors.Is(walkErr, errStopWalk) {
			return nil
		}
		return walkErr
	})
	if err != nil {
		return 0, err
	}
	return len(seen), nil
}

// errStopWalk ends a capped walk early without reporting a failure.
var errStopWalk = errors.New("stop walk")

// EntryValue reads the value of e, capped at limit bytes. A limit of 0 reads
// the whole value.
func (s *Store) EntryValue(e Entry, limit int) (data []byte, truncated bool, err error) {
	return s.Value(e.Bucket, e.Key, limit)
}

// EntryCount returns the number of keys in the file, capped at
// maxCountedEntries.
func (s *Store) EntryCount() (int, error) {
	_, total, _, err := s.Entries(0, 1)
	return total, err
}
