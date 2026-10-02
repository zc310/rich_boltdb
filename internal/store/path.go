package store

import (
	"fmt"
	"strings"
)

// Path identifies a bucket by the chain of names leading to it, starting at
// the transaction root.
//
// bbolt has no flat address for a nested bucket: every lookup has to walk
// parent by parent, so a path cannot be flattened into a single string. The
// original viewer joined the names with "/" and handed the result to
// tx.Bucket, which only ever resolves a single top-level name. Any bucket
// below the first level therefore came back as "not found". Keeping the
// segments separate also means a key that literally contains '/' still
// resolves correctly.
type Path [][]byte

// NewPath builds a path from the given bucket names.
func NewPath(names ...string) Path {
	p := make(Path, len(names))
	for i, name := range names {
		p[i] = []byte(name)
	}
	return p
}

// Child returns the path of the bucket called name inside p. The receiver is
// not modified, so a parent path stays reusable as a node's identity.
func (p Path) Child(name []byte) Path {
	child := make(Path, len(p), len(p)+1)
	copy(child, p)
	return append(child, bytesClone(name))
}

// Clone returns an independent copy of the path.
func (p Path) Clone() Path {
	out := make(Path, len(p))
	for i, seg := range p {
		out[i] = bytesClone(seg)
	}
	return out
}

// Name returns the path of the bucket itself, which is the last segment.
func (p Path) Name() []byte {
	if len(p) == 0 {
		return nil
	}
	return p[len(p)-1]
}

// Depth returns the number of segments; 1 for a top-level bucket.
func (p Path) Depth() int { return len(p) }

// Valid reports whether the path names at least one bucket.
func (p Path) Valid() bool { return len(p) > 0 }

// String renders the path for display. It is a lossy, human-facing rendering
// and must not be fed back into a lookup; segments that contain the separator
// are escaped so the result stays unambiguous to read.
func (p Path) String() string {
	var b strings.Builder
	for i, seg := range p {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(quoteSegment(seg))
	}
	return b.String()
}

func (p Path) GoString() string { return fmt.Sprintf("store.Path(%q)", p.String()) }

func quoteSegment(seg []byte) string {
	var b strings.Builder
	for _, c := range seg {
		switch {
		case c == '/':
			b.WriteString(`\/`)
		case c == '\\':
			b.WriteString(`\\`)
		case c < 0x20 || c > 0x7e:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func bytesClone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
