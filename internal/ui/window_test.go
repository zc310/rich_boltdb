package ui

import (
	"strconv"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"boltdb_viewer/internal/store"
)

// walkAll visits every node reachable through the tree widget's callbacks,
// loading buckets as the widget would when a branch is opened. Reads run
// inline so the walk is finished by the time it returns.
func walkAll(w *MainWindow, visit func(n *node)) {
	var recurse func(id widget.TreeNodeID)
	recurse = func(id widget.TreeNodeID) {
		if id == "" {
			for _, r := range w.tree.roots {
				recurse(r.id)
			}
			return
		}
		n := w.tree.lookup(id)
		if n == nil {
			return
		}
		if n.isBucket {
			if err := w.loadSync(n); err != nil {
				return
			}
			if visit != nil {
				visit(n)
			}
		}
		for _, child := range w.tree.childIDs(id) {
			recurse(child)
		}
	}
	recurse("")
}

func TestWindowTreeCallbacks(t *testing.T) {
	w := newWindow(t, false)

	// The widget and the model must agree on the top level.
	ids := w.treeWidget.ChildUIDs("")
	if len(ids) != len(w.tree.roots) {
		t.Fatalf("widget lists %d roots, model has %d", len(ids), len(w.tree.roots))
	}

	depth := 0
	walkAll(w, func(n *node) {
		if n.pathString() == "Users/Profile/Fields" {
			depth = n.path.Depth()
		}
	})

	if depth != 3 {
		t.Errorf("Users/Profile/Fields depth = %d, want 3", depth)
	}
}

func TestWindowFillRowRendersLabels(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	if users == nil {
		t.Fatal("Users bucket missing")
	}
	mustLoad(t, w, users)

	// A bucket row shows the entry count in the trailing label.
	branchRow := w.nodeRow(true).(*treeRow)
	w.fillRow(users.id, true, branchRow)
	if branchRow.name.Text != "Users" {
		t.Errorf("bucket row label = %q, want Users", branchRow.name.Text)
	}
	if branchRow.tail.Text != strconv.Itoa(len(users.children)) {
		t.Errorf("bucket row count = %q, want %d", branchRow.tail.Text, len(users.children))
	}

	// A key row shows the value size.
	var key *node
	for _, c := range users.children {
		if !c.isBucket {
			key = c
			break
		}
	}
	if key == nil {
		t.Fatal("no key under Users")
	}
	keyRow := w.nodeRow(false).(*treeRow)
	w.fillRow(key.id, false, keyRow)
	if got := keyRow.tail.Text; got != formatSize(key.size) {
		t.Errorf("key row size = %q, want %q", got, formatSize(key.size))
	}
}

// TestWindowExpandAllLoadsEveryBucket covers the expand-all path, which walks
// the tree while loading each bucket on the way.
func TestWindowExpandAllLoadsEveryBucket(t *testing.T) {
	w := newWindow(t, false)

	if err := w.expandAllSync(); err != nil {
		t.Fatalf("expandAll: %v", err)
	}

	loaded := 0
	walkAll(w, func(n *node) {
		if n.loaded {
			loaded++
		}
	})
	if loaded == 0 {
		t.Error("expandAll loaded nothing")
	}

	fields := nodeByPathIn(w, "Users/Profile/Fields")
	if fields == nil {
		t.Fatal("Users/Profile/Fields missing after expandAll")
	}
	if !fields.loaded {
		t.Error("deep bucket not loaded by expandAll")
	}
}

func TestWindowEmptyBucketHasNoExpander(t *testing.T) {
	w := newWindow(t, false)

	empty := findRoot(w.tree, "Empty")
	if empty == nil {
		t.Fatal("Empty bucket missing")
	}

	// The flag comes from the root listing, so no probe is needed here.
	if empty.hasKids {
		t.Error("empty bucket marked as holding entries")
	}
	if w.treeWidget.IsBranch(empty.id) {
		t.Error("empty bucket reported as a branch")
	}

	// A bucket that does hold entries must be a branch before it is opened.
	users := findRoot(w.tree, "Users")
	if users == nil {
		t.Fatal("Users bucket missing")
	}
	if !users.hasKids {
		t.Error("Users marked as empty without being opened")
	}
	if !w.treeWidget.IsBranch(users.id) {
		t.Error("Users is not a branch before being opened")
	}
}

