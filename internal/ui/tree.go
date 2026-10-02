// Package ui builds the viewer window.
//
// All model state lives on the Fyne main goroutine. Database reads run on
// worker goroutines and post their results back with fyne.Do, which is what
// Fyne requires for touching widgets. Keeping the model single-goroutine means
// the tree needs no locks: the previous version guarded it with an RWMutex
// whose recursive remove helper deadlocked against itself.
package ui

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"boltdb_viewer/internal/store"
)

// maxPreviewBytes caps how much of a value is rendered in the detail panel.
// Values are read lazily and only this many bytes are pulled out of the
// transaction, so opening a file with a multi-megabyte value stays instant.
const maxPreviewBytes = 1024 * 1024

// tree is the lazy model behind the bucket tree.
//
// Buckets are loaded on demand, so expanding a node does not walk the whole
// file. Leaf nodes hold only the key and the value length; the value itself is
// read from disk when the key is selected.
type tree struct {
	st     *store.Store
	roots  []*node
	byID   map[string]*node
	nextID int
}

type node struct {
	id     string
	name   []byte
	path   store.Path // bucket path; nil for a key node
	parent *node

	isBucket  bool
	size      int64
	loaded    bool
	truncated bool

	// hasKids records whether a bucket holds at least one entry, as read
	// alongside the parent listing. It is what lets an unopened bucket be drawn
	// with the folder icon and the right expander: without it the tree has to
	// load a bucket to know how to display it, which is what made a bucket look
	// like a key until it was clicked.
	hasKids bool

	children []*node
}

func newTree(st *store.Store) *tree {
	return &tree{st: st, byID: make(map[string]*node)}
}

func (t *tree) newID() string {
	t.nextID++
	return strconv.Itoa(t.nextID)
}

func (t *tree) lookup(id string) *node { return t.byID[id] }

// reset drops the whole model, used when a different file is opened.
func (t *tree) reset() {
	t.roots = nil
	t.byID = make(map[string]*node)
	t.nextID = 0
}

// applyRoots installs the top-level buckets that have already been read. Must
// run on the main goroutine: it rewrites the model's maps.
func (t *tree) applyRoots(nodes []store.Node) {
	t.reset()
	for _, entry := range nodes {
		n := &node{
			id:       t.newID(),
			name:     entry.Name,
			path:     store.NewPath(string(entry.Name)),
			isBucket: true,
			hasKids:  entry.HasKids,
		}
		t.roots = append(t.roots, n)
		t.byID[n.id] = n
	}
}

// loadRoots reads and installs the top-level buckets on the calling goroutine.
// Only for tests: the interactive path is loadRoots on MainWindow, which reads
// on a worker and applies the result on the main goroutine.
func (t *tree) loadRoots() error {
	nodes, err := t.st.Roots()
	if err != nil {
		return err
	}
	t.applyRoots(nodes)
	return nil
}

// loadBucket fills in a bucket's children. Must run on the main goroutine,
// after the read has already happened.
func (t *tree) loadBucket(n *node, nodes []store.Node, truncated bool) {
	// Drop the old descendants from the index first, so a reload cannot leave
	// stale ids behind. The node itself stays indexed: it is still the same
	// bucket, and the tree holds on to its id while open.
	for _, c := range n.children {
		t.unindex(c)
	}
	n.children = nil

	for _, entry := range nodes {
		child := &node{
			id:       t.newID(),
			name:     entry.Name,
			isBucket: entry.IsBucket,
			size:     entry.Size,
			hasKids:  entry.HasKids,
			parent:   n,
		}
		if entry.IsBucket {
			child.path = n.path.Child(entry.Name)
		}
		n.children = append(n.children, child)
		t.byID[child.id] = child
	}

	n.loaded = true
	n.truncated = truncated
}

// unindex removes a subtree from the id index.
func (t *tree) unindex(n *node) {
	delete(t.byID, n.id)
	for _, c := range n.children {
		t.unindex(c)
	}
}

