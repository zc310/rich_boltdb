package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// buildDB creates a database with a deliberately awkward layout: three levels
// of nesting, an empty bucket, and names that contain the path separator.
func buildDB(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "nested.db")
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	err = db.Update(func(tx *bolt.Tx) error {
		a, err := tx.CreateBucket([]byte("A"))
		if err != nil {
			return err
		}
		if err := a.Put([]byte("a1"), []byte("value-a1")); err != nil {
			return err
		}

		b, err := a.CreateBucket([]byte("B"))
		if err != nil {
			return err
		}
		if err := b.Put([]byte("b1"), []byte("value-b1")); err != nil {
			return err
		}

		c, err := b.CreateBucket([]byte("C"))
		if err != nil {
			return err
		}
		if err := c.Put([]byte("c1"), []byte("value-c1")); err != nil {
			return err
		}

		if _, err := tx.CreateBucket([]byte("Empty")); err != nil {
			return err
		}

		// A key and a bucket that both contain '/', to prove that a path is
		// resolved by segments and not by splitting a joined string.
		if err := a.Put([]byte("x/y"), []byte("key-with-slash")); err != nil {
			return err
		}
		nested, err := tx.CreateBucket([]byte("p/q"))
		if err != nil {
			return err
		}
		if err := nested.Put([]byte("z"), []byte("bucket-with-slash")); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		t.Fatalf("populate: %v", err)
	}
	return path
}

func openStore(t *testing.T, path string, writable bool) *Store {
	t.Helper()
	s, err := Open(path, writable)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestBuckets(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	got, err := s.Buckets()
	if err != nil {
		t.Fatalf("Buckets: %v", err)
	}

	want := []string{"A", "Empty", `p\/q`}
	if len(got) != len(want) {
		t.Fatalf("got %d buckets %v, want %d %v", len(got), pathsOf(got), len(want), want)
	}
	for i, w := range want {
		if got[i].String() != w {
			t.Errorf("bucket %d = %q, want %q", i, got[i], w)
		}
	}
}

func pathsOf(ps []Path) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

// TestChildrenNested is the regression test for nested buckets: the original
// viewer looked buckets up by joining the path with "/" and asking
// tx.Bucket for that one name, so everything below the top level failed to
// resolve.
func TestChildrenNested(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	for _, tc := range []struct {
		path []string
		want []string
	}{
		// Byte order puts the capitalised names first: "B" < "a1" < "x/y".
		{path: []string{"A"}, want: []string{"B", "a1", "x/y"}},
		{path: []string{"A", "B"}, want: []string{"C", "b1"}},
		{path: []string{"A", "B", "C"}, want: []string{"c1"}},
		{path: []string{"p/q"}, want: []string{"z"}},
	} {
		t.Run(fmt.Sprintf("%v", tc.path), func(t *testing.T) {
			nodes, truncated, err := s.Children(NewPath(tc.path...), 0)
			if err != nil {
				t.Fatalf("Children(%v): %v", tc.path, err)
			}
			if truncated {
				t.Errorf("Children(%v) reported truncation", tc.path)
			}
			if len(nodes) != len(tc.want) {
				t.Fatalf("got %d nodes %v, want %d", len(nodes), nodesOf(nodes), len(tc.want))
			}
			for i, w := range tc.want {
				if string(nodes[i].Name) != w {
					t.Errorf("node %d = %q, want %q", i, nodes[i].Name, w)
				}
			}
		})
	}
}

func nodesOf(ns []Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = fmt.Sprintf("%s(bucket=%v,size=%d)", n.Display(), n.IsBucket, n.Size)
	}
	return out
}

// TestChildrenReportsBuckets guards the empty-bucket case. A cursor's value is
// empty rather than nil for an empty sub-bucket, so testing the value against
// nil would mislabel it as a key with a zero-byte value.
func TestChildrenReportsBuckets(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	nodes, _, err := s.Children(NewPath("A", "B"), 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}

	var c *Node
	for i := range nodes {
		if string(nodes[i].Name) == "C" {
			c = &nodes[i]
		}
	}
	if c == nil {
		t.Fatal("sub-bucket C missing from listing")
	}
	if !c.IsBucket {
		t.Error("C reported as a key, want bucket")
	}
}

func TestChildrenEmptyBucket(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	nodes, truncated, err := s.Children(NewPath("Empty"), 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("got %v, want no entries", nodesOf(nodes))
	}
	if truncated {
		t.Error("empty bucket reported as truncated")
	}

	has, err := s.HasChildren(NewPath("Empty"))
	if err != nil {
		t.Fatalf("HasChildren: %v", err)
	}
	if has {
		t.Error("HasChildren(Empty) = true, want false")
	}
}

