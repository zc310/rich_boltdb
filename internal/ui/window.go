// Package ui wires the viewer window together.
package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"boltdb_viewer/internal/store"
)

// AppName is the window title and application name.
const AppName = "BoltDB Viewer"

const (
	defaultWidth  = 1000
	defaultHeight = 720
	splitOffset   = 0.36
)

// MainWindow owns the window and everything in it.
type MainWindow struct {
	app    fyne.App
	window fyne.Window

	tree   *tree
	value  *valuePane
	screen *screenLayout

	treeWidget  *widget.Tree
	searchEntry *widget.Entry
	editToggle  *widget.Check
	progress    *widget.ProgressBarInfinite
	statusBar   *widget.Label
	fileLabel   *widget.Label

	// pending counts overlapping background reads so the progress bar only
	// disappears once every one of them has reported back.
	pending int

	// dark tracks the current theme so it can be toggled without a dedicated
	// button per window.
	dark bool

	// mobile reports that the app is running on a phone or tablet. It is read
	// once at startup and kept as a field so the layout decision is testable:
	// the Fyne test driver always reports a desktop device, so a test cannot
	// exercise the phone path through fyne.CurrentDevice() directly.
	mobile bool

	// loadInline makes bucket reads happen on the calling goroutine instead of
	// a worker. Only tests set it, so a load is complete when the tree callback
	// returns and no goroutine outlives the test.
	loadInline bool

	dbPath string
}

// New builds the window for the given app. The caller runs the app afterwards.
func New(a fyne.App) *MainWindow {
	w := &MainWindow{app: a, tree: newTree(nil), mobile: fyne.CurrentDevice().IsMobile()}
	w.window = a.NewWindow(AppName)
	w.window.Resize(fyne.NewSize(defaultWidth, defaultHeight))

	w.treeWidget = widget.NewTree(w.tree.childIDs, w.tree.hasChildren, w.nodeRow, w.fillRow)
	w.treeWidget.OnSelected = w.onSelected
	w.treeWidget.OnBranchOpened = func(id widget.TreeNodeID) {
		if n := w.tree.lookup(id); n != nil {
			w.open(n)
		}
	}

	w.value = newValuePane(w)
	w.buildWidgets()
	w.screen = newLayout(w)
	w.screenHooks()

	return w
}

// buildWidgets creates the controls shared by both layouts. They are created
// once and referenced by each arrangement, so switching layouts keeps the
// search text and the edit switch state.
func (w *MainWindow) buildWidgets() {
	w.searchEntry = widget.NewEntry()
	w.searchEntry.SetPlaceHolder("Search keys by prefix")
	w.searchEntry.OnSubmitted = w.runSearch
	w.searchEntry.Hide()

	w.editToggle = widget.NewCheck("Allow edits", w.toggleWritable)

	w.fileLabel = widget.NewLabel("No file open")
	w.fileLabel.TextStyle = fyne.TextStyle{Italic: true}
	// A database path is easily wider than a phone, and the untruncated label
	// would push the whole layout past the screen edge.
	w.fileLabel.Truncation = fyne.TextTruncateEllipsis

	w.progress = widget.NewProgressBarInfinite()
	w.progress.Hide()

	w.statusBar = widget.NewLabel("Ready")
	w.statusBar.TextStyle = fyne.TextStyle{Italic: true}
	w.statusBar.Truncation = fyne.TextTruncateEllipsis
}

// screenHooks re-evaluates the layout when the app comes back to the
// foreground. Resizes and rotations are handled by the layout pass itself.
func (w *MainWindow) screenHooks() {
	w.app.Lifecycle().SetOnEnteredForeground(func() {
		w.screen.refresh()
	})
}

// Run shows the window and blocks until it closes.
func (w *MainWindow) Run() { w.window.ShowAndRun() }

// toggleTheme flips between the light and dark themes. v1 shipped a fixed
// colour scheme; this is the one deliberate addition beyond it.
func (w *MainWindow) toggleTheme() {
	w.dark = !w.dark
	if w.dark {
		w.app.Settings().SetTheme(theme.DarkTheme())
		w.status("Dark theme")
		return
	}
	w.app.Settings().SetTheme(theme.LightTheme())
	w.status("Light theme")
}

