package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"boltdb_viewer/internal/store"
)

// phonePortrait and phoneLandscape are the canvas sizes of a typical phone. The
// narrow layout has to work at both.
var (
	phonePortrait  = fyne.NewSize(390, 844)
	phoneLandscape = fyne.NewSize(844, 390)
	tabletSize     = fyne.NewSize(834, 1112)
)

// resize drives a canvas size change through the window, the same path a real
// rotation or window resize takes.
func resize(w *MainWindow, size fyne.Size) {
	w.window.Resize(size)
	w.screen.refresh()
}

func newWindowSized(t *testing.T, writable bool, size fyne.Size) *MainWindow {
	return newWindowOn(t, writable, size, false)
}

// newWindowOn builds a window that believes it is running on a phone when
// mobile is set, because the Fyne test driver always reports a desktop device.
func newWindowOn(t *testing.T, writable bool, size fyne.Size, mobile bool) *MainWindow {
	t.Helper()

	app := fynetest.NewTempApp(t)

	path := t.TempDir() + "/sample.db"
	if err := store.CreateSample(path); err != nil {
		t.Fatalf("create sample: %v", err)
	}

	st, err := store.Open(path, writable)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	w := &MainWindow{app: app, tree: newTree(st), loadInline: true, mobile: mobile}
	w.buildWidgets()
	w.tree.st = st
	w.window = app.NewWindow("test")
	w.dbPath = path

	w.treeWidget = widget.NewTree(w.tree.childIDs, w.tree.hasChildren, w.nodeRow, w.fillRow)
	w.treeWidget.OnSelected = w.onSelected
	w.treeWidget.OnBranchOpened = func(id widget.TreeNodeID) {
		if n := w.tree.lookup(id); n != nil {
			w.open(n)
		}
	}

	if err := w.tree.loadRoots(); err != nil {
		t.Fatalf("loadRoots: %v", err)
	}

	w.value = newValuePane(w)
	w.screen = newLayout(w)
	// SetChecked fires the switch's OnChanged, which reopens the file and kicks
	// off a background load. Detach the handler first: the store is already
	// open in the state this test wants.
	w.editToggle.OnChanged = nil
	w.editToggle.SetChecked(writable)
	w.fileLabel.SetText(path)
	resize(w, size)

	t.Cleanup(func() { _ = st.Close() })
	return w
}

// entryOf converts a search hit to the flat-table entry the value pane reads.
func entryOf(hit store.Hit) store.Entry {
	return store.Entry{Bucket: hit.Bucket, Key: hit.Key, Size: hit.Size}
}

func newWindow(t *testing.T, writable bool) *MainWindow {
	return newWindowSized(t, writable, fyne.NewSize(defaultWidth, defaultHeight))
}

func mustLoad(t *testing.T, w *MainWindow, n *node) {
	t.Helper()
	if n == nil || n.loaded {
		return
	}
	if err := w.loadSync(n); err != nil {
		t.Fatalf("load %s: %v", n.pathString(), err)
	}
}

// findRoot searches the top level.
func findRoot(tr *tree, name string) *node {
	for _, r := range tr.roots {
		if store.Display(r.name) == name {
			return r
		}
	}
	return nil
}

// findByName searches a node's children recursively.
func findByName(n *node, name string) *node {
	if store.Display(n.name) == name {
		return n
	}
	for _, c := range n.children {
		if got := findByName(c, name); got != nil {
			return got
		}
	}
	return nil
}

func TestNarrowLayoutOnPhonePortrait(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	if !w.screen.narrowMode() {
		t.Fatal("phone portrait did not select the single-column layout")
	}
	if w.screen.narrow.Visible() != true {
		t.Error("narrow arrangement is hidden")
	}
	if w.screen.wide.Visible() {
		t.Error("wide arrangement is visible on a phone")
	}
}

func TestNarrowLayoutOnPhoneLandscape(t *testing.T) {
	w := newWindowOn(t, false, phoneLandscape, true)

	// A phone in landscape is wide enough that the split would look right, but
	// each pane would still be too small to read a key or a hex dump, so the
	// single-column layout has to stay in force.
	if !w.screen.narrowMode() {
		t.Fatal("phone landscape did not select the single-column layout")
	}
}

// TestRotationStaysNarrow is the phone case that matters: rotating must not
// flip the app into a two-pane layout it cannot fit.
func TestRotationStaysNarrow(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	resize(w, phoneLandscape)
	if !w.screen.narrowMode() {
		t.Error("rotating to landscape left the single-column layout")
	}

	resize(w, phonePortrait)
	if !w.screen.narrowMode() {
		t.Error("rotating back to portrait left the single-column layout")
	}
}

func TestWideLayoutOnDesktop(t *testing.T) {
	w := newWindow(t, false)

	if w.screen.narrowMode() {
		t.Fatal("desktop size selected the single-column layout")
	}
	if !w.screen.wide.Visible() || w.screen.narrow.Visible() {
		t.Error("desktop should show only the wide arrangement")
	}
}