func TestHasChildren(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	for _, tc := range []struct {
		path []string
		want bool
	}{
		{path: []string{"A"}, want: true},
		{path: []string{"A", "B"}, want: true},
		{path: []string{"A", "B", "C"}, want: true},
		{path: []string{"Empty"}, want: false},
	} {
		has, err := s.HasChildren(NewPath(tc.path...))
		if err != nil {
			t.Fatalf("HasChildren(%v): %v", tc.path, err)
		}
		if has != tc.want {
			t.Errorf("HasChildren(%v) = %v, want %v", tc.path, has, tc.want)
		}
	}
}

func TestChildrenMissingBucket(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	if _, _, err := s.Children(NewPath("nope"), 0); !errors.Is(err, ErrBucketNotFound) {
		t.Errorf("Children(nope) error = %v, want ErrBucketNotFound", err)
	}
	if _, _, err := s.Children(NewPath("A", "nope"), 0); !errors.Is(err, ErrBucketNotFound) {
		t.Errorf("Children(A/nope) error = %v, want ErrBucketNotFound", err)
	}
	// A path whose first segment names a key is not a bucket either.
	if _, _, err := s.Children(NewPath("A", "a1"), 0); !errors.Is(err, ErrBucketNotFound) {
		t.Errorf("Children(A/a1) error = %v, want ErrBucketNotFound", err)
	}
	if _, _, err := s.Children(nil, 0); !errors.Is(err, ErrEmptyPath) {
		t.Errorf("Children(nil) error = %v, want ErrEmptyPath", err)
	}
}

func TestChildrenTruncation(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	nodes, truncated, err := s.Children(NewPath("A"), 2)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("got %d nodes, want 2", len(nodes))
	}
	if !truncated {
		t.Error("truncated = false, want true")
	}

	nodes, truncated, err = s.Children(NewPath("A", "B", "C"), 2)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("got %d nodes, want 1", len(nodes))
	}
	if truncated {
		t.Error("truncated = true, want false when the bucket fits")
	}
}

func TestValueNested(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	for _, tc := range []struct {
		parent []string
		key    string
		want   string
	}{
		{parent: []string{"A"}, key: "a1", want: "value-a1"},
		{parent: []string{"A", "B"}, key: "b1", want: "value-b1"},
		{parent: []string{"A", "B", "C"}, key: "c1", want: "value-c1"},
		{parent: []string{"A"}, key: "x/y", want: "key-with-slash"},
		{parent: []string{"p/q"}, key: "z", want: "bucket-with-slash"},
	} {
		data, truncated, err := s.Value(NewPath(tc.parent...), []byte(tc.key), 0)
		if err != nil {
			t.Fatalf("Value(%v, %q): %v", tc.parent, tc.key, err)
		}
		if truncated {
			t.Errorf("Value(%v, %q) reported truncation", tc.parent, tc.key)
		}
		if string(data) != tc.want {
			t.Errorf("Value(%v, %q) = %q, want %q", tc.parent, tc.key, data, tc.want)
		}
	}
}

func TestValueLimit(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	data, truncated, err := s.Value(NewPath("A", "B", "C"), []byte("c1"), 4)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if string(data) != "valu" {
		t.Errorf("Value with limit 4 = %q, want %q", data, "valu")
	}
	if !truncated {
		t.Error("truncated = false, want true")
	}
}

// TestValueReturnedBytesSurviveTransaction makes sure the copy is real: the
// reader below uses the bytes long after the read transaction has closed, which
// would be undefined behaviour if the store handed back bbolt's own memory.
func TestValueReturnedBytesSurviveTransaction(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	for i := 0; i < 50; i++ {
		data, _, err := s.Value(NewPath("A", "B", "C"), []byte("c1"), 0)
		if err != nil {
			t.Fatalf("Value: %v", err)
		}
		if string(data) != "value-c1" {
			t.Fatalf("value corrupted after %d reads: %q", i, data)
		}
	}
}

func TestValueErrors(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	if _, _, err := s.Value(NewPath("A", "B"), []byte("missing"), 0); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("Value(missing) error = %v, want ErrKeyNotFound", err)
	}
	// Asking a sub-bucket for a value is a key miss, not an empty value.
	if _, _, err := s.Value(NewPath("A", "B"), []byte("C"), 0); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("Value(bucket) error = %v, want ErrKeyNotFound", err)
	}
	if _, _, err := s.Value(nil, []byte("a1"), 0); !errors.Is(err, ErrEmptyPath) {
		t.Errorf("Value(nil path) error = %v, want ErrEmptyPath", err)
	}
}

