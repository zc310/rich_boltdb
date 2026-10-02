package ui

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"boltdb_viewer/internal/store"
)

// viewMode selects how a value is rendered.
type viewMode int

const (
	viewBase64 viewMode = iota
	viewRaw
	viewHex
)

func (m viewMode) label() string {
	switch m {
	case viewRaw:
		return "Raw"
	case viewHex:
		return "Hex"
	default:
		return "Base64"
	}
}

// valuePane shows the selected key: its metadata, the value in a switchable
// encoding, and the actions that apply to it.
type valuePane struct {
	win *MainWindow

	container *fyne.Container
	keyLabel  *widget.Label
	metaLabel *widget.Label
	text      *widget.RichText

	exportBtn *widget.Button
	deleteBtn *widget.Button

	// node is the selected tree key, nil when the pane is showing a bucket or
	// a search hit.
	node *node
	// entry is the selected search result or flat-table row, nil when the
	// selection came from the tree. Exactly one of node and entry is set for a
	// key selection.
	entry   *store.Entry
	data    []byte
	partial bool
	mode    viewMode
	modes   map[viewMode]*widget.Button
}

func newValuePane(win *MainWindow) *valuePane {
	p := &valuePane{win: win, container: container.NewVBox()}
	p.build()
	p.reset()
	return p
}

func (p *valuePane) build() {
	p.keyLabel = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.metaLabel = widget.NewLabel("")
	p.metaLabel.TextStyle = fyne.TextStyle{Italic: true}

	// A key and a bucket path are arbitrary length. Without truncation their
	// labels report their full width as a minimum, which would push the pane
	// wider than the window and squeeze the value area instead.
	p.keyLabel.Truncation = fyne.TextTruncateEllipsis
	p.metaLabel.Truncation = fyne.TextTruncateEllipsis

	// RichText rather than a disabled Entry: a disabled Entry cannot be
	// selected, so the value could not be copied out of the window.
	p.text = widget.NewRichText(&widget.TextSegment{
		Text:  "",
		Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Monospace: true}},
	})

	p.modes = make(map[viewMode]*widget.Button)
	seg := container.NewHBox(
		p.modeButton(viewBase64),
		p.modeButton(viewRaw),
		p.modeButton(viewHex),
	)

	p.exportBtn = widget.NewButtonWithIcon("Export", theme.DownloadIcon(), p.exportValue)
	p.deleteBtn = widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), p.deleteKey)
	p.deleteBtn.Importance = widget.DangerImportance

	// A border layout, not a VBox: a VBox gives every child its minimum height,
	// and the value area's minimum is the one line of text a RichText reports.
	// The scroll was stuck at 32px however tall the pane was. As the centre
	// object it receives all the height left over from the labels and buttons.
	p.container = container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("Value", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			widget.NewSeparator(),
			p.keyLabel,
			p.metaLabel,
		),
		container.NewVBox(
			container.NewBorder(nil, nil, nil, p.deleteBtn, seg),
			p.exportBtn,
		),
		nil, nil,
		container.NewScroll(p.text),
	)
}

func (p *valuePane) modeButton(m viewMode) *widget.Button {
	btn := widget.NewButton(m.label(), func() { p.setMode(m) })
	p.modes[m] = btn
	return btn
}

// reset returns the pane to its empty state.
func (p *valuePane) reset() {
	p.node = nil
	p.entry = nil
	p.data = nil
	p.partial = false

	p.keyLabel.SetText("No selection")
	p.metaLabel.SetText("Select a key in the tree to inspect its value.")
	setRichText(p.text, "")
	p.exportBtn.Disable()
	p.deleteBtn.Disable()
	p.highlightModes()
	p.container.Refresh()
}

// showBucket renders the summary shown when a bucket, not a key, is selected.
func (p *valuePane) showBucket(n *node) {
	p.node = nil
	p.entry = nil
	p.data = nil
	p.partial = false

	rows := []string{
		fmt.Sprintf("Bucket: %s", n.pathString()),
		fmt.Sprintf("Depth: %d level(s)", n.path.Depth()),
	}
	if n.truncated {
		rows = append(rows, fmt.Sprintf("Entries: more than %d (listing capped)", len(n.children)))
	} else if n.loaded {
		rows = append(rows, fmt.Sprintf("Entries: %d", len(n.children)))
	} else {
		rows = append(rows, "Entries: not loaded yet")
	}

	p.keyLabel.SetText(n.label())
	p.metaLabel.SetText(strings.Join(rows, "   "))
	setRichText(p.text, "")
	p.exportBtn.Disable()
	p.deleteBtn.Enable()
	p.deleteBtn.SetText("Delete Bucket")
	p.highlightModes()
	p.container.Refresh()
}

