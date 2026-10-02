# rich_boltdb — BoltDB Viewer

A [BoltDB](https://github.com/etcd-io/bbolt) viewer, rewritten from the Flutter
build on the `v1` branch as a native [Fyne](https://fyne.io) desktop and mobile
application. The Go data layer is kept; the interface is not.

## Features

- Browse buckets as a tree, nested to any depth
- Flat table of every key in the file, with bucket, key, value and size
- Read values as Base64, Raw or Hex, and copy them out
- Truncated preview for large values, full value on export
- Search a key prefix across every bucket, at every depth
- Delete keys and buckets, behind an explicit edit switch
- Read-only by default: nothing on disk changes while browsing
- Dark theme
- Adapts to a phone screen, in portrait and landscape

## Requirements

Go 1.25 or later. A C toolchain is needed for the desktop GL driver; the Fyne
toolchain for mobile (see [Mobile](#mobile)).

## Running

```bash
go build -o boltdb_viewer .
./boltdb_viewer
```

Click **Sample** to generate a demonstration database in the system temp
directory. It is the quickest way to see nested buckets, keys containing `/`,
and an empty bucket, without having a file to hand.

## Using it

The toolbar is icon-only, so this is the reference for what each button does.

| Button       | Action                                                          |
|--------------|-----------------------------------------------------------------|
| Open         | Open a database file, read-only                                  |
| Close        | Close the current file and clear the view                        |
| Sample       | Write a demonstration database to the temp directory and open it |
| Search       | Show the search field; submitting runs a prefix search           |
| All keys     | Open the flat table of every key in the file                     |
| Expand all   | Open and expand every bucket in the file                         |
| Collapse all | Collapse everything                                              |
| More         | Overflow menu: Help, and the light/dark theme switch             |

In the bucket tree, buckets show a folder icon and keys a document icon whether
or not they have been expanded yet, and a bucket that holds nothing shows no
expander. Selecting a key reads it into the value pane; selecting a bucket shows
its path, depth and entry count instead.

The value pane shows the key, its size and bucket path, and the value in the
selected encoding. It is rendered as selectable text, so it can be copied.
Values over 1 MB are shown as a truncated preview with the withheld amount
stated; **Export** always writes the whole value.

**Allow edits** reopens the file for writing. Until it is on, Delete is refused
by the database itself rather than hidden, so the button is always there and
always tells you why it will not act. It deletes the selected key, or the
selected bucket along with everything inside it. Neither can be undone, so the
switch is off by default and worth leaving off while browsing.

## The flat table

**All keys** opens a grid with one row per key in the whole file, at any depth:

| Bucket               | Key          | Value                        | Size |
|----------------------|--------------|------------------------------|------|
| Users                | user:1       | {"id":1,"name":"User 1",...} | 48 B |
| Users/Profile/Fields | display_name | value of display_name        | 23 B |

The Bucket column is what makes a bare key readable: `display_name` is a
different thing three levels down than it would be at the top, so keys from
different buckets stay distinguishable. The column is dropped when every key in
the file shares one bucket, where it would only repeat the same name.

Values are read per visible row, up to 512 bytes each, rather than all at once.
That is what keeps the view usable on a file with a thousand large values: only
the rows on screen are ever read. The Value cell follows the encoding selected in
the detail pane, and a value cut off at the cell limit is marked with an ellipsis
rather than looking like the whole value. Selecting a row shows the full preview
in the detail pane.

Rows are paged 200 at a time as the scroll reaches them, keyed by absolute row
index so scrolling back over a page does not re-read anything.

## Mobile

Building for Android or iOS needs the Fyne toolchain, which bundles the Android
NDK and the Apple toolchain:

```bash
go install fyne.io/tools/cmd/fyne@latest
fyne package -os android
fyne package -os ios
```

Two layouts are built and the one in use is chosen from the canvas size:

- **Wide** (canvas at least 720pt): the bucket tree and the value pane side by
  side in a split.
- **Narrow** (phone, or a narrow window): one page at a time. The tree is the
  root page and selecting a key pushes the value pane, which carries its own
  header with the file name and the edit switch. The navigation bar's back arrow
  returns to the list.

A phone stays on the narrow layout even in landscape, because each half of the
split would be too small to read a key or a hex dump.

The toolbar is icon-only. Labelled buttons need roughly four times the width of
an icon, so seven of them plus an overflow menu still fit one row on a phone.
Each button is a square 44pt touch target, and its action appears as a tooltip
after half a second of hovering. There is no tooltip on a touch device, where
there is no pointer to hover with, so [Using it](#using-it) spells the buttons
out. Help and the theme switch live in the overflow menu, since they are the two
actions a user reaches for rarely.

## Limits

Deliberate caps, so a large or malformed file cannot hang the window. None of
them are reachable in normal use; when one is hit the status bar says so.

| Limit                | Value  | Where                                     |
|----------------------|--------|-------------------------------------------|
| Preview size         | 1 MB   | Value pane, before the full value is read |
| Table cell size      | 512 B  | One cell of the flat table                |
| Bucket listing       | 2000   | Entries shown for one bucket              |
| Search results       | 500    | Hits collected by a prefix search         |
| Table page           | 200    | Rows read per scroll                      |
| Bucket nesting depth | 64     | Recursion bound                           |
| Counted entries      | 200000 | Scrollbar count for a very large file     |

## Layout

```
main.go                 entry point
internal/store/         bbolt access layer, no UI imports
  path.go               bucket paths, resolved segment by segment
  store.go              open, list, read, search, delete
  entries.go            flat listing across all buckets, paging
  sample.go             demonstration database
  store_test.go
  entries_test.go
internal/ui/            Fyne widgets
  window.go             window wiring, file handling, search, edit mode
  layout.go             the wide and narrow arrangements, toolbar
  tree.go               lazy bucket tree model
  value.go              value pane: encodings, export, delete
  table.go              the flat all-keys grid
  dialog.go             dialog sizing shared by help, search and the table
  tooltip.go            hover tooltips for the icon-only toolbar
  *_test.go
```

## Tests

```bash
go test ./...
go test -race ./...
```

The store tests cover three levels of nesting, empty buckets, names containing
the path separator, paging, and reads against a closed database. The UI tests
drive the real widgets through Fyne's test driver, including the phone layouts
at 390×844 and 844×390, the toolbar at a phone's width, and the bucket tree
three levels deep.

## Design notes

**Nested buckets.** v1's data layer looked buckets up by joining the path with
`/` and handing the result to `tx.Bucket`, which only resolves a single
top-level name, so everything below the first level came back as "not found".
Paths are now segments, resolved one bucket at a time. Keeping the segments
separate also makes a key that literally contains `/` unambiguous, which joining
them would not.

**One goroutine owns the model.** The UI keeps database reads on worker
goroutines and applies the results with `fyne.Do`, so the tree needs no locks.
v1 guarded its model with a `sync.RWMutex` whose recursive index removal locked
against itself and deadlocked.

**The store does hold one lock**, on the bbolt handle: `Close` waits for a
transaction in flight rather than nulling the handle out from under it. After
that, every accessor returns `ErrClosed` rather than faulting.

**Values are read late.** A key's value is read when it is selected, not when
its bucket is expanded. v1 base64-encoded every value up front, capped at 100
entries. The flat table and the value pane each read only what they are about to
draw.

**Expander state comes from the listing.** A bucket listing carries a `HasKids`
flag, read in the same transaction as the entries. Fyne reports a tree row as a
branch when it has an expander, so without that flag an unopened bucket had no
answer and was drawn with the key icon until clicked. Each row's icon now comes
from the node's own kind, independent of the expander flag.

**Dialogs are told their size.** A dialog sizes itself from its content's
minimum, and the content of a scrolling list or a wrapped text area is one line
tall. Help and the search results both opened as a box a few words high until
they were given a real size.

**Layouts give the spare space to one object.** A border layout hands its fixed
slots exactly their minimum, and a truncating label reports a very small
minimum. Two things depend on being the centre object instead: the tree row's
name, which was left with a few pixels and rendered as a bare ellipsis, and the
value area, which stayed one line tall however much room the pane had.

**Plugins are not part of this rewrite.** v1 exposed the data layer as a Flutter
plugin over JSON-RPC; that interface is not reproduced.