// childIDs is the callback the Fyne tree uses to enumerate rows.
func (t *tree) childIDs(id widget.TreeNodeID) []widget.TreeNodeID {
	if id == "" {
		ids := make([]widget.TreeNodeID, len(t.roots))
		for i, n := range t.roots {
			ids[i] = n.id
		}
		return ids
	}

	n := t.lookup(id)
	if n == nil || !n.isBucket {
		return nil
	}

	ids := make([]widget.TreeNodeID, len(n.children))
	for i, c := range n.children {
		ids[i] = c.id
	}
	return ids
}

// hasChildren drives the expander arrow. An unloaded bucket answers from the
// listing read alongside it, so a node with nothing inside it never shows an
// arrow that opens onto an empty listing.
//
// This says nothing about the row's icon. Fyne passes the same flag to the row
// builder, which made an unopened bucket draw as a key: it had no expander yet,
// so the icon came from the wrong branch. fillRow sets the icon from the node.
func (t *tree) hasChildren(id widget.TreeNodeID) bool {
	if id == "" {
		return len(t.roots) > 0
	}

	n := t.lookup(id)
	if n == nil || !n.isBucket {
		return false
	}
	if n.loaded {
		return len(n.children) > 0
	}
	return n.hasKids
}

// pathOf renders a node's bucket path for display.
func (n *node) pathString() string {
	if n.path == nil {
		return ""
	}
	return n.path.String()
}

// label is what the tree row shows.
func (n *node) label() string {
	if n.isBucket {
		return string(n.name)
	}
	return store.Display(n.name)
}

// open loads a bucket if it has not been read yet. It is called from the tree
// callbacks and from expandAll.
func (w *MainWindow) open(n *node) {
	if n == nil || !n.isBucket || n.loaded {
		return
	}

	// Tests read inline so a bucket is fully loaded by the time the callback
	// returns, and no worker goroutine outlives the test that started it.
	if w.loadInline {
		if err := w.loadSync(n); err != nil {
			w.fail(err)
		}
		return
	}
	w.load(n)
}

// load reads one bucket off the main goroutine and applies the result back on
// it.
func (w *MainWindow) load(n *node) {
	st := w.tree.st
	if st == nil {
		return
	}

	w.busy(true)
	go func() {
		nodes, truncated, err := st.Children(n.path, 0)

		fyne.Do(func() {
			w.busy(false)
			if err != nil {
				w.fail(fmt.Errorf("load bucket %s: %w", n.path, err))
				return
			}
			w.applyChildren(n, nodes, truncated)
		})
	}()
}

// applyChildren installs a listing that has already been read. Split out from
// load so the same code path runs whether the read happened on a worker
// goroutine or inline.
func (w *MainWindow) applyChildren(n *node, nodes []store.Node, truncated bool) {
	w.tree.loadBucket(n, nodes, truncated)
	w.refreshTree()

	switch {
	case truncated:
		w.status("Loaded %s: showing first %d entries", n.path, len(nodes))
	case len(nodes) == 0:
		w.status("%s is empty", n.path)
	default:
		w.status("Loaded %s: %d entries", n.path, len(nodes))
	}
}

// loadSync reads a bucket on the calling goroutine. Only for tests: the
// interactive path goes through load so a slow read cannot freeze the window.
func (w *MainWindow) loadSync(n *node) error {
	if n == nil || !n.isBucket {
		return fmt.Errorf("not a bucket node")
	}
	nodes, truncated, err := w.tree.st.Children(n.path, 0)
	if err != nil {
		return err
	}
	w.applyChildren(n, nodes, truncated)
	return nil
}

// refreshTree redraws the tree widget.
func (w *MainWindow) refreshTree() {
	w.treeWidget.Refresh()
}

// setStatus writes the status bar.
func (w *MainWindow) status(format string, args ...any) {
	w.statusBar.SetText(fmt.Sprintf(format, args...))
}

// fail reports an error to the user and leaves the status bar honest.
func (w *MainWindow) fail(err error) {
	w.status("Error: %v", err)
	dialog.ShowError(err, w.window)
}