// showKey reads a value and renders it. The read happens on a worker goroutine
// so a large value cannot stall the window.
func (p *valuePane) showKey(n *node) {
	st := p.win.tree.st
	if st == nil || n.parent == nil {
		return
	}
	parent := n.parent.path
	key := append([]byte(nil), n.name...)

	if p.win.loadInline {
		p.readValue(n, parent, key)
		return
	}

	p.win.busy(true)
	go func() {
		// Only the preview is read; the full value stays on disk until the
		// user exports it.
		data, partial, err := st.Value(parent, key, maxPreviewBytes)

		fyne.Do(func() {
			p.win.busy(false)
			if err != nil {
				p.win.fail(fmt.Errorf("read %s: %w", n.label(), err))
				return
			}
			p.applyValue(n, data, partial)
		})
	}()
}

// readValue reads on the calling goroutine, used when the window is in inline
// mode so no read outlives the caller.
func (p *valuePane) readValue(n *node, parent store.Path, key []byte) {
	data, partial, err := p.win.tree.st.Value(parent, key, maxPreviewBytes)
	if err != nil {
		p.win.fail(fmt.Errorf("read %s: %w", n.label(), err))
		return
	}
	p.applyValue(n, data, partial)
}

// applyValue installs a value that has already been read.
func (p *valuePane) applyValue(n *node, data []byte, partial bool) {
	p.node = n
	p.entry = nil
	p.data = data
	p.partial = partial

	p.keyLabel.SetText(n.label())
	p.metaLabel.SetText(p.metaText(n, len(data), partial))
	p.exportBtn.Enable()
	p.deleteBtn.Enable()
	p.deleteBtn.SetText("Delete Key")
	p.render()
	p.container.Refresh()
}

// showEntry renders a key that was located outside the tree, by a search or by
// the flat table. The entry carries its own bucket path, so the value is read
// directly rather than resolved through the tree, which may not have the
// containing bucket loaded.
func (p *valuePane) showEntry(entry store.Entry) {
	st := p.win.tree.st
	if st == nil {
		return
	}

	key := append([]byte(nil), entry.Key...)
	size := entry.Size

	if p.win.loadInline {
		data, partial, err := st.Value(entry.Bucket, key, maxPreviewBytes)
		if err != nil {
			p.win.fail(fmt.Errorf("read %s: %w", key, err))
			return
		}
		p.applyEntry(entry, size, data, partial)
		return
	}

	p.win.busy(true)
	go func() {
		data, partial, err := st.Value(entry.Bucket, key, maxPreviewBytes)

		fyne.Do(func() {
			p.win.busy(false)
			if err != nil {
				p.win.fail(fmt.Errorf("read %s: %w", key, err))
				return
			}
			p.applyEntry(entry, size, data, partial)
		})
	}()
}

// applyEntry renders an entry that has already been read.
func (p *valuePane) applyEntry(entry store.Entry, size int64, data []byte, partial bool) {
	// The entry is not a tree node, so the pane tracks it separately and the
	// node stays nil: there is nothing in the tree to delete or reveal.
	p.node = nil
	p.entry = &entry
	p.data = data
	p.partial = partial

	p.keyLabel.SetText(store.Display(entry.Key))
	p.metaLabel.SetText(p.entryMetaText(entry, size, len(data), partial))
	p.exportBtn.Enable()
	p.deleteBtn.Enable()
	p.deleteBtn.SetText("Delete Key")
	p.render()
	p.container.Refresh()
}

func (p *valuePane) entryMetaText(entry store.Entry, size int64, shown int, partial bool) string {
	text := fmt.Sprintf("Size: %s   Path: %s", formatSize(size), entry.Bucket)
	if partial {
		text += fmt.Sprintf("   (showing first %s)", formatSize(int64(shown)))
	}
	return text
}

func (p *valuePane) metaText(n *node, shown int, partial bool) string {
	size := fmt.Sprintf("Size: %s", formatSize(n.size))
	location := fmt.Sprintf("Path: %s", n.parent.pathString())

	switch {
	case partial:
		// Say what is being withheld, rather than letting a reader assume
		// the truncated text is the whole value.
		size += fmt.Sprintf(" (showing first %s)", formatSize(int64(shown)))
	case n.size == 0:
		size += " (empty)"
	}
	return size + "   " + location
}

// setMode switches encoding and repaints.
func (p *valuePane) setMode(m viewMode) {
	p.mode = m
	p.render()
	p.highlightModes()
	p.notifyModeChanged()
}

// modeChanged is called after the encoding changes, so views that cache
// rendered values can drop them.
var modeChangedHooks []func()

// onModeChanged registers a callback for encoding changes.
func onModeChanged(fn func()) { modeChangedHooks = append(modeChangedHooks, fn) }

// notifyModeChanged tells the registered views.
func (p *valuePane) notifyModeChanged() {
	for _, fn := range modeChangedHooks {
		fn()
	}
}

func (p *valuePane) highlightModes() {
	for m, btn := range p.modes {
		if m == p.mode {
			btn.Importance = widget.HighImportance
		} else {
			btn.Importance = widget.MediumImportance
		}
		btn.Refresh()
	}
}