func TestSearchNested(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	hits, truncated, err := s.Search([]byte(""), 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if truncated {
		t.Error("Search reported truncation")
	}

	got := make(map[string]bool)
	for _, h := range hits {
		got[h.Label()] = true
	}
	for _, want := range []string{
		"A: a1",
		"A/B: b1",
		"A/B/C: c1",
		`A: x\/y`,
		`p\/q: z`,
	} {
		if !got[want] {
			t.Errorf("Search missed %q (got %v)", want, keysOf(got))
		}
	}

	hits, _, err = s.Search([]byte("b"), 0)
	if err != nil {
		t.Fatalf("Search(b): %v", err)
	}
	if len(hits) != 1 || string(hits[0].Key) != "b1" {
		t.Errorf("Search(b) = %v, want only b1", hits)
	}
	if hits[0].Bucket.String() != "A/B" {
		t.Errorf("Search(b) bucket = %q, want A/B", hits[0].Bucket)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSearchLimit(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	hits, truncated, err := s.Search([]byte(""), 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("got %d hits, want 2", len(hits))
	}
	if !truncated {
		t.Error("truncated = false, want true")
	}
}

func TestReadOnlyRejectsWrites(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	if s.Writable() {
		t.Fatal("Writable() = true, want false")
	}
	if err := s.DeleteKey(NewPath("A"), []byte("a1")); !errors.Is(err, ErrReadOnly) {
		t.Errorf("DeleteKey error = %v, want ErrReadOnly", err)
	}
	if err := s.DeleteBucket(NewPath("A", "B")); !errors.Is(err, ErrReadOnly) {
		t.Errorf("DeleteBucket error = %v, want ErrReadOnly", err)
	}
}

func TestDeleteKeyNested(t *testing.T) {
	s := openStore(t, buildDB(t), true)

	if !s.Writable() {
		t.Fatal("Writable() = false, want true")
	}
	if err := s.DeleteKey(NewPath("A", "B", "C"), []byte("c1")); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}

	// The parent buckets must survive the delete.
	if _, _, err := s.Value(NewPath("A", "B"), []byte("b1"), 0); err != nil {
		t.Errorf("sibling key gone after delete: %v", err)
	}
	if _, _, err := s.Value(NewPath("A", "B", "C"), []byte("c1"), 0); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("Value after delete = %v, want ErrKeyNotFound", err)
	}

	if err := s.DeleteKey(NewPath("A", "B", "C"), []byte("nope")); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("DeleteKey(missing) error = %v, want ErrKeyNotFound", err)
	}
	// Deleting a sub-bucket through DeleteKey must be refused, not silently
	// interpreted as a key delete.
	if err := s.DeleteKey(NewPath("A", "B"), []byte("C")); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("DeleteKey on a bucket error = %v, want ErrKeyNotFound", err)
	}
	if _, _, err := s.Children(NewPath("A", "B"), 0); err != nil {
		t.Errorf("sub-bucket C disappeared: %v", err)
	}
}

func TestDeleteBucketNested(t *testing.T) {
	s := openStore(t, buildDB(t), true)

	if err := s.DeleteBucket(NewPath("A", "B", "C")); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	if _, _, err := s.Children(NewPath("A", "B", "C"), 0); !errors.Is(err, ErrBucketNotFound) {
		t.Errorf("Children after delete = %v, want ErrBucketNotFound", err)
	}
	// Deleting a top-level bucket by name still works.
	if err := s.DeleteBucket(NewPath("Empty")); err != nil {
		t.Fatalf("DeleteBucket(Empty): %v", err)
	}
	if _, _, err := s.Children(NewPath("Empty"), 0); !errors.Is(err, ErrBucketNotFound) {
		t.Errorf("Empty survived delete: %v", err)
	}
	if err := s.DeleteBucket(NewPath("nope")); !errors.Is(err, ErrBucketNotFound) {
		t.Errorf("DeleteBucket(nope) error = %v, want ErrBucketNotFound", err)
	}
	if err := s.DeleteBucket(nil); !errors.Is(err, ErrEmptyPath) {
		t.Errorf("DeleteBucket(nil) error = %v, want ErrEmptyPath", err)
	}
}

