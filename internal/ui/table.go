package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"boltdb_viewer/internal/store"
)

// tableViewCellLimit is how many bytes of a value a table cell renders. A cell is
// one line in a scrolling grid, so only enough to fill it is read; the detail
// pane shows more when the row is selected.
const tableViewCellLimit = 512

// Logical columns of the flat table.
const (
	colBucket = 0
	colKey    = 1
	colValue  = 2
	colSize   = 3
)

// lastColumn is the highest logical column index, kept so the column count can
// be derived from it.
const lastColumn = colSize

// entryTable is the flat "every key in the file" view: one row per key, with its
// bucket, key, value and size side by side.
//
// The bucket tree answers "what is in this bucket", but not "show me everything
// at once". This answers that. Values are read per visible row rather than up
// front, so a file with a thousand large values does not pull all of them into
// memory to draw one screenful.
type entryTable struct {
	win *MainWindow

	table *widget.Table

	// showBucket is false when every key in the file sits in the same bucket,
	// where the column would repeat one name on every row.
	showBucket bool

	// rows is the loaded page; offset is where it starts in the file.
	rows   []store.Entry
	offset int
	// total is the entry count for the file, used for the scrollbar.
	total int

	// Cached cell text, keyed by absolute row index. A page shift invalidates
	// them, so scrolling back over a page does not re-read anything.
	bucketLabels map[int]string
	keyLabels    map[int]string
	values       map[int]string

	// valuesMode records the encoding the cached values were rendered in. The
	// view mode is switchable, and a cache that ignored it would keep showing
	// Base64 after the user asked for Hex.
	valuesMode viewMode
	// valuesModeSet distinguishes "rendered as the default" from "never
	// rendered", since Base64 is the zero value.
	valuesModeSet bool

	// loading is true while a page read is in flight.
	loading bool
}

// newEntryTable builds the flat view. showBucket decides whether the bucket
// column is displayed.
func newEntryTable(w *MainWindow, showBucket bool) *entryTable {
	t := &entryTable{
		win:          w,
		showBucket:   showBucket,
		bucketLabels: make(map[int]string),
		keyLabels:    make(map[int]string),
		values:       make(map[int]string),
	}

	t.table = widget.NewTableWithHeaders(t.length, t.createCell, t.updateCell)
	t.table.CreateHeader = t.createHeader
	t.table.UpdateHeader = t.updateHeader
	t.table.StickyRowCount = 1
	t.table.OnSelected = t.onSelected

	t.setColumnWidths()

	// The value cells follow the encoding switch in the detail pane; their
	// cache has to go with it.
	onModeChanged(t.modeChanged)

	return t
}

// length reports the grid size. The row count is the file's entry count, so the
// scrollbar reflects the whole file even though only a page is loaded.
func (t *entryTable) length() (rows, cols int) {
	cols = lastColumn + 1
	if !t.showBucket {
		cols--
	}
	return t.total, cols
}

// setColumnWidths gives the value column the lion's share. Bucket and key are
// truncated; a hex dump is the part a reader wants to see.
func (t *entryTable) setColumnWidths() {
	_, cols := t.length()
	for physical := 0; physical < cols; physical++ {
		switch t.logicalOf(physical) {
		case colBucket:
			t.table.SetColumnWidth(physical, 180)
		case colKey:
			t.table.SetColumnWidth(physical, 200)
		case colValue:
			t.table.SetColumnWidth(physical, 380)
		case colSize:
			t.table.SetColumnWidth(physical, 80)
		}
	}
}

// logicalOf maps a physical column to a logical one. When the bucket column is
// hidden, every physical column shifts one to the left.
func (t *entryTable) logicalOf(physical int) int {
	if t.showBucket {
		return physical
	}
	return physical + 1
}

func headerFor(logical int) string {
	switch logical {
	case colBucket:
		return "Bucket"
	case colKey:
		return "Key"
	case colValue:
		return "Value"
	default:
		return "Size"
	}
}

