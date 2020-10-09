package boltdb

import (
	"github.com/valyala/fastjson"
	bolt "go.etcd.io/bbolt"
	"sync"
)

var (
	db   map[string]*api
	lock sync.Mutex
)

func init() {
	db = make(map[string]*api)
}
func BoltDB(params string) string {
	var p *api
	var ok bool

	var fj fastjson.Parser
	a, err := fj.Parse(params)
	if err != nil {
		return newResult(a, newError(err), nil)
	}

	method := string(a.GetStringBytes("method"))
	s := a.Get("params")
	path := string(s.GetStringBytes("file"))
	if path == "" {
		return newResult(a, newError(bolt.ErrInvalid), nil)
	}

	lock.Lock()
	p, ok = db[path]
	if !ok || p.db == nil {
		p = &api{}
		err = p.open(path)
		if err != nil {
			lock.Unlock()
			return newResult(a, newError(err), nil)
		}
		db[path] = p
	}
	lock.Unlock()

	var o *fastjson.Value
	o, err = p.Execute(method, s)
	if err != nil {
		return newResult(a, newError(err), o)
	}

	return newResult(a, nil, o)
}
func Close() {
	for k, v := range db {
		_, _ = v.close(nil)
		delete(db, k)
	}

}
