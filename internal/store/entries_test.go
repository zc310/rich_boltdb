package store

import (
	"testing"
)

func TestEntriesListsEveryDepth(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	entries, total, hitLimit, err := s.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if hitLimit {
		t.Error("hitLimit reported for a file that fits one page")
	}
	if total < len(entries) {
		t.Errorf("total %d is below the %d returned entries", total, len(entries))
	}
	if total == 0 {
		t.Error("total is zero for a file with keys")
	}

	// buildDB creates a1, b1, c1, x/y and p/q/z: five keys across depths 1..3.
	want := map[string]bool{
		"A: a1":     true,
		"A/B: b1":   true,
		"A/B/C: c1": true,
		`A: x\/y`:   true,
		`p\/q: z`:   true,
	}
	got := make(map[string]bool, len(entries))
	for _, e := range entries {
		got[e.Label()] = true
	}
	for label := range want {
		if !got[label] {
			t.Errorf("Entries missed %q; got %v", label, labelsOf(entries))
		}
	}
}

func labelsOf(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Label()
	}
	return out
}

func TestEntriesPaging(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	all, total, _, err := s.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(all) < 3 {
		t.Fatalf("need at least 3 entries to page, got %d", len(all))
	}
	if total < len(all) {
		t.Fatalf("total %d is below the %d returned entries", total, len(all))
	}

	// Two pages of two must cover the file with no gaps or repeats.
	first, _, hitLimit, err := s.Entries(0, 2)
	if err != nil {
		t.Fatalf("Entries(0,2): %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first page has %d entries, want 2", len(first))
	}
	if !hitLimit {
		t.Error("hitLimit false when more entries exist")
	}

	second, _, hitLimit, err := s.Entries(2, 2)
	if err != nil {
		t.Fatalf("Entries(2,2): %v", err)
	}
	if len(second) != 2 {
		t.Errorf("second page has %d entries, want 2", len(second))
	}

	for i := range first {
		if first[i].Label() != all[i].Label() {
			t.Errorf("page 1 row %d = %q, want %q", i, first[i].Label(), all[i].Label())
		}
	}
	for i := range second {
		if second[i].Label() != all[2+i].Label() {
			t.Errorf("page 2 row %d = %q, want %q", i, second[i].Label(), all[2+i].Label())
		}
	}

	// A page reaching the end of the file must not claim more entries follow.
	_, _, hitLimit, err = s.Entries(len(all)-1, 10)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if hitLimit {
		t.Error("hitLimit true at the end of the file")
	}

	if _, _, _, err := s.Entries(-5, 2); err != nil {
		t.Errorf("Entries with a negative offset: %v", err)
	}
}

func TestEntriesCarriesSizeAndPath(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	entries, _, _, err := s.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	for _, e := range entries {
		switch e.Label() {
		case "A/B/C: c1":
			if e.Bucket.String() != "A/B/C" {
				t.Errorf("bucket = %q, want A/B/C", e.Bucket)
			}
			if e.Bucket.Depth() != 3 {
				t.Errorf("bucket depth = %d, want 3", e.Bucket.Depth())
			}
			if e.Size != int64(len("value-c1")) {
				t.Errorf("size = %d, want %d", e.Size, len("value-c1"))
			}
		}
	}
}

func TestEntryValue(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	entries, _, _, err := s.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	for _, e := range entries {
		if e.Label() != "A/B/C: c1" {
			continue
		}

		data, truncated, err := s.EntryValue(e, 0)
		if err != nil {
			t.Fatalf("EntryValue: %v", err)
		}
		if truncated {
			t.Error("unlimited read reported as truncated")
		}
		if string(data) != "value-c1" {
			t.Errorf("EntryValue = %q, want value-c1", data)
		}

		data, truncated, err = s.EntryValue(e, 5)
		if err != nil {
			t.Fatalf("EntryValue: %v", err)
		}
		if string(data) != "value" {
			t.Errorf("EntryValue(limit 5) = %q, want value", data)
		}
		if !truncated {
			t.Error("limited read not reported as truncated")
		}
		return
	}
	t.Fatal("A/B/C: c1 not listed")
}

func TestEntryCount(t *testing.T) {
	s := openStore(t, buildDB(t), false)

	count, err := s.EntryCount()
	if err != nil {
		t.Fatalf("EntryCount: %v", err)
	}
	entries, _, _, err := s.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if count != len(entries) {
		t.Errorf("EntryCount = %d, want %d", count, len(entries))
	}
}

// TestEntriesOnEmptyFile covers a database with no keys at all, where the table
// has to show an empty state rather than a zero-row grid.
func TestEntriesOnEmptyFile(t *testing.T) {
	path := t.TempDir() + "/empty.db"
	if err := CreateSample(path); err != nil {
		t.Fatalf("create sample: %v", err)
	}

	// Remove every key but keep the buckets.
	s := openStore(t, path, true)
	if err := s.DeleteKey(NewPath("Users"), []byte("user:1")); err != nil {
		t.Fatalf("delete: %v", err)
	}

	entries, total, hitLimit, err := s.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if hitLimit {
		t.Error("hitLimit on a small file")
	}
	if len(entries) > total {
		t.Errorf("%d entries returned but total is %d", len(entries), total)
	}
}

func TestEntryLabelEscapes(t *testing.T) {
	e := Entry{Bucket: NewPath("a/b"), Key: []byte("k/y")}
	if got := e.Label(); got != `a\/b: k\/y` {
		t.Errorf("Label = %q, want %q", got, `a\/b: k\/y`)
	}
}
