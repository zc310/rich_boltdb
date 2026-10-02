package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"boltdb_viewer/internal/store"
)

// loadTablePage reads one page inline, so the assertions do not have to wait for
// a worker goroutine.
func loadTablePage(t *testing.T, tbl *entryTable, offset int) []store.Entry {
	t.Helper()

	entries, total, _, err := tbl.win.tree.st.Entries(offset, store.DefaultPageSize)
	if err != nil {
		t.Fatalf("Entries(%d): %v", offset, err)
	}
	tbl.applyPage(entries, total, offset)
	return entries
}

func newLoadedTable(t *testing.T, showBucket bool) *entryTable {
	t.Helper()

	w := newWindow(t, false)
	tbl := newEntryTable(w, showBucket)
	loadTablePage(t, tbl, 0)
	return tbl
}

// TestTableRowsCountEveryKey is the core of the request: the flat view must list
// every key in the file, not just the ones in one expanded bucket.
func TestTableRowsCountEveryKey(t *testing.T) {
	tbl := newLoadedTable(t, true)

	entries, total, _, err := tbl.win.tree.st.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	rows, _ := tbl.length()
	if rows != total {
		t.Errorf("table reports %d rows, file has %d keys", rows, total)
	}
	if rows != len(entries) {
		t.Errorf("table reports %d rows but %d keys listed", rows, len(entries))
	}

	// The sample file nests Users/Profile/Fields, so a flat table has to reach
	// keys three levels down as well as the top-level ones.
	seen := make(map[string]bool)
	for _, e := range entries {
		seen[e.Label()] = true
	}
	for _, want := range []string{
		"MyBucket_A: key_A_000000",
		"Users: user:1",
		"Users/Profile: created",
		"Users/Profile/Fields: display_name",
		`logs\/2024: count`,
	} {
		if !seen[want] {
			t.Errorf("flat table missing %q", want)
		}
	}
}

func TestTableColumnsIncludeBucketWhenAmbiguous(t *testing.T) {
	tbl := newLoadedTable(t, true)

	_, cols := tbl.length()
	if cols != lastColumn+1 {
		t.Errorf("with a bucket column: %d columns, want %d", cols, lastColumn+1)
	}

	// The bucket path must be readable, since a bare key is ambiguous across
	// buckets.
	rows, _ := tbl.length()
	found := false
	for row := 0; row < rows; row++ {
		if tbl.textCell(row, colBucket) == "Users/Profile/Fields" {
			found = true
			break
		}
	}
	if !found {
		t.Error("bucket column does not show a nested path")
	}
}