// tableCell is one cell of the grid: a truncating label in a padded container.
//
// A named type rather than a bare container, so the update callbacks can reach
// the label without indexing into a container's object list. An index that
// happens to be wrong produces a panic inside a Fyne layout pass, long after
// the change that caused it.
type tableCell struct {
	*fyne.Container
	label *widget.Label
}

func newTableCell(bold bool) *tableCell {
	label := widget.NewLabel("")
	if bold {
		label.TextStyle = fyne.TextStyle{Bold: true}
	}
	// A key or a hex dump is easily wider than the column, and truncation keeps
	// the grid from forcing a scrollbar across.
	label.Truncation = fyne.TextTruncateEllipsis

	return &tableCell{
		Container: container.NewPadded(label),
		label:     label,
	}
}

// createCell makes the template for a body cell.
func (t *entryTable) createCell() fyne.CanvasObject {
	return newTableCell(false)
}

// createHeader makes the template for a header cell.
func (t *entryTable) createHeader() fyne.CanvasObject {
	return newTableCell(true)
}

// cellLabel unwraps a cell template.
func cellLabel(obj fyne.CanvasObject) *widget.Label {
	cell, ok := obj.(*tableCell)
	if !ok {
		return nil
	}
	return cell.label
}

func (t *entryTable) updateCell(id widget.TableCellID, obj fyne.CanvasObject) {
	label := cellLabel(obj)
	if label == nil {
		return
	}

	switch t.logicalOf(id.Col) {
	case colBucket:
		label.SetText(t.textCell(id.Row, colBucket))
	case colKey:
		label.SetText(t.textCell(id.Row, colKey))
	case colValue:
		label.SetText(t.valueCell(id.Row))
	case colSize:
		label.SetText(t.sizeCell(id.Row))
	}
}

func (t *entryTable) updateHeader(id widget.TableCellID, obj fyne.CanvasObject) {
	label := cellLabel(obj)
	if label == nil {
		return
	}
	label.SetText(headerFor(t.logicalOf(id.Col)))
}

// textCell returns the bucket or key text for a row, from cache, from the loaded
// page, or by asking for the page that holds it.
func (t *entryTable) textCell(row, logical int) string {
	cache := t.keyLabels
	if logical == colBucket {
		cache = t.bucketLabels
	}
	if text, ok := cache[row]; ok {
		return text
	}

	entry, ok := t.entryAt(row)
	if !ok {
		t.loadPageFor(row)
		return ""
	}

	text := store.Display(entry.Key)
	if logical == colBucket {
		text = entry.Bucket.String()
	}
	cache[row] = text
	return text
}

// valueCell returns the value preview for a row.
func (t *entryTable) valueCell(row int) string {
	t.dropValuesIfModeChanged()
	if text, ok := t.values[row]; ok {
		return text
	}

	entry, ok := t.entryAt(row)
	if !ok {
		t.loadPageFor(row)
		return ""
	}

	st := t.win.tree.st
	if st == nil {
		return ""
	}

	data, truncated, err := st.EntryValue(entry, tableViewCellLimit)
	if err != nil {
		t.values[row] = "[unreadable]"
		return t.values[row]
	}

	text := encodeCell(data, t.win.value.mode)
	if truncated {
		// Say so: a preview that stops mid-value reads as the whole value.
		text += " …"
	}

	t.valuesMode = t.win.value.mode
	t.valuesModeSet = true
	t.values[row] = text
	return text
}

// dropValuesIfModeChanged clears the value cache when the encoding has been
// switched, so the cells repaint in the new mode instead of showing the old
// one.
func (t *entryTable) dropValuesIfModeChanged() {
	if !t.valuesModeSet || t.valuesMode == t.win.value.mode {
		return
	}
	t.values = make(map[int]string)
	t.valuesModeSet = false
}

// sizeCell returns the value size for a row.
func (t *entryTable) sizeCell(row int) string {
	entry, ok := t.entryAt(row)
	if !ok {
		t.loadPageFor(row)
		return ""
	}
	return formatSize(entry.Size)
}

