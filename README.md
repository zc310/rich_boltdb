# BoltDB Viewer

A [BoltDB](https://github.com/etcd-io/bbolt) Viewer Flutter project.

## Features

- Display the contents of the boltdb file.
- Support Windows、Linux、Android.

## Screenshot

### Linux

![BoltDB Viewer - linux](https://raw.githubusercontent.com/zc310/rich_boltdb/master/assets/linux.png)

### Windows

![BoltDB Viewer - windows](https://raw.githubusercontent.com/zc310/rich_boltdb/master/assets/windows.png)

## Getting Started

### Install dependencies

You'll need to have the following tools installed on your machine.

- [flutter](https://flutter.dev)
- [go](https://golang.org)
- [hover](https://github.com/go-flutter-desktop/hover)

### Run on Linux or Windows

```bash
git clone https://github.com/zc310/rich_boltdb.git
cd rich_boltdb
flutter pub get
hover run
```

### Build standalone application

```bash
hover build linux # or darwin or windows
```

### Build android apk

```bash
flutter create .
# build boltdb aar
plugins/boltdb/go/boltdb.ps1
flutter build apk
```