// busy drives the indeterminate progress bar. It counts nested calls so an
// expand-all that opens many buckets does not flicker the bar off between them.
func (w *MainWindow) busy(on bool) {
	if on {
		w.pending++
		if w.pending == 1 {
			w.progress.Show()
			w.progress.Start()
		}
		return
	}

	if w.pending > 0 {
		w.pending--
	}
	if w.pending == 0 {
		w.progress.Stop()
		w.progress.Hide()
	}
}

// expandAll expands every bucket in the file.
//
// The read pass runs on a worker goroutine and collects the listings keyed by
// bucket path; the main goroutine then applies them in the order they were
// collected, which is parent before child, so every node it has to address
// already exists. Loading bucket-by-bucket through the interactive path would
// not work here: expandNode has no way to wait for an in-flight read, so it
// would expand branches that had no children yet.
func (w *MainWindow) expandAll() {
	roots := w.rootsSnapshot()
	if len(roots) == 0 {
		return
	}

	w.busy(true)
	go func() {
		lists, err := w.collectListings(roots)

		fyne.Do(func() {
			w.busy(false)
			if err != nil {
				w.fail(err)
				return
			}
			w.applyListings(lists)
		})
	}()
}

// collectListings reads every bucket below roots, depth first, and returns the
// listings in parent-before-child order.
func (w *MainWindow) collectListings(roots []store.Path) ([]listing, error) {
	st := w.tree.st
	if st == nil {
		return nil, fmt.Errorf("no database open")
	}

	lists := make([]listing, 0, 32)
	var walk func(p store.Path) error
	walk = func(p store.Path) error {
		nodes, truncated, err := st.Children(p, 0)
		if err != nil {
			return err
		}
		lists = append(lists, listing{path: p, nodes: nodes, truncated: truncated})

		for _, e := range nodes {
			if e.IsBucket {
				if err := walk(p.Child(e.Name)); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, root := range roots {
		if err := walk(root); err != nil {
			return nil, err
		}
	}
	return lists, nil
}

// applyListings installs collected listings and opens every branch.
func (w *MainWindow) applyListings(lists []listing) {
	for _, l := range lists {
		n := w.nodeForPath(l.path)
		if n == nil {
			continue
		}
		w.tree.loadBucket(n, l.nodes, l.truncated)
		w.treeWidget.OpenBranch(n.id)
	}
	w.refreshTree()
	w.status("Expanded %d bucket(s)", len(lists))
}

// expandAllSync is expandAll without the goroutine handoff. Only for tests.
func (w *MainWindow) expandAllSync() error {
	lists, err := w.collectListings(w.rootsSnapshot())
	if err != nil {
		return err
	}
	w.applyListings(lists)
	return nil
}

// listing is one bucket's entries, captured before it is applied to the tree.
type listing struct {
	path      store.Path
	nodes     []store.Node
	truncated bool
}

// rootsSnapshot copies the current top-level paths so the worker goroutine does
// not read the model.
func (w *MainWindow) rootsSnapshot() []store.Path {
	out := make([]store.Path, 0, len(w.tree.roots))
	for _, r := range w.tree.roots {
		out = append(out, r.path)
	}
	return out
}

// nodeForPath finds the node for a bucket path, matching on the display form
// of each segment. It creates nothing: a segment that is not in the tree means
// the caller has not applied the listing for that level yet.
func (w *MainWindow) nodeForPath(p store.Path) *node {
	if !p.Valid() {
		return nil
	}

	var current *node
	for i, seg := range p {
		want := store.Display(seg)

		if current == nil {
			// Top level: the roots were created by loadRoots.
			for _, r := range w.tree.roots {
				if store.Display(r.name) == want {
					current = r
					break
				}
			}
		} else {
			for _, c := range current.children {
				if c.isBucket && store.Display(c.name) == want {
					current = c
					break
				}
			}
		}

		if current == nil {
			return nil
		}
		if i == len(p)-1 {
			return current
		}
	}
	return current
}

func (w *MainWindow) collapseAll() {
	for _, root := range w.tree.roots {
		w.collapseNode(root)
	}
	w.refreshTree()
}

func (w *MainWindow) collapseNode(n *node) {
	if !n.isBucket {
		return
	}
	w.treeWidget.CloseBranch(n.id)
	for _, c := range n.children {
		w.collapseNode(c)
	}
}

// treeRow is one row of the bucket tree.
//
// It is a distinct type rather than a bare container so fillRow can reach the
// two labels it updates. Digging them out by index made the row layout fragile:
// the positions depend on how the containers are nested, and a change turns
// into a panic deep inside a Fyne layout pass instead of a compile error.
//
// It is also a custom widget so its minimum height can be raised to the touch
// target. A container would only ever report the sum of its padding and its
// icon, which falls a pixel short of a comfortable tap target.
type treeRow struct {
	widget.BaseWidget

	icon *widget.Icon
	name *widget.Label
	tail *widget.Label
	box  fyne.CanvasObject
}

// newTreeRow builds a row.
//
// The icon starts as a placeholder and is set from the node's own kind in
// fillRow. Fyne's branch flag says whether a row has an expander, not whether
// the entry is a bucket, so keying the icon off it drew every unopened bucket as
// a document until it was clicked.
//
// The name label takes the spare width and truncates, because a bbolt key is an
// arbitrary byte string and can be wider than any pane. The row is padded so it
// also clears the minimum height a fingertip needs.
func newTreeRow(_ bool) *treeRow {
	icon := theme.DocumentIcon()

	row := &treeRow{
		icon: widget.NewIcon(icon),
		name: widget.NewLabel(""),
		tail: widget.NewLabel(""),
	}
	row.name.Truncation = fyne.TextTruncateEllipsis

	// The icon and the size column take only what they need; the name is the
	// center object, which is what receives the spare width.
	//
	// Putting the name inside the left-hand HBox instead is the trap here: a
	// border layout gives its left and right objects exactly their minimum
	// width, and a truncating label reports a very small minimum. The name was
	// left with a few pixels and every row rendered as an ellipsis.
	row.box = container.NewPadded(
		container.NewBorder(
			nil, nil,
			row.icon,
			row.tail,
			row.name,
		),
	)

	row.ExtendBaseWidget(row)
	return row
}

// MinSize raises the row height to the touch target.
func (r *treeRow) MinSize() fyne.Size {
	size := r.BaseWidget.MinSize()
	if size.Height < touchTarget {
		size.Height = touchTarget
	}
	return size
}

func (r *treeRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.box)
}

// nodeRow builds one tree row for the Fyne tree widget.
func (w *MainWindow) nodeRow(branch bool) fyne.CanvasObject {
	return newTreeRow(branch)
}

// iconFor picks the row icon from the entry itself rather than from Fyne's
// branch flag, so a bucket always shows a folder, whether or not it has been
// expanded yet, and a key always shows a document.
func iconFor(n *node) fyne.Resource {
	if n.isBucket {
		return theme.FolderIcon()
	}
	return theme.DocumentIcon()
}

// fillRow writes the text for one tree row. The trailing label carries the
// entry count for buckets and the size for keys.
func (w *MainWindow) fillRow(id widget.TreeNodeID, branch bool, obj fyne.CanvasObject) {
	n := w.tree.lookup(id)
	if n == nil {
		return
	}

	row, ok := obj.(*treeRow)
	if !ok {
		return
	}

	// A bbolt key is an arbitrary byte string and can be far wider than any
	// pane. Without truncation the label would push the row's minimum width
	// out past the screen, which on a phone makes the whole window scroll
	// sideways.
	row.name.SetText(n.label())
	row.icon.SetResource(iconFor(n))

	if branch {
		switch {
		case n.truncated:
			// The listing was capped, so the count is a lower bound.
			row.tail.SetText(fmt.Sprintf("%d+", len(n.children)))
		case !n.loaded:
			row.tail.SetText("")
		default:
			row.tail.SetText(strconv.Itoa(len(n.children)))
		}
		return
	}
	row.tail.SetText(formatSize(n.size))
}
