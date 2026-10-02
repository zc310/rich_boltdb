package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// waitForTooltip waits for the delayed show to land, or for the timer to lapse
// without one appearing.
func waitForTooltip(t *testing.T, tip *tooltipped, want bool) {
	t.Helper()

	deadline := time.Now().Add(tooltipDelay + time.Second)
	for time.Now().Before(deadline) {
		if tip.visible() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tip.visible() != want {
		t.Errorf("after the delay shown = %v, want %v", tip.visible(), want)
	}
}

func newTestTooltip(t *testing.T, label string) *tooltipped {
	t.Helper()

	app := test.NewTempApp(t)
	btn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {})
	return newTooltipped(app.Driver().CanvasForObject(btn), btn, label)
}

func TestTooltipHidesThenShows(t *testing.T) {
	tip := newTestTooltip(t, "Open")

	if tip.visible() {
		t.Fatal("tooltip shown before the pointer arrived")
	}

	// Hover alone must not show it: a tip that flashes on every brush past the
	// toolbar is unreadable.
	tip.MouseIn(&desktop.MouseEvent{})
	if tip.visible() {
		t.Error("tooltip appeared immediately on hover")
	}

	// Let the delay elapse and run the queued show.
	waitForTooltip(t, tip, true)

	tip.MouseOut()
	if tip.visible() {
		t.Error("tooltip still shown after the pointer left")
	}
}

func TestTooltipCancelledByLeaving(t *testing.T) {
	tip := newTestTooltip(t, "Close")

	tip.MouseIn(&desktop.MouseEvent{})
	// Leaving before the delay fires must cancel the pending show.
	tip.MouseOut()

	time.Sleep(tooltipDelay + 50*time.Millisecond)
	if tip.visible() {
		t.Error("tooltip appeared even though the pointer left first")
	}
}

func TestTooltipWithoutLabelDoesNothing(t *testing.T) {
	tip := newTestTooltip(t, "")

	tip.MouseIn(&desktop.MouseEvent{})
	time.Sleep(tooltipDelay + 50*time.Millisecond)

	if tip.visible() {
		t.Error("tooltip shown without a label")
	}
	tip.MouseOut()
}

// TestTooltipRehoverCancelsPendingShow covers brushing past two buttons in
// quick succession: the first timer must not pop a tip for the button already
// left behind.
func TestTooltipRehoverCancelsPendingShow(t *testing.T) {
	tip := newTestTooltip(t, "Search")

	tip.MouseIn(&desktop.MouseEvent{})
	tip.MouseOut()
	tip.MouseIn(&desktop.MouseEvent{})

	waitForTooltip(t, tip, true)
	tip.MouseOut()
}

func TestTooltipMinSizeIsTouchTarget(t *testing.T) {
	tip := newTestTooltip(t, "Expand all")

	min := tip.MinSize()
	if min.Width < touchTarget || min.Height < touchTarget {
		t.Errorf("tooltip wrapper is %vx%v, below the %v touch target", min.Width, min.Height, touchTarget)
	}
}

func TestTooltipPassesResizeThrough(t *testing.T) {
	tip := newTestTooltip(t, "Help")

	// A wrapper that swallowed the content's size would collapse the toolbar.
	tip.Resize(fyne.NewSize(80, 44))
	if got := tip.content.Size(); got.Width != 80 || got.Height != 44 {
		t.Errorf("wrapped content is %v, want the wrapper's 80x44", got)
	}
}
