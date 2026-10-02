package ui

import (
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// tooltipDelay is how long the pointer must rest before the tip appears. A
// tooltip that flashes up on every brush across the toolbar is noise; this is
// long enough to read a couple of words.
const tooltipDelay = 500 * time.Millisecond

// tooltipped wraps a control with a hover tooltip.
//
// The toolbar buttons are icon-only so they do not crowd each other, which
// leaves the action unlabelled on screen. The tooltip restores that, and the
// label is kept in the button's Text field only when there is no text to draw,
// so a screen reader still announces the action.
//
// Hovering only happens with a pointer, so on a phone there is no tooltip and
// nothing is lost: the icons are unambiguous enough at that size.
type tooltipped struct {
	widget.BaseWidget

	content fyne.CanvasObject
	label   string
	canvas  fyne.Canvas

	// mu guards the popup and the shown flag. The timer callback and the hover
	// callbacks can both be in flight at once: a tip that is scheduled to appear
	// while the pointer leaves is the ordinary case, not an edge case.
	mu    sync.Mutex
	popup *widget.PopUp
	timer *time.Timer
	shown bool
}

// visible reports whether the tooltip is currently displayed.
func (t *tooltipped) visible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.shown
}

var _ fyne.Widget = (*tooltipped)(nil)
var _ desktop.Hoverable = (*tooltipped)(nil)

// newTooltipped wraps content so that hovering it shows label. The canvas is
// where the popup is added; a nil canvas disables the tooltip, which is what
// happens in tests that never create a window.
func newTooltipped(canvas fyne.Canvas, content fyne.CanvasObject, label string) *tooltipped {
	t := &tooltipped{content: content, label: label, canvas: canvas}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tooltipped) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

// MinSize passes the wrapped control's size through, but never below a square
// touch target. An icon-only button's own minimum is only as wide as its icon,
// which is a little under the target and too small to hit reliably.
func (t *tooltipped) MinSize() fyne.Size {
	size := t.BaseWidget.MinSize()
	if size.Width < touchTarget {
		size.Width = touchTarget
	}
	if size.Height < touchTarget {
		size.Height = touchTarget
	}
	return size
}

func (t *tooltipped) MouseIn(*desktop.MouseEvent) {
	if t.label == "" || t.canvas == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(tooltipDelay, t.showOnMainThread)
}

// MouseMoved keeps the tip anchored to the control as it is resized or the
// window moves; a tooltip that stays at its original spot would drift away from
// its button.
func (t *tooltipped) MouseMoved(*desktop.MouseEvent) {
	t.mu.Lock()
	popup, shown := t.popup, t.shown
	t.mu.Unlock()

	if !shown || popup == nil {
		return
	}
	fyne.Do(func() {
		popup.ShowAtRelativePosition(
			fyne.NewPos(0, t.Size().Height+theme.Padding()/2),
			t,
		)
	})
}

func (t *tooltipped) MouseOut() {
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	shown := t.shown
	t.mu.Unlock()

	if shown {
		fyne.Do(t.hide)
	}
}

// showOnMainThread runs when the hover delay has elapsed. The popup is a
// widget, so the show has to be marshalled onto the Fyne thread.
func (t *tooltipped) showOnMainThread() {
	fyne.Do(t.show)
}

func (t *tooltipped) show() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.shown || t.canvas == nil {
		return
	}

	label := widget.NewLabel(t.label)
	label.Importance = widget.HighImportance

	t.popup = widget.NewPopUp(container.NewPadded(label), t.canvas)
	// Below the control: a tip above would land on whatever the pointer just
	// left.
	t.popup.ShowAtRelativePosition(
		fyne.NewPos(0, t.Size().Height+theme.Padding()/2),
		t,
	)
	t.shown = true
}

func (t *tooltipped) hide() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.popup != nil {
		t.popup.Hide()
		t.popup = nil
	}
	t.shown = false
}

// Refresh propagates to the wrapped control.
func (t *tooltipped) Refresh() {
	t.BaseWidget.Refresh()
	t.content.Refresh()
}
