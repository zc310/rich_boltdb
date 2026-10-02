package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// narrowBreakpoint is the canvas width below which the window switches to the
// single-column phone layout.
//
// The two-pane split needs room for a readable bucket tree and a readable hex
// dump at the same time. Below this width it produces two unusable slivers, so
// the panes are stacked and the detail is pushed onto a navigation stack with a
// back button instead.
const narrowBreakpoint = 720

// touchTarget is the minimum height of an interactive control. Fyne's default
// button height is comfortable for a mouse but small for a fingertip, and a
// tree row is the control a phone user taps most.
const touchTarget = 44

// layoutMode selects between the two arrangements.
type layoutMode int

const (
	// modeWide is the desktop arrangement: tree and detail side by side.
	modeWide layoutMode = iota
	// modeNarrow is the phone arrangement: one page at a time, with a back
	// button to return from the detail to the tree.
	modeNarrow
)

// screenLayout holds both arrangements. They are built once and one is shown, so a
// rotation or a window resize does not rebuild widgets or lose state such as
// the search text and scroll position.
type screenLayout struct {
	win *MainWindow

	root   *fyne.Container
	wide   fyne.CanvasObject
	narrow fyne.CanvasObject
	// nav is the navigation container inside the phone arrangement.
	nav *container.Navigation

	mode layoutMode

	// detailPage is the value pane, pushed on top of the tree on a phone.
	detailPage fyne.CanvasObject
	// showingDetail tracks whether the detail page is currently pushed. The
	// Navigation widget keeps its stack depth private, so the depth is mirrored
	// here.
	showingDetail bool
}

// newLayout builds the window content and installs it.
func newLayout(w *MainWindow) *screenLayout {
	l := &screenLayout{win: w}

	treePage := w.treePage()
	l.detailPage = w.value.container

	l.wide = l.buildWide(treePage, l.detailPage)
	l.narrow = l.buildNarrow(treePage, l.detailPage)

	// The stack holds both arrangements; the layout callback below decides
	// which one is visible for the current size.
	l.root = container.NewStack(l.wide, l.narrow)
	l.root.Layout = &responsiveStack{l: l}
	w.window.SetContent(l.root)

	l.apply(w.narrowCanvas())
	return l
}

// buildWide is the desktop arrangement.
func (l *screenLayout) buildWide(treePage, detailPage fyne.CanvasObject) fyne.CanvasObject {
	left := container.NewBorder(
		l.win.toolbar(),
		nil, nil, nil,
		container.NewScroll(l.win.treeWidget),
	)
	right := container.NewBorder(
		l.win.header(),
		nil, nil, nil,
		detailPage,
	)

	split := container.NewHSplit(left, right)
	split.Offset = splitOffset

	return l.withStatusBar(split)
}

// buildNarrow is the phone arrangement: the tree is the root page and the
// detail is pushed onto it, so the navigation bar's back arrow returns to the
// list.
func (l *screenLayout) buildNarrow(treePage, detailPage fyne.CanvasObject) fyne.CanvasObject {
	l.nav = container.NewNavigation(treePage)
	nav := l.nav
	nav.Title = AppName

	// The detail page carries the file name and the edit switch, which sit
	// beside the tree in the wide layout and would be unreachable here.
	l.detailPage = container.NewBorder(
		container.NewVBox(
			container.NewBorder(nil, nil, nil, l.win.editToggle, l.win.fileLabel),
			widget.NewSeparator(),
		),
		nil, nil, nil,
		detailPage,
	)

	return l.withStatusBar(nav)
}

// withStatusBar puts the progress bar and status line under a screenLayout.
func (l *screenLayout) withStatusBar(content fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(
		nil,
		container.NewVBox(
			widget.NewSeparator(),
			container.NewBorder(nil, nil, nil, l.win.progress, l.win.statusBar),
		),
		nil, nil,
		content,
	)
}

// apply switches to the arrangement for a narrow or wide canvas.
func (l *screenLayout) apply(narrow bool) {
	want := modeWide
	if narrow {
		want = modeNarrow
	}

	l.narrow.Hide()
	l.wide.Show()

	if want == modeNarrow {
		l.narrow.Show()
		l.wide.Hide()
	}

	// Leaving the phone arrangement must reset the navigation stack, or coming
	// back to it lands on a stale detail page instead of the tree.
	if want == modeWide && l.showingDetail {
		l.showingDetail = false
		l.nav.Back()
	}

	l.mode = want
}

// responsiveStack is the layout of the root stack. Fyne runs a layout
// whenever a container is resized, which includes every window resize and every
// orientation change, so switching arrangements off the layout pass needs no
// separate hook and cannot miss an event.
type responsiveStack struct {
	l *screenLayout
}

func (r *responsiveStack) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, obj := range objs {
		obj.Resize(size)
	}
	r.l.apply(size.Width > 0 && size.Width < narrowBreakpoint)
}

// MinSize returns the larger of the two arrangements so the stack can shrink
// to whichever one is showing without clipping.
func (r *responsiveStack) MinSize(objs []fyne.CanvasObject) fyne.Size {
	size := fyne.NewSize(0, 0)
	for _, obj := range objs {
		if !obj.Visible() {
			continue
		}
		min := obj.MinSize()
		if min.Width > size.Width {
			size.Width = min.Width
		}
		if min.Height > size.Height {
			size.Height = min.Height
		}
	}
	return size
}

