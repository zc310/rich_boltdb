package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"boltdb_viewer/internal/store"
)

// dialogMargin is the fraction of the canvas left showing around a dialog, so
// the window underneath stays recognisable and the dialog is obviously
// dismissible.
const dialogMargin = 0.9

// fitDialogSize returns the size to ask a dialog for: the preferred size,
// reduced to fit the canvas.
//
// Every dialog here needs an explicit size. A dialog sizes itself from its
// content's minimum, and the content of a scrolling list or a wrapped text area
// is one line tall, so left alone the help dialog and the search results both
// opened as a box a few words high.
func fitDialogSize(preferred, canvas fyne.Size) fyne.Size {
	if canvas.Width <= 0 || canvas.Height <= 0 {
		return preferred
	}
	return fyne.NewSize(
		min(preferred.Width, canvas.Width*dialogMargin),
		min(preferred.Height, canvas.Height*dialogMargin),
	)
}

// showDialog opens a custom dialog at a size that fits the canvas.
func (w *MainWindow) showDialog(title, dismiss string, content fyne.CanvasObject, preferred fyne.Size) *dialog.CustomDialog {
	dlg := dialog.NewCustom(title, dismiss, content, w.window)
	dlg.Resize(fitDialogSize(preferred, w.window.Canvas().Size()))
	dlg.Show()
	return dlg
}

// showHitList opens a dialog listing the hits, each labelled with its full
// bucket path so hits from different depths stay distinguishable.
func (w *MainWindow) showHitList(hits []store.Hit) {
	list := widget.NewList(
		func() int { return len(hits) },
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
		},
		func(i int, obj fyne.CanvasObject) {
			obj.(*widget.Label).SetText(hits[i].Label())
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		w.value.showEntry(store.Entry{
			Bucket: hits[id].Bucket,
			Key:    hits[id].Key,
			Size:   hits[id].Size,
		})
	}

	w.showDialog("Search results", "Close", list, fyne.NewSize(560, 420))
}
