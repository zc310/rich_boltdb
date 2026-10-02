package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"boltdb_viewer/internal/store"
)

// newTestTree builds a tree model backed by a real database holding three
// levels of buckets, matching the layout in the store tests.
func newTestTree(t *testing.T) *tree {
	t.Helper()

	dir := t.TempDir()
	if err := store.CreateSample(dir + "/sample.db"); err != nil {
		t.Fatalf("create sample: %v", err)
	}

	st, err := store.Open(dir+"/sample.db", false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	tr := newTree(st)
	if err := tr.loadRoots(); err != nil {
		t.Fatalf("loadRoots: %v", err)
	}
	return tr
}

// TestTreeLoadsNestedBuckets is the UI-side regression test: walking the tree
// three levels down has to work, which it could not when buckets were looked
// up by a "/"-joined name.
func TestTreeLoadsNestedBuckets(t *testing.T) {
	tr := newTestTree(t)

	if len(tr.roots) != len(tr.childIDs("")) {
		t.Fatalf("root count mismatch: %d vs %d", len(tr.roots), len(tr.childIDs("")))
	}

	users := findRoot(tr, "Users")
	if users == nil {
		t.Fatal("Users bucket missing")
	}
	if got := users.pathString(); got != "Users" {
		t.Errorf("path = %q, want Users", got)
	}

	// Load level one.
	nodes, truncated, err := tr.st.Children(users.path, 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if truncated {
		t.Error("unexpected truncation")
	}
	tr.loadBucket(users, nodes, truncated)

	profile := findByName(users, "Profile")
	if profile == nil {
		t.Fatal("Profile bucket missing from Users")
	}
	if !profile.isBucket {
		t.Error("Profile reported as a key")
	}

	// Level two.
	nodes, truncated, err = tr.st.Children(profile.path, 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	tr.loadBucket(profile, nodes, truncated)

	fields := findByName(profile, "Fields")
	if fields == nil {
		t.Fatal("Fields bucket missing from Users/Profile")
	}

	// Level three.
	nodes, _, err = tr.st.Children(fields.path, 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	tr.loadBucket(fields, nodes, false)

	if len(fields.children) != 3 {
		t.Errorf("Fields has %d children, want 3", len(fields.children))
	}
	if got := fields.pathString(); got != "Users/Profile/Fields" {
		t.Errorf("deep path = %q, want Users/Profile/Fields", got)
	}

	// Every id must resolve, which is what the Fyne tree relies on.
	for _, n := range fields.children {
		if tr.lookup(n.id) != n {
			t.Errorf("node %q missing from the index", n.id)
		}
		if got := tr.childIDs(n.id); len(got) != 0 {
			t.Errorf("key node %q reports %d children", n.label(), len(got))
		}
		if tr.hasChildren(n.id) {
			t.Errorf("key node %q reports children", n.label())
		}
	}
}

// TestTreeEmptyBucketHasNoExpander guards the empty-bucket case: an empty
// bucket must not advertise an expander.
func TestTreeEmptyBucketHasNoExpander(t *testing.T) {
	tr := newTestTree(t)

	empty := findRoot(tr, "Empty")
	if empty == nil {
		t.Fatal("Empty bucket missing")
	}

	has, err := tr.st.HasChildren(empty.path)
	if err != nil {
		t.Fatalf("HasChildren: %v", err)
	}
	if has {
		t.Fatal("sample Empty bucket reports children")
	}
	if tr.hasChildren(empty.id) {
		t.Error("tree shows an expander for an empty bucket")
	}
}

// TestTreeReloadReplacesSubtree makes sure a second load does not leave stale
// nodes behind in the index.
func TestTreeReloadReplacesSubtree(t *testing.T) {
	tr := newTestTree(t)

	users := findRoot(tr, "Users")
	if users == nil {
		t.Fatal("Users bucket missing")
	}

	nodes, truncated, err := tr.st.Children(users.path, 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	tr.loadBucket(users, nodes, truncated)
	first := len(users.children)
	if first == 0 {
		t.Fatal("no children loaded")
	}

	stale := tr.childIDs(users.id)

	nodes, truncated, err = tr.st.Children(users.path, 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	tr.loadBucket(users, nodes, truncated)

	if len(users.children) != first {
		t.Errorf("after reload %d children, want %d", len(users.children), first)
	}
	if len(tr.childIDs(users.id)) != first {
		t.Errorf("tree reports %d children, want %d", len(tr.childIDs(users.id)), first)
	}
	for _, id := range stale {
		if id != users.id && tr.lookup(id) != nil {
			// Reloading recreates ids, so an old id lingering would mean the
			// previous subtree was never removed from the index.
			if !containsID(users, id) {
				t.Errorf("stale id %s still indexed after reload", id)
			}
		}
	}
}

func containsID(n *node, id string) bool {
	if n.id == id {
		return true
	}
	for _, c := range n.children {
		if containsID(c, id) {
			return true
		}
	}
	return false
}

func TestTreeResetClearsState(t *testing.T) {
	tr := newTestTree(t)

	users := findRoot(tr, "Users")
	if users == nil {
		t.Fatal("Users bucket missing")
	}

	nodes, truncated, err := tr.st.Children(users.path, 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	tr.loadBucket(users, nodes, truncated)

	tr.reset()

	if len(tr.roots) != 0 {
		t.Errorf("%d roots after reset, want 0", len(tr.roots))
	}
	if len(tr.byID) != 0 {
		t.Errorf("%d indexed nodes after reset, want 0", len(tr.byID))
	}
	if ids := tr.childIDs(""); len(ids) != 0 {
		t.Errorf("childIDs returned %v after reset", ids)
	}
	if tr.hasChildren("") {
		t.Error("hasChildren(\"\") = true after reset")
	}
}

func TestFormatSize(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	} {
		if got := formatSize(tc.in); got != tc.want {
			t.Errorf("formatSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEncode(t *testing.T) {
	data := []byte("hello")

	if got := encode(data, viewBase64); got != "aGVsbG8=" {
		t.Errorf("base64 = %q", got)
	}
	if got := encode(data, viewRaw); got != "hello" {
		t.Errorf("raw = %q", got)
	}
	if got := encode(data, viewHex); !strings.Contains(got, "68 65 6c 6c 6f") || !strings.Contains(got, "|hello|") {
		t.Errorf("hex = %q, want a dump containing the bytes and the text", got)
	}

	// Binary data is not silently rendered as text.
	binary := []byte{0x00, 0xff, 0x10}
	if got := encode(binary, viewRaw); got == "" {
		t.Error("raw of binary data produced nothing")
	}
	if got := encode(binary, viewHex); got == "" {
		t.Error("hex of binary data produced nothing")
	}

	if got := encode(nil, viewHex); got != "" {
		t.Errorf("hex of empty = %q, want empty", got)
	}
	if got := encode(nil, viewBase64); got != "" {
		t.Errorf("base64 of empty = %q, want empty", got)
	}
}

func TestIsPrintable(t *testing.T) {
	if !isPrintable([]byte("plain text 123")) {
		t.Error("plain text reported as not printable")
	}
	if !isPrintable([]byte("line\nbreak\ttab")) {
		t.Error("newlines and tabs should stay printable")
	}
	if isPrintable([]byte{0x00}) {
		t.Error("NUL reported as printable")
	}
	if isPrintable([]byte{0xff}) {
		t.Error("0xff reported as printable")
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename("a/b"); got != "a_b" {
		t.Errorf("got %q, want a_b", got)
	}
	if got := sanitizeFilename(""); got != "value.bin" {
		t.Errorf("got %q, want value.bin", got)
	}
}

// TestValuePaneRendersModes builds the pane against a real widget tree to
// confirm the three encodings render and the preview limit is respected.
func TestValuePaneRendersModes(t *testing.T) {
	test.NewTempApp(t)

	tr := newTestTree(t)
	users := findRoot(tr, "Users")
	if users == nil {
		t.Fatal("Users bucket missing")
	}

	nodes, truncated, err := tr.st.Children(users.path, 0)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	tr.loadBucket(users, nodes, truncated)

	target := findByName(users, "user:1")
	if target == nil {
		t.Fatal("user:1 key missing")
	}

	w := &MainWindow{app: test.NewTempApp(t), tree: tr}
	w.window = w.app.NewWindow("test")
	w.buildWidgets()

	p := newValuePane(w)

	data, partial, err := tr.st.Value(target.parent.path, target.name, maxPreviewBytes)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if partial {
		t.Error("small value reported as truncated")
	}

	p.node = target
	p.data = data
	p.partial = partial

	for _, mode := range []viewMode{viewBase64, viewRaw, viewHex} {
		p.setMode(mode)
		if p.text.String() == "" {
			t.Errorf("mode %v rendered nothing", mode.label())
		}
	}

	// A value larger than the preview cap must be truncated, and the metadata
	// must say so rather than implying the whole value is shown.
	big := make([]byte, maxPreviewBytes+64)
	for i := range big {
		big[i] = byte(i)
	}
	data, partial, err = tr.st.Value(target.parent.path, target.name, len(big)+1)
	if err == nil && partial {
		t.Errorf("store did not flag truncation: partial=%v", partial)
	}

	if p.metaText(target, len(data), partial) == "" {
		t.Error("empty metadata text")
	}
}

// TestTreeRowGivesNameTheSpareWidth is the regression test for rows rendering as
// a bare ellipsis.
//
// The row is a border layout, and its name label was inside the left-hand HBox
// with the icon. A border layout gives its left and right objects exactly their
// minimum width, and a truncating label reports a tiny minimum, so the name was
// left with a handful of pixels at any pane width. The name has to be the center
// object for the spare width to reach it.
func TestTreeRowGivesNameTheSpareWidth(t *testing.T) {
	row := newTreeRow(false)
	row.name.SetText("user:1")
	row.tail.SetText("48 B")

	const width = 300
	row.Resize(fyne.NewSize(width, touchTarget))
	row.Refresh()

	// The name must be wide enough to show its text at full length. A label that
	// renders as "…" has roughly the width of the ellipsis alone.
	needed := fyne.MeasureText(row.name.Text, theme.TextSize(), fyne.TextStyle{})
	if row.name.Size().Width < needed.Width {
		t.Errorf("name got %.1fpx, needs %.1fpx to show %q; it would truncate",
			row.name.Size().Width, needed.Width, row.name.Text)
	}

	// And the size column must not have taken the row.
	if row.tail.Size().Width > width/3 {
		t.Errorf("size column is %.1fpx of a %dpx row", row.tail.Size().Width, width)
	}
}

// TestTreeRowTruncatesOnlyGenuinelyLongNames keeps the truncation useful: a name
// that fits is shown whole, and one that cannot fit is cut rather than pushing
// the row wider than the pane.
func TestTreeRowTruncatesOnlyGenuinelyLongNames(t *testing.T) {
	long := strings.Repeat("abcdefghij", 12)
	row := newTreeRow(false)
	row.name.SetText(long)
	row.tail.SetText("48 B")

	row.Resize(fyne.NewSize(300, touchTarget))
	row.Refresh()

	if row.name.Truncation != fyne.TextTruncateEllipsis {
		t.Error("long names are not truncated")
	}
	if got := row.MinSize().Width; got > 300 {
		t.Errorf("row minimum width %v exceeds the 300px pane", got)
	}
	if row.name.Size().Width <= 0 {
		t.Error("long name given no width at all")
	}
}

// TestTreeRowFitsNarrowPane covers the phone case, where the pane is the full
// canvas width and the row has to stay inside it.
func TestTreeRowFitsNarrowPane(t *testing.T) {
	for _, width := range []float32{320, 390, 480} {
		row := newTreeRow(false)
		row.name.SetText("MyBucket_A")
		row.tail.SetText("1.0 MB")

		row.Resize(fyne.NewSize(width, touchTarget))
		row.Refresh()

		if got := row.MinSize().Width; got > width {
			t.Errorf("at %v: row minimum width %v exceeds the pane", width, got)
		}
		if row.name.Size().Width <= 0 || row.tail.Size().Width <= 0 {
			t.Errorf("at %v: name %.1fpx, size %.1fpx", width, row.name.Size().Width, row.tail.Size().Width)
		}
	}
}