// openFile asks for a file and opens it read-only.
func (w *MainWindow) openFile() {
	dlg := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			w.fail(err)
			return
		}
		if r == nil {
			return
		}
		defer r.Close()
		w.openPath(r.URI().Path())
	}, w.window)
	dlg.SetTitleText("Open BoltDB file")
	dlg.SetFilter(storage.NewExtensionFileFilter([]string{".db", ".bolt", ".dat"}))
	dlg.Show()
}

// openPath closes whatever is open and opens path.
func (w *MainWindow) openPath(path string) {
	w.closeFile()

	w.busy(true)
	st, err := store.Open(path, w.editToggle != nil && w.editToggle.Checked)

	fyne.Do(func() {
		w.busy(false)
		if err != nil {
			w.fail(fmt.Errorf("open %s: %w", path, err))
			return
		}

		w.tree.st = st
		w.dbPath = path
		w.loadRoots()
	})
}

// loadRoots reads the top-level buckets off the main goroutine.
//
// Only the read runs on the worker. Installing the result touches the model's
// maps, which the tree widget reads on the main goroutine, so it has to happen
// there too.
func (w *MainWindow) loadRoots() {
	st := w.tree.st
	if st == nil {
		return
	}

	w.busy(true)
	go func() {
		nodes, err := st.Roots()

		fyne.Do(func() {
			w.busy(false)
			if err != nil {
				w.fail(fmt.Errorf("read %s: %w", w.dbPath, err))
				return
			}
			w.tree.applyRoots(nodes)
			w.value.reset()
			w.refreshTree()
			w.fileLabel.SetText(fmt.Sprintf("%s  (%s)", w.dbPath, formatSize(st.FileSize())))
			w.status("Opened %s: %d top-level bucket(s)", w.dbPath, len(w.tree.roots))
		})
	}()
}

// closeFile releases the database and clears the view.
func (w *MainWindow) closeFile() {
	if w.tree != nil && w.tree.st != nil {
		_ = w.tree.st.Close()
		w.tree.st = nil
	}

	w.tree.reset()
	w.value.reset()
	w.dbPath = ""
	w.fileLabel.SetText("No file open")
	w.refreshTree()
	w.status("Closed")
}

// toggleWritable reopens the file so edits become possible. Switching modes
// closes the current handle first, otherwise bbolt refuses to remap a file it
// already holds.
func (w *MainWindow) toggleWritable(on bool) {
	if w.dbPath == "" {
		if on {
			w.editToggle.SetChecked(false)
			w.status("Open a file first")
		}
		return
	}

	w.busy(true)
	path := w.dbPath
	_ = w.tree.st.Close()
	w.tree.st = nil

	st, err := store.Open(path, on)

	fyne.Do(func() {
		w.busy(false)
		if err != nil {
			w.fail(fmt.Errorf("reopen %s: %w", path, err))
			w.editToggle.SetChecked(!on)
			return
		}
		w.tree.st = st
		w.loadRoots()
		if on {
			w.status("%s is now editable: keys and buckets can be deleted", path)
		} else {
			w.status("%s is now read-only", path)
		}
	})
}

// dropNodes removes entries after a delete and refreshes the parent listing.
func (w *MainWindow) dropNodes(removed []*node) {
	byID := make(map[*node]bool, len(removed))
	for _, n := range removed {
		byID[n] = true
	}

	// Only direct children of a common parent can be dropped cheaply; for
	// anything else the parent's listing is reloaded so the result is exact.
	for parentID := range groupByParent(removed) {
		parent := w.tree.lookup(parentID)
		if parent == nil || !parent.isBucket {
			continue
		}

		kept := make([]*node, 0, len(parent.children))
		for _, c := range parent.children {
			if !byID[c] {
				kept = append(kept, c)
			} else {
				w.tree.unindex(c)
			}
		}
		parent.children = kept
		parent.hasKids = len(kept) > 0
	}
}

func groupByParent(removed []*node) map[string][]*node {
	out := make(map[string][]*node)
	for _, n := range removed {
		if n.parent == nil {
			continue
		}
		out[n.parent.id] = append(out[n.parent.id], n)
	}
	return out
}