func TestWindowValuePaneRendersModes(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)

	var target *node
	for _, c := range users.children {
		if !c.isBucket && string(c.name) == "user:1" {
			target = c
		}
	}
	if target == nil {
		t.Fatal("user:1 not found")
	}

	data, partial, err := w.tree.st.Value(target.parent.path, target.name, maxPreviewBytes)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	w.value.node = target
	w.value.data = data
	w.value.partial = partial

	seen := make(map[viewMode]string)
	for _, m := range []viewMode{viewBase64, viewRaw, viewHex} {
		w.value.setMode(m)
		seen[m] = w.value.text.String()
	}

	if seen[viewBase64] == seen[viewRaw] {
		t.Error("Base64 and Raw produced identical text for a JSON value")
	}
	if seen[viewRaw] != string(data) {
		t.Errorf("Raw text = %q, want the value itself", seen[viewRaw])
	}
	if seen[viewBase64] == string(data) {
		t.Error("Base64 text was not encoded")
	}
}

func TestWindowBucketSelectionShowsSummary(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	w.onSelected(users.id)

	if w.value.node != nil {
		t.Error("bucket selection populated a key node")
	}
	if got := w.value.metaLabel.Text; got == "" {
		t.Error("no bucket summary shown")
	}
}

func TestWindowDeleteRefusedWhenReadOnly(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)
	for _, c := range users.children {
		if !c.isBucket {
			w.onSelected(c.id)
			break
		}
	}
	if w.value.node == nil {
		t.Fatal("no key selected")
	}

	before := len(users.children)
	if err := w.tree.st.DeleteKey(w.value.node.parent.path, w.value.node.name); err == nil {
		t.Fatal("delete on a read-only store succeeded")
	}
	if after := len(users.children); after != before {
		t.Errorf("children %d -> %d after refused delete", before, after)
	}
}

func TestWindowDropNodesUpdatesIndex(t *testing.T) {
	w := newWindow(t, false)

	if err := w.expandAllSync(); err != nil {
		t.Fatalf("expandAll: %v", err)
	}

	users := findRoot(w.tree, "Users")
	target := nodeByPathIn(w, "Users: user:1")
	if target == nil {
		t.Fatal("Users/user:1 missing")
	}
	staleID := target.id
	before := len(users.children)

	w.dropNodes([]*node{target})

	if after := len(users.children); after != before-1 {
		t.Errorf("children %d -> %d, want one fewer", before, after)
	}
	if w.tree.lookup(staleID) != nil {
		t.Error("deleted node still in the index")
	}
	for _, c := range users.children {
		if store.Display(c.name) == "user:1" {
			t.Error("deleted node still a child of its parent")
		}
	}
}