func TestPathChildDoesNotMutateParent(t *testing.T) {
	p := NewPath("A", "B")
	child := p.Child([]byte("C"))

	if p.String() != "A/B" {
		t.Errorf("parent mutated: %q", p)
	}
	if child.String() != "A/B/C" {
		t.Errorf("child = %q, want A/B/C", child)
	}
	if child.Depth() != 3 {
		t.Errorf("Depth = %d, want 3", child.Depth())
	}
	if string(child.Name()) != "C" {
		t.Errorf("Name = %q, want C", child.Name())
	}
}

func TestPathEscapesSeparator(t *testing.T) {
	if got := NewPath("p/q").String(); got != `p\/q` {
		t.Errorf("String = %q, want %q", got, `p\/q`)
	}
	if got := NewPath("a\x00b").String(); got != `a\x00b` {
		t.Errorf("String = %q, want %q", got, `a\x00b`)
	}
	if got := (Node{Name: []byte("k/y")}).Display(); got != `k\/y` {
		t.Errorf("Display = %q, want %q", got, `k\/y`)
	}
}

func TestOpenNonDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notadb.db")
	if err := os.WriteFile(path, []byte("this is not a bolt database"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(path, false); err == nil {
		t.Error("Open on a non-database file succeeded, want error")
	}
}

func TestSampleRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), SampleFileName)
	if err := CreateSample(path); err != nil {
		t.Fatalf("CreateSample: %v", err)
	}

	s := openStore(t, path, false)

	buckets, err := s.Buckets()
	if err != nil {
		t.Fatalf("Buckets: %v", err)
	}
	want := []string{"Empty", "MyBucket_A", "MyBucket_B", "MyBucket_C", "Users", `logs\/2024`}
	if len(buckets) != len(want) {
		t.Fatalf("got %v, want %v", pathsOf(buckets), want)
	}
	for i, w := range want {
		if buckets[i].String() != w {
			t.Errorf("bucket %d = %q, want %q", i, buckets[i], w)
		}
	}

	// Three levels deep in the generated file.
	nodes, _, err := s.Children(NewPath("Users", "Profile", "Fields"), 0)
	if err != nil {
		t.Fatalf("Children(Users/Profile/Fields): %v", err)
	}
	if len(nodes) != 3 {
		t.Errorf("got %d fields, want 3", len(nodes))
	}

	data, _, err := s.Value(NewPath("Users"), []byte("a/b"), 0)
	if err != nil {
		t.Fatalf("Value(Users, a/b): %v", err)
	}
	if string(data) != "separator inside a key" {
		t.Errorf("Value = %q", data)
	}

	hits, _, err := s.Search([]byte("key_A_000001"), 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("got %d hits, want 1", len(hits))
	}
}