// entryAt returns the loaded entry for an absolute row index.
func (t *entryTable) entryAt(row int) (store.Entry, bool) {
	i := row - t.offset
	if i < 0 || i >= len(t.rows) {
		return store.Entry{}, false
	}
	return t.rows[i], true
}

// encodeCell renders a value for a single-line cell. hex.Dump is multi-line,
// which a table row cannot show, so it is cut to the first line.
func encodeCell(data []byte, mode viewMode) string {
	text := encode(data, mode)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx] + " …"
	}
	return text
}

// loadPageFor asks for the page holding row, unless that page is already the one
// loaded.
func (t *entryTable) loadPageFor(row int) {
	if t.loading || t.win.tree.st == nil {
		return
	}
	if row < 0 || row >= t.total {
		// The row does not exist. Fetching a page for it would loop on an empty
		// file, since the page would come back empty and ask for the page again.
		return
	}
	if row >= t.offset && row < t.offset+len(t.rows) {
		return
	}

	// Page on multiples of the page size, so scrolling back within a page reuses
	// the loaded one instead of requesting a page per row.
	t.loadPage((row / store.DefaultPageSize) * store.DefaultPageSize)
}

// loadPage reads a page off the main goroutine.
func (t *entryTable) loadPage(offset int) {
	st := t.win.tree.st
	if st == nil {
		return
	}

	t.loading = true
	t.win.busy(true)

	go func() {
		entries, total, _, err := st.Entries(offset, store.DefaultPageSize)

		fyne.Do(func() {
			t.win.busy(false)
			t.loading = false
			if err != nil {
				t.win.fail(fmt.Errorf("list entries at %d: %w", offset, err))
				return
			}
			t.applyPage(entries, total, offset)
		})
	}()
}

// applyPage installs a page that has been read.
func (t *entryTable) applyPage(entries []store.Entry, total, offset int) {
	if offset == t.offset && len(entries) == len(t.rows) {
		// Same page: only the total changed, so the cached cells still stand.
		if t.total != total {
			t.total = total
			t.table.Refresh()
		}
		return
	}

	t.rows = entries
	t.offset = offset
	t.total = total

	// Cached text is keyed by absolute row, so shifting the page invalidates it.
	t.bucketLabels = make(map[int]string)
	t.keyLabels = make(map[int]string)
	t.values = make(map[int]string)
	t.valuesModeSet = false

	t.table.Refresh()
}

// object returns the view as a canvas object.
func (t *entryTable) object() fyne.CanvasObject { return t.table }

// refresh repaints the grid after the view mode changes.
func (t *entryTable) refresh() {
	t.dropValuesIfModeChanged()
	t.table.Refresh()
}

// modeChanged drops the value cache so the cells repaint in the new encoding.
func (t *entryTable) modeChanged() {
	t.dropValuesIfModeChanged()
	t.table.Refresh()
}

// onSelected shows the selected row in the value pane.
func (t *entryTable) onSelected(id widget.TableCellID) {
	entry, ok := t.entryAt(id.Row)
	if !ok {
		return
	}
	t.win.value.showEntry(entry)
}

// singleBucket reports whether every key in the file shares one bucket, which is
// when the bucket column would repeat the same name on every row and has nothing
// to say.
//
// It asks the store rather than the tree: a file can hold one top-level bucket
// and still nest others inside it, and the tree only knows what has been
// expanded.
func (w *MainWindow) singleBucket() bool {
	if w.tree.st == nil {
		return true
	}

	count, err := w.tree.st.DistinctBucketCount(2)
	if err != nil {
		// Without a count, keep the column: a redundant column is a smaller
		// problem than ambiguous rows.
		return false
	}
	return count <= 1
}

// showAllEntries opens the flat table over the current file.
func (w *MainWindow) showAllEntries() {
	if w.tree.st == nil {
		w.status("Open a file before listing all keys")
		return
	}

	table := newEntryTable(w, !w.singleBucket())
	table.loadPage(0)

	w.showDialog("All keys", "Close", table.object(), fyne.NewSize(900, 600))
}