// refresh re-evaluates the canvas size. The layout pass above does this
// automatically; this is for the cases where nothing triggers one, such as the
// app returning to the foreground.
func (l *screenLayout) refresh() {
	if l == nil {
		return
	}
	l.apply(l.win.narrowCanvas())
}

// narrow reports whether the phone arrangement is active.
func (l *screenLayout) narrowMode() bool { return l != nil && l.mode == modeNarrow }

// pushDetail shows the value page, which on a phone means leaving the tree.
// Pushing twice would stack two copies of the same page, so the state is
// tracked here.
func (l *screenLayout) pushDetail() {
	if l == nil || l.mode != modeNarrow || l.showingDetail {
		return
	}
	l.showingDetail = true
	l.nav.PushWithTitle(l.detailPage, "Value")
}

// popDetail returns to the tree.
func (l *screenLayout) popDetail() {
	if l == nil || l.mode != modeNarrow || !l.showingDetail {
		return
	}
	l.showingDetail = false
	l.nav.Back()
}

// narrowCanvas reports whether the canvas is too small for two panes.
func (w *MainWindow) narrowCanvas() bool {
	if w.mobile {
		// A phone stays narrow even in landscape: the split would still leave
		// each pane too small to read a key or a hex dump.
		return true
	}
	size := w.window.Canvas().Size()
	return size.Width > 0 && size.Width < narrowBreakpoint
}

// treePage builds the bucket list page: toolbar, search field and tree.
func (w *MainWindow) treePage() fyne.CanvasObject {
	return container.NewBorder(
		container.NewVBox(w.toolbar(), w.searchEntry),
		nil, nil, nil,
		container.NewScroll(w.treeWidget),
	)
}

// header is the row above the detail pane: file name, edit switch and theme.
func (w *MainWindow) header() fyne.CanvasObject {
	return container.NewVBox(
		container.NewBorder(nil, nil, nil, w.editToggle, w.fileLabel),
		widget.NewSeparator(),
	)
}

// toolbar builds the row of action buttons.
//
// The buttons are icon-only. Labelled buttons need roughly four times the width
// of an icon, so eight of them either crowd the pane or wrap onto three rows
// and eat the tree's height; the action is carried by the tooltip instead, and
// the button's accessibility label falls back to the icon name.
func (w *MainWindow) toolbar() fyne.CanvasObject {
	items := []struct {
		label   string
		icon    fyne.Resource
		onClick func()
	}{
		{"Open", theme.FolderOpenIcon(), w.openFile},
		{"Close", theme.CancelIcon(), w.closeFile},
		{"Sample", theme.ContentAddIcon(), w.createSample},
		{"Search", theme.SearchIcon(), w.toggleSearch},
		{"All keys", theme.ListIcon(), w.showAllEntries},
		{"Expand all", theme.MoveDownIcon(), w.expandAll},
		{"Collapse all", theme.MoveUpIcon(), w.collapseAll},
	}

	canvas := w.window.Canvas()
	buttons := make([]fyne.CanvasObject, 0, len(items)+1)
	for _, item := range items {
		btn := widget.NewButtonWithIcon("", item.icon, item.onClick)
		btn.Importance = widget.MediumImportance
		buttons = append(buttons, newTooltipped(canvas, btn, item.label))
	}

	// Help and the theme switch go in an overflow menu rather than taking two
	// more icon slots: eight 44pt buttons are wider than a phone, and a second
	// toolbar row costs more vertical space than the tree can spare. They are
	// the two actions a user reaches for rarely, so they are the right pair to
	// fold away.
	overflow := widget.NewButtonWithIcon("", theme.MoreHorizontalIcon(), w.showOverflowMenu)
	overflow.Importance = widget.MediumImportance
	buttons = append(buttons, newTooltipped(canvas, overflow, "More"))

	// A grid wrap keeps every button reachable regardless of width, which a
	// fixed column count does not: on a narrow pane a fixed count would squeeze
	// the icons to a few pixels.
	return container.NewGridWrap(toolButtonSize(), buttons...)
}

// showOverflowMenu opens the menu holding the less-used actions.
func (w *MainWindow) showOverflowMenu() {
	menu := fyne.NewMenu(
		"",
		fyne.NewMenuItem("Help", func() { w.showHelp() }),
		fyne.NewMenuItem(w.themeItemLabel(), func() { w.toggleTheme() }),
	)
	widget.ShowPopUpMenuAtRelativePosition(menu, w.window.Canvas(), fyne.NewPos(0, touchTarget), w.overflowAnchor())
}

// overflowAnchor returns the object the overflow menu is positioned against.
// The tree widget sits directly under the toolbar, so anchoring to it puts the
// menu at the bottom edge of the button row.
func (w *MainWindow) overflowAnchor() fyne.CanvasObject { return w.treeWidget }

// themeItemLabel names the theme switch by what it will switch to.
func (w *MainWindow) themeItemLabel() string {
	if w.dark {
		return "Light theme"
	}
	return "Dark theme"
}

// toolButtonSize is the footprint of one icon-only toolbar button. It is
// square, sized to the touch target, so eight of them fit across a phone without
// wrapping and each one stays comfortably tappable.
func toolButtonSize() fyne.Size {
	return fyne.NewSize(touchTarget, touchTarget)
}