func TestTableColumnsOmitBucketWhenSingle(t *testing.T) {
	// A file whose keys all sit in one bucket does not need the column.
	path := t.TempDir() + "/one.db"
	if err := writeSingleBucket(path); err != nil {
		t.Fatalf("write: %v", err)
	}

	st, err := store.Open(path, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	w := newWindow(t, false)
	w.tree.st = st

	if !w.singleBucket() {
		t.Error("singleBucket false for a file with one flat bucket")
	}

	tbl := newEntryTable(w, false)
	_, cols := tbl.length()
	if cols != lastColumn {
		t.Errorf("without a bucket column: %d columns, want %d", cols, lastColumn)
	}

	// The physical-to-logical mapping must skip column zero without shifting the
	// value column onto the bucket header.
	if got := tbl.logicalOf(0); got != colKey {
		t.Errorf("logicalOf(0) = %d, want %d (Key)", got, colKey)
	}
	if got := tbl.logicalOf(1); got != colValue {
		t.Errorf("logicalOf(1) = %d, want %d (Value)", got, colValue)
	}
}

func TestTableHeadersMatchColumns(t *testing.T) {
	tbl := newLoadedTable(t, true)

	_, cols := tbl.length()
	want := []string{"Bucket", "Key", "Value", "Size"}
	if cols != len(want) {
		t.Fatalf("%d columns, want %d", cols, len(want))
	}

	for physical := 0; physical < cols; physical++ {
		cell, ok := tbl.createHeader().(*tableCell)
		if !ok {
			t.Fatal("createHeader did not return a table cell")
		}
		tbl.updateHeader(widget.TableCellID{Col: physical}, cell)
		if got := cell.label.Text; got != want[physical] {
			t.Errorf("column %d header = %q, want %q", physical, got, want[physical])
		}
	}
}

// TestTableShowsValueForEachRow covers the other half of the request: the value
// has to be visible in the list, not only after selecting a row.
func TestTableShowsValueForEachRow(t *testing.T) {
	tbl := newLoadedTable(t, true)

	rows, _ := tbl.length()
	nonEmpty := 0
	for row := 0; row < rows; row++ {
		if tbl.valueCell(row) != "" {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		t.Fatal("no value rendered in any row")
	}

	// A JSON value must be readable as itself in Raw mode.
	users, _, _, err := tbl.win.tree.st.Entries(0, store.DefaultPageSize)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	for i, e := range users {
		if e.Label() != "Users: user:1" {
			continue
		}
		tbl.win.value.mode = viewRaw
		got := tbl.valueCell(i)
		if got == "" {
			t.Error("user:1 row has no value")
		}
		if !contains(got, "user1@example.com") {
			t.Errorf("user:1 value = %q, want the JSON content", got)
		}
		return
	}
	t.Error("Users: user:1 not listed")
}

func TestTableValueFollowsViewMode(t *testing.T) {
	tbl := newLoadedTable(t, true)

	var row int
	found := false
	all, _, _, err := tbl.win.tree.st.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	for i, e := range all {
		if e.Label() == "Users: user:1" {
			row, found = i, true
			break
		}
	}
	if !found {
		t.Fatal("Users: user:1 not listed")
	}

	tbl.win.value.mode = viewBase64
	base64Text := tbl.valueCell(row)
	tbl.values = make(map[int]string)
	tbl.win.value.mode = viewHex
	hexText := tbl.valueCell(row)

	if base64Text == hexText {
		t.Error("value cell did not change with the view mode")
	}
	if !contains(hexText, "7b 22 69 64 22") {
		t.Errorf("hex cell = %q, want hex bytes", hexText)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestTableSizeColumn(t *testing.T) {
	tbl := newLoadedTable(t, true)

	entries, _, _, err := tbl.win.tree.st.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	shown := 0
	for row := 0; row < len(entries); row++ {
		got := tbl.sizeCell(row)
		if got == "" {
			t.Errorf("row %d has no size", row)
			continue
		}
		if got != formatSize(entries[row].Size) {
			t.Errorf("row %d size = %q, want %q", row, got, formatSize(entries[row].Size))
		}
		shown++
	}
	if shown == 0 {
		t.Error("no sizes rendered")
	}
}

// TestTablePageShiftLoadsBucketNames checks a second page's rows resolve, which
// is where a lazy page would show blanks if the offset were wrong.
func TestTablePageShiftLoadsBucketNames(t *testing.T) {
	w := newWindow(t, false)
	tbl := newEntryTable(w, true)
	loadTablePage(t, tbl, 0)

	all, total, _, err := w.tree.st.Entries(0, 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if total < 2 {
		t.Skip("need at least two entries")
	}

	loadTablePage(t, tbl, 1)
	if tbl.offset != 1 {
		t.Errorf("offset = %d, want 1", tbl.offset)
	}
	if got := tbl.textCell(1, colKey); got == "" {
		t.Error("second page row 1 has no key")
	}
	if got := tbl.textCell(1, colKey); got != store.Display(all[1].Key) {
		t.Errorf("row 1 key = %q, want %q", got, store.Display(all[1].Key))
	}
}

func TestTableCacheSurvivesRowReread(t *testing.T) {
	tbl := newLoadedTable(t, true)

	first := tbl.textCell(0, colKey)
	if first == "" {
		t.Fatal("no key rendered")
	}
	// A second read of the same row must come from cache, not a re-read; if it
	// re-read the entry would still match, so assert the cache is populated.
	if _, ok := tbl.keyLabels[0]; !ok {
		t.Error("key text not cached after first render")
	}
	if got := tbl.textCell(0, colKey); got != first {
		t.Errorf("cached key changed: %q then %q", first, got)
	}
}

func TestTableSelectionShowsValue(t *testing.T) {
	tbl := newLoadedTable(t, true)

	tbl.onSelected(widget.TableCellID{Row: 0, Col: colValue})

	if tbl.win.value.entry == nil && tbl.win.value.node == nil {
		t.Error("selecting a row left the value pane empty")
	}
	if tbl.win.value.data == nil {
		t.Error("selecting a row did not read the value")
	}
}

func TestTableSelectionBeyondPageIsIgnored(t *testing.T) {
	tbl := newLoadedTable(t, true)

	// A row past the loaded page must not render a stale or panicking entry.
	tbl.onSelected(widget.TableCellID{Row: 9999, Col: colKey})

	if tbl.win.value.entry != nil && tbl.win.value.node != nil {
		t.Error("selecting an unloaded row rendered something")
	}
}

func TestTableEmptyFile(t *testing.T) {
	path := t.TempDir() + "/empty.db"
	if err := store.CreateFlat(path, "only", nil); err != nil {
		t.Fatalf("write: %v", err)
	}

	st, err := store.Open(path, false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	w := newWindow(t, false)
	w.tree.st = st

	tbl := newEntryTable(w, false)
	loadTablePage(t, tbl, 0)

	rows, cols := tbl.length()
	if rows != 0 {
		t.Errorf("empty file reports %d rows", rows)
	}
	if cols != lastColumn {
		t.Errorf("empty file has %d columns, want %d", cols, lastColumn)
	}

	// Rendering a row that does not exist must be inert.
	if got := tbl.valueCell(0); got != "" {
		t.Errorf("value of a missing row = %q", got)
	}
}

// TestEncodeCellFlattensHex covers the hex dump being multi-line, which a
// single-line table cell cannot show.
func TestEncodeCellFlattensHex(t *testing.T) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(i)
	}

	got := encodeCell(data, viewHex)
	for i := 0; i < len(got); i++ {
		if got[i] == '\n' {
			t.Errorf("cell text contains a newline: %q", got)
			break
		}
	}
	if got == "" {
		t.Error("hex cell is empty")
	}

	if got := encodeCell(nil, viewHex); got != "" {
		t.Errorf("empty value renders %q", got)
	}
}

func TestTableButtonsPresent(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	labels := make(map[string]bool)
	for _, obj := range w.toolbar().(*fyne.Container).Objects {
		if tip, ok := obj.(*tooltipped); ok {
			labels[tip.label] = true
		}
	}

	if !labels["All keys"] {
		t.Error("no toolbar entry for the flat table")
	}
	if !labels["More"] {
		t.Error("no overflow entry")
	}
}

func TestThemeItemLabelFlips(t *testing.T) {
	w := newWindow(t, false)

	if got := w.themeItemLabel(); got != "Dark theme" {
		t.Errorf("label = %q, want Dark theme", got)
	}
	w.dark = true
	if got := w.themeItemLabel(); got != "Light theme" {
		t.Errorf("label = %q, want Light theme", got)
	}
}

// writeSingleBucket creates a database with one flat bucket and a few keys.
func writeSingleBucket(path string) error {
	return store.CreateFlat(path, "only", []string{"alpha", "beta", "gamma"})
}

var _ = test.Tap