func TestCreateSampleIsDeterministic(t *testing.T) {
	first := filepath.Join(t.TempDir(), "one.db")
	second := filepath.Join(t.TempDir(), "two.db")

	if err := CreateSample(first); err != nil {
		t.Fatalf("CreateSample: %v", err)
	}
	if err := CreateSample(second); err != nil {
		t.Fatalf("CreateSample: %v", err)
	}

	s1, s2 := openStore(t, first, false), openStore(t, second, false)
	for _, tc := range []struct{ bucket, key string }{
		{"MyBucket_A", "key_A_000001"},
		{"MyBucket_A", "key_A_000008"},
		{"MyBucket_C", "key_C_000004"},
	} {
		a, _, err := s1.Value(NewPath(tc.bucket), []byte(tc.key), 0)
		if err != nil {
			t.Fatalf("Value(%s, %q): %v", tc.bucket, tc.key, err)
		}
		b, _, err := s2.Value(NewPath(tc.bucket), []byte(tc.key), 0)
		if err != nil {
			t.Fatalf("Value(%s, %q): %v", tc.bucket, tc.key, err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s/%s differs between runs", tc.bucket, tc.key)
		}
	}
}

// TestClosedStoreRefusesReads covers a read still in flight when the file is
// closed underneath it: the UI keeps reads on worker goroutines, and reaching a
// nil bbolt handle would panic rather than return.
func TestClosedStoreRefusesReads(t *testing.T) {
	s := openStore(t, buildDB(t), false)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := s.Buckets(); !errors.Is(err, ErrClosed) {
		t.Errorf("Buckets after close = %v, want ErrClosed", err)
	}
	if _, _, err := s.Children(NewPath("A"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("Children after close = %v, want ErrClosed", err)
	}
	if _, err := s.HasChildren(NewPath("A")); !errors.Is(err, ErrClosed) {
		t.Errorf("HasChildren after close = %v, want ErrClosed", err)
	}
	if _, _, err := s.Value(NewPath("A"), []byte("a1"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("Value after close = %v, want ErrClosed", err)
	}
	if _, _, _, err := s.Entries(0, 10); !errors.Is(err, ErrClosed) {
		t.Errorf("Entries after close = %v, want ErrClosed", err)
	}
	if _, _, err := s.Search([]byte(""), 10); !errors.Is(err, ErrClosed) {
		t.Errorf("Search after close = %v, want ErrClosed", err)
	}
	if _, err := s.EntryCount(); !errors.Is(err, ErrClosed) {
		t.Errorf("EntryCount after close = %v, want ErrClosed", err)
	}
	if err := s.DeleteKey(NewPath("A"), []byte("a1")); !errors.Is(err, ErrClosed) {
		t.Errorf("DeleteKey after close = %v, want ErrClosed", err)
	}
	if err := s.DeleteBucket(NewPath("A")); !errors.Is(err, ErrClosed) {
		t.Errorf("DeleteBucket after close = %v, want ErrClosed", err)
	}

	// Close is idempotent, so cleanup can call it again.
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestDistinctBucketCount(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	// A, A/B, A/B/C and p/q all hold keys.
	count, err := s.DistinctBucketCount(10)
	if err != nil {
		t.Fatalf("DistinctBucketCount: %v", err)
	}
	if count != 4 {
		t.Errorf("got %d distinct buckets, want 4", count)
	}

	// A cap of 2 stops early rather than walking the whole file.
	count, err = s.DistinctBucketCount(2)
	if err != nil {
		t.Fatalf("DistinctBucketCount(2): %v", err)
	}
	if count != 2 {
		t.Errorf("capped count = %d, want 2", count)
	}

	if _, err := s.DistinctBucketCount(0); err != nil {
		t.Errorf("DistinctBucketCount(0) should default, got %v", err)
	}
}

func TestDistinctBucketCountSingleBucket(t *testing.T) {
	path := t.TempDir() + "/one.db"
	if err := CreateFlat(path, "only", []string{"a", "b", "c"}); err != nil {
		t.Fatalf("create flat: %v", err)
	}

	s := openStore(t, path, false)
	count, err := s.DistinctBucketCount(10)
	if err != nil {
		t.Fatalf("DistinctBucketCount: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d distinct buckets, want 1", count)
	}
}

// TestRootsReportHasKids is what lets the tree draw a folder icon and the right
// expander before a bucket has been opened: without it, an unopened bucket is
// indistinguishable from a key.
func TestRootsReportHasKids(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	roots, err := s.Roots()
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}

	for _, n := range roots {
		if !n.IsBucket {
			t.Errorf("root %q is not reported as a bucket", n.Name)
		}
		// A, Empty and p/q are the only top-level buckets; Empty holds nothing.
		wantKids := string(n.Name) != "Empty"
		if n.HasKids != wantKids {
			t.Errorf("root %q HasKids = %v, want %v", n.Name, n.HasKids, wantKids)
		}
	}
}

// TestChildrenReportHasKids covers sub-buckets, which appear in a parent listing
// and carry the same flag.
func TestChildrenReportHasKids(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	nodes, _, err := s.Children(NewPath("A", "B"), 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}

	var c *Node
	for i := range nodes {
		if string(nodes[i].Name) == "C" {
			c = &nodes[i]
		}
		if string(nodes[i].Name) == "b1" {
			if nodes[i].HasKids {
				t.Error("a key reported as holding entries")
			}
		}
	}
	if c == nil {
		t.Fatal("sub-bucket C missing")
	}
	if !c.HasKids {
		t.Error("sub-bucket C holds c1 but reports HasKids false")
	}

	// An empty sub-bucket must be distinguishable from a key.
	emptyPath := NewPath("Empty")
	nodes, _, err = s.Children(emptyPath, 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("Empty has %d entries", len(nodes))
	}
	has, err := s.HasChildren(emptyPath)
	if err != nil {
		t.Fatalf("HasChildren: %v", err)
	}
	if has {
		t.Error("HasChildren(Empty) = true")
	}
}

func TestRootsRequiresOpenDatabase(t *testing.T) {
	s := openStore(t, buildDB(t), false)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := s.Roots(); !errors.Is(err, ErrClosed) {
		t.Errorf("Roots after close = %v, want ErrClosed", err)
	}
}