// render writes the current mode's encoding of p.data into the text area.
// Every mode renders from the same buffered bytes, so switching modes cannot
// bypass the preview limit the way a per-mode read would.
func (p *valuePane) render() {
	if p.node == nil && p.entry == nil {
		setRichText(p.text, "")
		return
	}
	setRichText(p.text, encode(p.data, p.mode))
}

// encode renders data in the requested form.
func encode(data []byte, mode viewMode) string {
	switch mode {
	case viewRaw:
		if isPrintable(data) {
			return string(data)
		}
		return "[not printable text - switch to Hex or Base64]"
	case viewHex:
		if len(data) == 0 {
			return ""
		}
		return hex.Dump(data)
	default:
		if len(data) == 0 {
			return ""
		}
		return base64.StdEncoding.EncodeToString(data)
	}
}

// isPrintable reports whether data can be shown as text.
func isPrintable(data []byte) bool {
	for _, b := range data {
		if b == '\n' || b == '\t' || b == '\r' {
			continue
		}
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}

func setRichText(t *widget.RichText, s string) {
	t.Segments = []widget.RichTextSegment{
		&widget.TextSegment{Text: s, Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Monospace: true}}},
	}
	t.Refresh()
}

// exportValue writes the whole value, not just the preview, to a chosen file.
func (p *valuePane) exportValue() {
	parent, key, label, ok := p.target()
	if !ok || p.win.tree.st == nil {
		return
	}

	st := p.win.tree.st
	name := sanitizeFilename(label)

	save := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			p.win.fail(err)
			return
		}
		if w == nil {
			return
		}
		defer w.Close()

		write := func(data []byte) {
			if _, err := w.Write(data); err != nil {
				p.win.fail(fmt.Errorf("write %s: %w", w.URI().Name(), err))
				return
			}
			p.win.status("Exported %s (%s)", w.URI().Name(), formatSize(int64(len(data))))
		}

		if p.win.loadInline {
			data, _, err := st.Value(parent, key, 0)
			if err != nil {
				p.win.fail(fmt.Errorf("read %s: %w", key, err))
				return
			}
			write(data)
			return
		}

		// Read the full value here, off the main goroutine.
		go func() {
			data, _, err := st.Value(parent, key, 0)

			fyne.Do(func() {
				if err != nil {
					p.win.fail(fmt.Errorf("read %s: %w", key, err))
					return
				}
				write(data)
			})
		}()
	}, p.win.window)

	save.SetTitleText("Export value")
	save.SetConfirmText("Export")
	save.SetFileName(name)
	save.SetFilter(storage.NewExtensionFileFilter([]string{".bin", ".txt", ".json", ".log"}))
	save.Show()
}

// deleteKey removes the selected key. Deleting is only offered on a database
// opened for writing, which the toolbar switch controls.
func (p *valuePane) deleteKey() {
	parent, key, label, ok := p.target()
	if !ok {
		return
	}

	st := p.win.tree.st

	confirmed := true
	remove := func() {
		if !confirmed {
			return
		}

		if p.win.loadInline {
			if err := st.DeleteKey(parent, key); err != nil {
				p.win.fail(fmt.Errorf("delete %s: %w", label, err))
				return
			}
			p.afterDelete(parent, label)
			return
		}

		p.win.busy(true)
		go func() {
			err := st.DeleteKey(parent, key)

			fyne.Do(func() {
				p.win.busy(false)
				if err != nil {
					p.win.fail(fmt.Errorf("delete %s: %w", label, err))
					return
				}
				p.afterDelete(parent, label)
			})
		}()
	}

	if p.win.loadInline {
		remove()
		return
	}
	dialog.ShowConfirm("Delete key",
		fmt.Sprintf("Delete %s from %s?\n\nThis cannot be undone.", label, parent),
		func(ok bool) { confirmed = ok; remove() }, p.win.window)
}

// afterDelete updates the tree and clears the pane once a key is gone. parent
// and label describe what was removed, for the status line.
func (p *valuePane) afterDelete(parent store.Path, label string) {
	if p.node != nil {
		p.win.dropNodes([]*node{p.node})
	}
	p.reset()
	p.win.refreshTree()
	p.win.status("Deleted key %s from %s", label, parent)
}

// target resolves the key the pane is acting on, from either a tree selection
// or a search hit.
func (p *valuePane) target() (parent store.Path, key []byte, label string, ok bool) {
	switch {
	case p.node != nil && p.node.parent != nil:
		return p.node.parent.path, append([]byte(nil), p.node.name...), p.node.label(), true
	case p.entry != nil:
		return p.entry.Bucket, append([]byte(nil), p.entry.Key...), store.Display(p.entry.Key), true
	default:
		return nil, nil, "", false
	}
}

// sanitizeFilename turns a key into something safe to offer as a save name.
func sanitizeFilename(key string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", "\x00", "_")
	name := replacer.Replace(key)
	if name == "" {
		return "value.bin"
	}
	return name
}

// formatSize renders a byte count the way v1 did, with a binary unit suffix.
func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return strconv.FormatInt(size, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}
