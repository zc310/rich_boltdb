package boltdb_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/go-flutter-desktop/plugins/boltdb/api/boltdb"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fastjson"
)

//var db = filepath.Join(os.TempDir(), "~bolt_001.db")
var db = filepath.Join(os.TempDir(), "access.log")

func execute(a string) string {
	var fj fastjson.Parser
	v, err := fj.Parse(a)
	if err != nil {
		return "err"
	}
	var aa fastjson.Arena
	p := v.GetObject("params")
	p.Set("file", aa.NewString(db))

	return BoltDB(v.String())
}
func TestApi_BoltDB(t *testing.T) {
	var s string

	s = execute(`{ "jsonrpc": "2.0","method":"bucket.create","params":{"bucket":"A001"},"id": {    "a": "A"  }}`)
	assert.Equal(t, s, `{"jsonrpc":"2.0","id":{"a":"A"},"result":null}`)

	s = execute(`{"method":"bucket.create","params":{"bucket":"A002"}}`)
	assert.Equal(t, s, `{"result":null}`)

	s = execute(`{"method":"bucket.create","params":{"bucket":"A003"}}`)
	assert.Equal(t, s, `{"result":null}`)

	s = execute(`{"method":"bucket.list","params":{}}`)
	assert.Equal(t, s, `{"result":["A001","A002","A003"]}`)

	s = execute(`{"method":"bucket.delete","params":{"bucket":"A001"}}`)
	assert.Equal(t, s, `{"result":null}`)

	s = execute(`{"method":"bucket.list","params":{}}`)
	assert.Equal(t, s, `{"result":["A002","A003"]}`)

	s = execute(`{"method":"key.put","params":{"bucket":"A003","key":"001","value":"0123456"}}`)
	assert.Equal(t, s, `{"result":null}`)

	s = execute(`{"method":"key.get","params":{"bucket":"A003","key":"001"}}`)
	assert.Equal(t, s, `{"result":"0123456"}`)

	s = execute(`{"method":"key.delete","params":{"bucket":"A003","key":"001"}}`)
	assert.Equal(t, s, `{"result":null}`)

	execute(`{"method":"key.put","params":{"bucket":"A003","key":"001","value":"0123456"}}`)
	execute(`{"method":"key.put","params":{"bucket":"A003","key":"002","value":"0123456"}}`)
	execute(`{"method":"key.put","params":{"bucket":"A003","key":"003","value":"0123456"}}`)

	s = execute(`{"method":"key.scan","params":{"bucket":"A003","prefix":"00"}}`)
	assert.Equal(t, s, `{"result":[{"k":"001","v":"MDEyMzQ1Ng==","s":3},{"k":"002","v":"MDEyMzQ1Ng==","s":3},{"k":"003","v":"MDEyMzQ1Ng==","s":3}]}`)

	s = execute(`{"method":"key.scan","params":{"bucket":"A003","prefix":""}}`)
	assert.Equal(t, s, `{"result":[{"k":"001","v":"MDEyMzQ1Ng==","s":3},{"k":"002","v":"MDEyMzQ1Ng==","s":3},{"k":"003","v":"MDEyMzQ1Ng==","s":3}]}`)

	s = execute(`{"method":"db.close","params":{}}`)
	assert.Equal(t, s, `{"result":null}`)

	assert.Equal(t, nil, os.Remove(db))
}