// TestWindowResizeSwitchesLayout covers a desktop window dragged narrow.
func TestWindowResizeSwitchesLayout(t *testing.T) {
	w := newWindowSized(t, false, fyne.NewSize(1000, 700))
	if w.screen.narrowMode() {
		t.Fatal("wide window started narrow")
	}

	resize(w, fyne.NewSize(500, 700))
	if !w.screen.narrowMode() {
		t.Fatal("narrowing the window did not switch layout")
	}
	if w.screen.wide.Visible() {
		t.Error("wide arrangement still visible after narrowing")
	}

	resize(w, fyne.NewSize(1000, 700))
	if w.screen.narrowMode() {
		t.Fatal("widening the window did not switch back")
	}
	if !w.screen.wide.Visible() {
		t.Error("wide arrangement hidden after widening")
	}
}

// TestDetailPushesOnNarrowAndNotOnWide covers the core phone navigation: on a
// narrow canvas the detail has to be pushed as its own page, on a wide one it
// must not, because there it is already visible beside the tree.
func TestDetailPushesOnNarrowAndNotOnWide(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)

	var target *node
	for _, c := range users.children {
		if !c.isBucket {
			target = c
			break
		}
	}
	if target == nil {
		t.Fatal("no key under Users")
	}

	w.onSelected(target.id)
	if !w.screen.showingDetail {
		t.Error("selecting a key did not push the detail page on a phone")
	}

	w.screen.popDetail()
	if w.screen.showingDetail {
		t.Error("back did not return to the tree")
	}
}

func TestDetailDoesNotPushOnWide(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)

	var target *node
	for _, c := range users.children {
		if !c.isBucket {
			target = c
			break
		}
	}

	w.onSelected(target.id)
	if w.screen.showingDetail {
		t.Error("wide layout pushed a detail page it does not need")
	}
	if w.value.keyLabel.Text == "" {
		t.Error("value not shown on the wide layout")
	}
}

// TestLeavingNarrowResetsNavigation checks the detail page does not linger: a
// user who selects a key on a phone, widens the window, then narrows it again
// should land back on the tree.
func TestLeavingNarrowResetsNavigation(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)
	for _, c := range users.children {
		if !c.isBucket {
			w.onSelected(c.id)
			break
		}
	}
	if !w.screen.showingDetail {
		t.Fatal("detail not pushed")
	}

	resize(w, fyne.NewSize(1000, 700))
	if w.screen.showingDetail {
		t.Error("navigation stack not reset when leaving the narrow layout")
	}

	resize(w, phonePortrait)
	if w.screen.showingDetail {
		t.Error("returning to narrow restored the stale detail page")
	}
}

// TestPushDetailIsIdempotent guards against stacking two copies of the same
// page when a key is selected twice.
func TestPushDetailIsIdempotent(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	w.screen.pushDetail()
	w.screen.pushDetail()
	w.screen.pushDetail()

	if !w.screen.showingDetail {
		t.Error("detail not showing")
	}
	// Back once must return to the tree, not to a duplicate detail page.
	w.screen.popDetail()
	if w.screen.showingDetail {
		t.Error("one Back did not leave the detail page")
	}
}

// TestToolbarWrapsOnNarrow checks the toolbar buttons stay usable rather than
// being squeezed into unreadable slivers by a fixed column count.
// TestToolbarFitsOneRowOnPhone is the point of the icon-only toolbar: eight
// actions must fit across a phone without wrapping, which they could not while
// they carried their labels.
func TestToolbarFitsOneRowOnPhone(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	bar := w.toolbar()
	bar.Resize(phonePortrait)
	bar.Refresh()

	if got := bar.MinSize().Height; got > touchTarget {
		t.Errorf("toolbar wraps onto more than one row on a phone: height %v", got)
	}
	if got := bar.MinSize().Width; got > phonePortrait.Width {
		t.Errorf("toolbar minimum width %v exceeds the phone width %v", got, phonePortrait.Width)
	}
}

// TestToolbarButtonsAreIconOnly guards the reason the toolbar fits: a labelled
// button is about four times as wide as an icon.
func TestToolbarButtonsAreIconOnly(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	for _, obj := range w.toolbar().(*fyne.Container).Objects {
		tip, ok := obj.(*tooltipped)
		if !ok {
			continue
		}
		btn, ok := tip.content.(*widget.Button)
		if !ok {
			t.Errorf("toolbar item %T is not a button", tip.content)
			continue
		}
		if btn.Text != "" {
			t.Errorf("toolbar button carries text %q; it should be icon-only", btn.Text)
		}
		if btn.Icon == nil {
			t.Error("toolbar button has no icon")
		}
	}
}