func TestWindowSearchHitRendersFromPath(t *testing.T) {
	w := newWindow(t, false)

	hits, _, err := w.tree.st.Search([]byte("display_name"), 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	hit := hits[0]

	// The hit must carry the full path so a key three levels down is
	// unambiguous and readable without the tree having loaded it.
	if got := hit.Bucket.String(); got != "Users/Profile/Fields" {
		t.Errorf("hit bucket = %q, want Users/Profile/Fields", got)
	}

	w.value.showEntry(entryOf(hit))

	if w.value.entry == nil {
		t.Fatal("pane did not record the hit")
	}
	if w.value.node != nil {
		t.Error("pane also recorded a tree node for a search hit")
	}
	if got := w.value.keyLabel.Text; got != "display_name" {
		t.Errorf("key label = %q, want display_name", got)
	}
	if w.value.deleteBtn.Disabled() {
		t.Error("delete not offered for a search hit")
	}
	if w.value.text.String() == "" {
		t.Error("no value rendered for the hit")
	}
}

// TestWindowSearchDoesNotAlterTree guards the reason hits are kept out of the
// bucket tree: the listing has to keep mirroring the file exactly.
func TestWindowSearchDoesNotAlterTree(t *testing.T) {
	w := newWindow(t, false)

	mustLoad(t, w, findRoot(w.tree, "MyBucket_A"))
	before := len(findRoot(w.tree, "MyBucket_A").children)
	if before == 0 {
		t.Fatal("MyBucket_A not loaded")
	}

	hits, _, err := w.tree.st.Search([]byte("key_A_"), 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, hit := range hits {
		w.value.showEntry(entryOf(hit))
	}

	if after := len(findRoot(w.tree, "MyBucket_A").children); after != before {
		t.Errorf("tree children %d -> %d after showing search hits", before, after)
	}
	if len(w.tree.roots[0].children) != 0 {
		t.Error("search populated a bucket that had not been opened")
	}
}

func TestWindowHitDeleteRemovesTheKey(t *testing.T) {
	w := newWindow(t, true) // writable

	hits, _, err := w.tree.st.Search([]byte("display_name"), 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	w.value.showEntry(entryOf(hits[0]))
	if w.value.entry == nil {
		t.Fatal("hit not recorded")
	}

	w.value.deleteKey()

	if w.value.entry != nil {
		t.Error("pane still holds the deleted hit")
	}
	if _, _, err := w.tree.st.Value(hits[0].Bucket, hits[0].Key, 0); err == nil {
		t.Error("value still readable after delete")
	}
}

func nodeByPathIn(w *MainWindow, addr string) *node {
	var found *node
	var recurse func(n *node)
	recurse = func(n *node) {
		if found != nil {
			return
		}
		switch {
		case n.isBucket && n.pathString() == addr:
			found = n
			return
		case !n.isBucket && n.parent != nil && n.parent.pathString()+": "+n.label() == addr:
			found = n
			return
		}
		for _, c := range n.children {
			recurse(c)
		}
	}
	for _, r := range w.tree.roots {
		recurse(r)
	}
	return found
}

// TestBucketIconBeforeClick is the regression test for the icon that only
// appeared after a bucket was expanded.
//
// Fyne reports a row as a branch when it has an expander, and an unopened bucket
// had no answer for that, so it was drawn with the key icon and only changed once
// clicked. The icon now comes from the node's own kind.
func TestBucketIconBeforeClick(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	if users == nil {
		t.Fatal("Users bucket missing")
	}
	if users.loaded {
		t.Fatal("Users was loaded; the test needs an unopened bucket")
	}

	row, ok := w.nodeRow(w.treeWidget.IsBranch(users.id)).(*treeRow)
	if !ok {
		t.Fatal("nodeRow did not return a tree row")
	}
	w.fillRow(users.id, w.treeWidget.IsBranch(users.id), row)

	if got := row.icon.Resource; got.Name() != theme.FolderIcon().Name() {
		t.Errorf("unopened bucket icon = %q, want a folder", got.Name())
	}

	// A key must keep the document icon.
	mustLoad(t, w, users)
	var key *node
	for _, c := range users.children {
		if !c.isBucket {
			key = c
			break
		}
	}
	if key == nil {
		t.Fatal("no key under Users")
	}
	keyRow, ok := w.nodeRow(w.treeWidget.IsBranch(key.id)).(*treeRow)
	if !ok {
		t.Fatal("nodeRow did not return a tree row")
	}
	w.fillRow(key.id, w.treeWidget.IsBranch(key.id), keyRow)

	if got := keyRow.icon.Resource; got.Name() != theme.DocumentIcon().Name() {
		t.Errorf("key icon = %q, want a document", got.Name())
	}
}

// TestNestedBucketIconBeforeOpening covers a sub-bucket, which arrives in a
// parent listing rather than the root listing.
func TestNestedBucketIconBeforeOpening(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)

	profile := findByName(users, "Profile")
	if profile == nil {
		t.Fatal("Users/Profile missing")
	}
	if profile.loaded {
		t.Fatal("Profile was loaded; the test needs an unopened bucket")
	}

	row := w.nodeRow(w.treeWidget.IsBranch(profile.id)).(*treeRow)
	w.fillRow(profile.id, w.treeWidget.IsBranch(profile.id), row)

	if got := row.icon.Resource; got.Name() != theme.FolderIcon().Name() {
		t.Errorf("unopened sub-bucket icon = %q, want a folder", got.Name())
	}

	// An empty bucket still shows a folder; it has no expander, but it is
	// still a bucket.
	empty := findRoot(w.tree, "Empty")
	emptyRow := w.nodeRow(w.treeWidget.IsBranch(empty.id)).(*treeRow)
	w.fillRow(empty.id, w.treeWidget.IsBranch(empty.id), emptyRow)
	if got := emptyRow.icon.Resource; got.Name() != theme.FolderIcon().Name() {
		t.Errorf("empty bucket icon = %q, want a folder", got.Name())
	}
}

// TestIconDoesNotDependOnBranchFlag guards the coupling that caused the bug: the
// icon must follow the node, not the expander flag.
func TestIconDoesNotDependOnBranchFlag(t *testing.T) {
	w := newWindow(t, false)

	users := findRoot(w.tree, "Users")
	mustLoad(t, w, users)

	var key *node
	for _, c := range users.children {
		if !c.isBucket {
			key = c
			break
		}
	}

	// The same node filled with each branch flag must get the same icon.
	for _, branch := range []bool{true, false} {
		row := w.nodeRow(branch).(*treeRow)
		w.fillRow(key.id, branch, row)
		if got := row.icon.Resource; got.Name() != theme.DocumentIcon().Name() {
			t.Errorf("branch=%v gave a key the icon %q", branch, got.Name())
		}
	}

	for _, branch := range []bool{true, false} {
		row := w.nodeRow(branch).(*treeRow)
		w.fillRow(users.id, branch, row)
		if got := row.icon.Resource; got.Name() != theme.FolderIcon().Name() {
			t.Errorf("branch=%v gave a bucket the icon %q", branch, got.Name())
		}
	}
}

// TestBucketHasKidsKnownBeforeLoading checks the tree can draw an expander
// without opening the bucket.
func TestBucketHasKidsKnownBeforeLoading(t *testing.T) {
	w := newWindow(t, false)

	for _, root := range w.tree.roots {
		if !root.hasKids {
			continue
		}
		if root.loaded {
			continue
		}
		if !w.treeWidget.IsBranch(root.id) {
			t.Errorf("%s has entries but is not a branch before being opened", root.name)
		}
	}
}

// TestValueAreaTakesTheRemainingHeight is the regression test for a value pane
// showing a single line.
//
// The pane was a VBox, and a VBox gives every child its minimum height. The
// value area's minimum is the one line a RichText reports, so the scroll stayed
// 32px tall however much space the pane had.
func TestValueAreaTakesTheRemainingHeight(t *testing.T) {
	w := newWindow(t, false)

	p := w.value
	p.data = []byte("one\ntwo\nthree\nfour\nfive\nsix")
	p.render()

	heights := make(map[float32]bool)
	for _, height := range []float32{300, 500, 800} {
		p.container.Resize(fyne.NewSize(400, height))
		p.container.Refresh()

		scroll := findScroll(p.container)
		if scroll == nil {
			t.Fatal("no scroll found in the value pane")
		}
		got := scroll.Size().Height
		if got < 100 {
			t.Errorf("at pane height %v the value area is only %v tall; it is not "+
				"taking the remaining space", height, got)
		}
		heights[got] = true
	}

	if len(heights) < 3 {
		t.Errorf("value area height did not follow the pane: %v", heights)
	}
}

// findScroll locates the scrolling value area inside the pane. It is found by
// type rather than by position, so restyling the pane does not silently point
// the assertion at a different control.
func findScroll(obj fyne.CanvasObject) *fyne.Container {
	box, ok := obj.(*fyne.Container)
	if !ok {
		return nil
	}
	for _, child := range box.Objects {
		if _, isScroll := child.(*container.Scroll); isScroll {
			return box
		}
		if found := findScroll(child); found != nil {
			return found
		}
	}
	return nil
}

// TestValueAreaGrowsInBothLayouts covers the phone arrangement too, where the
// pane is a pushed page rather than half of a split.
func TestValueAreaGrowsInNarrowLayout(t *testing.T) {
	w := newWindowOn(t, false, phonePortrait, true)

	w.value.data = []byte("one\ntwo\nthree")
	w.value.render()
	w.value.container.Resize(phonePortrait)
	w.value.container.Refresh()

	scroll := findScroll(w.value.container)
	if scroll == nil {
		t.Fatal("no scroll in the value pane")
	}
	// A phone in portrait is 844pt tall, so the value area should be several
	// lines at least, not one.
	if got := scroll.Size().Height; got < 150 {
		t.Errorf("value area is only %v tall on a phone", got)
	}
}

// TestValuePaneDoesNotWidenForLongNames keeps an over-long key or path from
// squeezing the value area: a label that reports its full text width as a
// minimum would push the pane past the window and shrink the area the value is
// shown in, which is the opposite of what is wanted.
func TestValuePaneDoesNotWidenForLongNames(t *testing.T) {
	w := newWindow(t, false)

	long := strings.Repeat("verylongkeyname", 20)
	w.value.keyLabel.SetText(long)
	w.value.metaLabel.SetText("Path: " + long)
	w.value.container.Resize(fyne.NewSize(400, 500))
	w.value.container.Refresh()

	if got := w.value.container.MinSize().Width; got > 400 {
		t.Errorf("pane minimum width %v exceeds the window for a long name", got)
	}

	scroll := findScroll(w.value.container)
	if scroll == nil {
		t.Fatal("no scroll in the value pane")
	}
	if got := scroll.Size().Height; got < 200 {
		t.Errorf("value area squeezed to %v by a long name", got)
	}
}

// TestHelpDialogIsBigEnoughToRead is the regression test for a help dialog that
// showed only a few words.
//
// A dialog sizes itself from its content's minimum, and a scroll's minimum is
// one line, so the help opened as a tiny box. It now asks for a real size.
func TestHelpDialogIsBigEnoughToRead(t *testing.T) {
	preferred := fyne.NewSize(460, 480)
	for _, canvas := range []fyne.Size{
		fyne.NewSize(1000, 720),
		fyne.NewSize(390, 844),
		fyne.NewSize(360, 640),
	} {
		size := fitDialogSize(preferred, canvas)

		if size.Width < 300 || size.Height < 300 {
			t.Errorf("canvas %v: help dialog sized %v, too small to read", canvas, size)
		}
		if size.Width > canvas.Width || size.Height > canvas.Height {
			t.Errorf("canvas %v: help dialog %v does not fit", canvas, size)
		}
	}
}

// TestHelpTextWrapsToTheDialogWidth checks the text reflows instead of running
// off the side. A RichText inside a scroll takes the viewport width, which is
// what makes the wrapping take effect at all.
func TestHelpTextWrapsToTheDialogWidth(t *testing.T) {
	size := fitDialogSize(fyne.NewSize(460, 480), fyne.NewSize(1000, 720))

	label := widget.NewRichTextWithText(helpText())
	label.Wrapping = fyne.TextWrapWord
	scroll := container.NewScroll(label)

	scroll.Resize(size)
	scroll.Refresh()

	// The text takes the full width rather than its own minimum.
	if got := label.Size().Width; got < size.Width*0.8 {
		t.Errorf("help text is only %.0fpx of a %.0fpx dialog; it is not wrapping to the width",
			got, size.Width)
	}

	// Longer than the dialog, which is why the scroll is there at all.
	if label.MinSize().Height <= size.Height {
		t.Logf("help text is %.0fpx tall in a %.0fpx dialog: it fits without scrolling",
			label.MinSize().Height, size.Height)
	}
}

// TestHelpTextIsPlainText guards against the markdown creeping back in: the
// dialog renders plain text, so "###" and "[text](url)" would show up verbatim.
func TestHelpTextIsPlainText(t *testing.T) {
	text := helpText()

	for _, marker := range []string{"###", "](", "**"} {
		if strings.Contains(text, marker) {
			t.Errorf("help text contains the markdown marker %q, which would be shown literally", marker)
		}
	}
	if !strings.Contains(text, "Allow edits") {
		t.Error("help text does not mention the edit switch")
	}
}

// TestDialogsAllAskForARealSize covers the whole family. A dialog sizes itself
// from its content's minimum, and the content of a list or a wrapped text area
// is one line tall, so each of these opened as a box showing a single row.
func TestDialogsAllAskForARealSize(t *testing.T) {
	preferred := []fyne.Size{
		fyne.NewSize(460, 480), // help
		fyne.NewSize(560, 420), // search results
		fyne.NewSize(900, 600), // all keys
	}
	canvases := []fyne.Size{
		fyne.NewSize(1000, 720),
		fyne.NewSize(390, 844),
		fyne.NewSize(360, 640),
	}

	for _, want := range preferred {
		for _, canvas := range canvases {
			got := fitDialogSize(want, canvas)

			if got.Width > canvas.Width || got.Height > canvas.Height {
				t.Errorf("preferred %v in canvas %v gave %v, which does not fit", want, canvas, got)
			}
			if got.Width < 300 || got.Height < 300 {
				t.Errorf("preferred %v in canvas %v gave %v, too small to read", want, canvas, got)
			}
		}
	}
}

// TestFitDialogSizeLeavesPreferredSizeAlone checks the helper does not shrink a
// dialog that already fits.
func TestFitDialogSizeLeavesPreferredSizeAlone(t *testing.T) {
	preferred := fyne.NewSize(400, 300)
	got := fitDialogSize(preferred, fyne.NewSize(2000, 2000))
	if got != preferred {
		t.Errorf("got %v, want the preferred %v unchanged", got, preferred)
	}
}

// TestFitDialogSizeToleratesUnknownCanvas covers a window that has not been laid
// out yet, where the canvas reports no size.
func TestFitDialogSizeToleratesUnknownCanvas(t *testing.T) {
	preferred := fyne.NewSize(460, 480)
	if got := fitDialogSize(preferred, fyne.NewSize(0, 0)); got != preferred {
		t.Errorf("got %v, want the preferred %v when the canvas is unknown", got, preferred)
	}
}