// createSample writes a demonstration database and opens it.
func (w *MainWindow) createSample() {
	path := store.SamplePath()

	w.busy(true)
	go func() {
		err := store.CreateSample(path)

		fyne.Do(func() {
			w.busy(false)
			if err != nil {
				w.fail(fmt.Errorf("create sample: %w", err))
				return
			}
			w.openPath(path)
		})
	}()
}

// toggleSearch shows or hides the search field.
func (w *MainWindow) toggleSearch() {
	if w.searchEntry.Visible() {
		w.searchEntry.Hide()
		w.treeWidget.UnselectAll()
		w.value.reset()
		w.refreshTree()
		w.status("Search cleared")
		return
	}
	w.searchEntry.Show()
	w.window.Canvas().Focus(w.searchEntry)
}

// runSearch looks for a key prefix across every bucket at every depth.
func (w *MainWindow) runSearch(prefix string) {
	if w.tree.st == nil {
		w.status("Open a file before searching")
		return
	}
	if prefix == "" {
		w.status("Type a prefix to search for")
		return
	}

	w.busy(true)
	st := w.tree.st

	go func() {
		hits, truncated, err := st.Search([]byte(prefix), 0)

		fyne.Do(func() {
			w.busy(false)
			if err != nil {
				w.fail(fmt.Errorf("search %q: %w", prefix, err))
				return
			}
			w.showResults(hits, truncated)
		})
	}()
}

// showResults presents the hits in a list. The hits are not grafted into the
// bucket tree: a hit can live in a bucket the tree has not loaded, and adding
// a synthetic node to a real listing would mix invented entries into a listing
// that is supposed to mirror the file. Reading the value straight from the hit
// keeps the tree honest, and the hit already carries the full bucket path.
func (w *MainWindow) showResults(hits []store.Hit, truncated bool) {
	if len(hits) == 0 {
		msg := "No key starts with that prefix."
		if truncated {
			msg = "No key found within the search limit."
		}
		dialog.ShowInformation("Search", msg, w.window)
		w.status("No matches")
		return
	}

	w.showHitList(hits)

	status := fmt.Sprintf("%d match(es) for the prefix", len(hits))
	if truncated {
		status += " (capped by the search limit)"
	}
	w.status("%s", status)
}

// onSelected reacts to a tree selection.
func (w *MainWindow) onSelected(id widget.TreeNodeID) {
	n := w.tree.lookup(id)
	if n == nil {
		return
	}

	if n.isBucket {
		w.open(n)
		w.value.showBucket(n)
		w.refreshTree()
		return
	}

	// On a phone the panes are separate pages, so selecting a key means
	// leaving the tree to read the value.
	w.screen.pushDetail()
	w.value.showKey(n)
}

// showHelp keeps v1's explanation of what Bolt is.
// helpText is the help body, written as plain text.
//
// It was markdown in v1, rendered by flutter_markdown, and carried over as
// markdown by mistake: the dialog shows a RichText, so "### Reading" and
// "[Bolt](url)" would appear with their punctuation intact.
func helpText() string {
	return `BoltDB Viewer reads BoltDB (bbolt) files, a pure Go key/value
store inspired by Howard Chu's LMDB project. It is meant to be a
low-level building block, so the API is small: put a value, get a value.

READING

  Open          Opens a file read-only. Nothing on disk can change.
  Expand        Expands every bucket in the file.
  The tree      Buckets nest to any depth. Click one to open it.
  A key         Selecting it reads the value, as Base64, Raw or Hex.
  All keys      Lists every key in the file, with its bucket and value.
  Search        Matches a key prefix across every bucket, at every depth.
  Export        Writes the whole value to a file. Values over 1 MB are
                shown truncated, but exported in full.

EDITING

  Allow edits   Reopens the file for writing. Leave it off while browsing.
  Delete        Removes the selected key, or the selected bucket with
                everything inside it. Deletions cannot be undone.

  Search results are listed with the full bucket path, so a key found at
  two levels down is still distinguishable from the same name at the top.`
}

// showHelp opens the help dialog.
func (w *MainWindow) showHelp() {
	label := widget.NewRichTextWithText(helpText())
	label.Wrapping = fyne.TextWrapWord

	w.showDialog("Help", "Close", container.NewScroll(label), fyne.NewSize(460, 480))
}
