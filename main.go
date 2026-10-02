// Command boltdb_viewer is a desktop viewer for BoltDB (bbolt) files.
//
// It replaces the Flutter build from the v1 branch: the Go data layer is kept
// (and extended to nested buckets), while the interface is a native Fyne
// desktop application.
package main

import (
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/theme"

	"boltdb_viewer/internal/ui"
)

func main() {
	a := app.NewWithID("tech.zc310.boltdbviewer")
	a.Settings().SetTheme(theme.LightTheme())

	ui.New(a).Run()
}