// TestToolbarButtonsHaveTooltips keeps the labels reachable after the text was
// removed from the buttons.
func TestToolbarButtonsHaveTooltips(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	labelled := 0
	for _, obj := range w.toolbar().(*fyne.Container).Objects {
		tip, ok := obj.(*tooltipped)
		if !ok {
			continue
		}
		if tip.label == "" {
			t.Error("icon-only button with no tooltip label")
			continue
		}
		labelled++
	}
	if labelled == 0 {
		t.Fatal("no tooltipped toolbar buttons")
	}
}

// TestToolbarButtonsKeepAccessibleName covers screen readers: with the text
// gone from the button, the icon name is all Fyne has to announce.
func TestToolbarButtonsKeepAccessibleName(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	for _, obj := range w.toolbar().(*fyne.Container).Objects {
		tip, ok := obj.(*tooltipped)
		if !ok {
			continue
		}
		btn := tip.content.(*widget.Button)
		if btn.AccessibilityLabel() == "" {
			t.Error("toolbar button has no accessibility label")
		}
	}
}

// TestTouchTargetsAreLargeEnough checks the controls a finger hits have a
// usable minimum size.
func TestTouchTargetsAreLargeEnough(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	for _, obj := range w.toolbar().(*fyne.Container).Objects {
		tip, ok := obj.(*tooltipped)
		if !ok {
			continue
		}
		min := tip.MinSize()
		if min.Height < touchTarget || min.Width < touchTarget {
			t.Errorf("toolbar button %q is %vx%v, below the %v touch target", tip.label, min.Width, min.Height, touchTarget)
		}
	}

	if got := w.editToggle.MinSize().Height; got < touchTarget/2 {
		t.Errorf("edit toggle height %v is too small to tap reliably", got)
	}
}

// TestToolbarDoesNotCrowdTheTree keeps the toolbar from eating the tree's
// height, which is what wrapping onto three rows did.
func TestToolbarDoesNotCrowdTheTree(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	bar := w.toolbar()
	bar.Resize(phonePortrait)
	bar.Refresh()

	tree := w.treeWidget.MinSize().Height
	if bar.MinSize().Height > tree {
		t.Errorf("toolbar (%v) is taller than the bucket tree (%v)", bar.MinSize().Height, tree)
	}
}

// TestNarrowLayoutFitsContent checks the value pane has room to render a hex
// dump rather than being clipped to nothing.
func TestNarrowLayoutFitsContent(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)
	for _, c := range users.children {
		if !c.isBucket {
			w.onSelected(c.id)
			break
		}
	}

	if w.value.text.String() == "" {
		t.Fatal("no value rendered")
	}

	// The detail page must have a usable width once the phone canvas is laid
	// out.
	content := w.window.Content()
	content.Resize(phonePortrait)
	content.Refresh()

	min := content.MinSize()
	if min.Width > phonePortrait.Width {
		t.Errorf("content minimum width %v exceeds the phone width %v", min.Width, phonePortrait.Width)
	}
}

// TestStatusAndProgressFitPhoneWidth keeps the bottom bar from forcing the whole
// window wider than the screen.
func TestStatusAndProgressFitPhoneWidth(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	w.status("Opened %s: %d top-level bucket(s)", "a/very/long/path/to/a/database/file.db", 6)
	w.window.Content().Resize(phonePortrait)
	w.window.Content().Refresh()

	if got := w.window.Content().MinSize().Width; got > phonePortrait.Width {
		t.Errorf("bottom bar forces width %v on a %v wide screen", got, phonePortrait.Width)
	}
}

// TestLongKeyTruncatesInTreeRow keeps an over-wide key from forcing the window
// wider than the screen.
func TestLongKeyTruncatesInTreeRow(t *testing.T) {
	row := newTreeRow(false)
	row.name.SetText(strings.Repeat("k", 4000))
	row.Refresh()

	// MinSize must stay bounded: an untruncated label reports the full text
	// width and the container would then demand a 4000px window.
	if got := row.MinSize().Width; got > phonePortrait.Width {
		t.Errorf("row minimum width %v exceeds the phone width %v", got, phonePortrait.Width)
	}
}

// TestTreeRowTouchHeight checks a bucket row clears the touch target, since the
// tree is the control a phone user taps most.
func TestTreeRowTouchHeight(t *testing.T) {
	row := newTreeRow(true)

	if got := row.MinSize().Height; got < touchTarget {
		t.Errorf("tree row height %v is below the %v touch target", got, touchTarget)
	}
}

// TestFillRowTruncatesTheName confirms the row label truncates rather than
// expanding.
func TestFillRowTruncatesTheName(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)

	long := make([]byte, 4000)
	for i := range long {
		long[i] = 'k'
	}
	victim := users.children[0]
	victim.name = long
	_ = victim

	row := newTreeRow(victim.isBucket)
	w.fillRow(victim.id, victim.isBucket, row)

	if row.name.Truncation != fyne.TextTruncateEllipsis {
		t.Error("row label does not truncate")
	}
	if got := row.MinSize().Width; got > defaultWidth {
		t.Errorf("row minimum width %v is too wide", got)
	}
}
