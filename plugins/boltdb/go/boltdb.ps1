 
$now=Get-Date -Format "yyyy-MM-dd"
#(arm, arm64, 386, amd64)

#$Env:GO111MODULE = "off"

gomobile bind -o rich_boltdb_$now.aar -ldflags "-s -w " -target="android/arm,android/arm64,android/386,android/amd64" github.com/go-flutter-desktop/plugins/boltdb/api/boltdb
cp rich_boltdb_${now}.aar ../android/boltdb/boltdb.aar




