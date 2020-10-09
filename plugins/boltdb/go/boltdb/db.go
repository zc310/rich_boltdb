package boltdb

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"

	"errors"
	"fmt"
	"github.com/valyala/fastjson"
	bolt "go.etcd.io/bbolt"
	"time"
)

type api struct {
	aa     fastjson.Arena
	db     *bolt.DB
	action map[string]func(v *fastjson.Value) (*fastjson.Value, error)
}

func (p *api) buckets(_ *fastjson.Value) (*fastjson.Value, error) {
	a := p.aa.NewArray()

	n := 0
	err := p.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			a.SetArrayItem(n, p.aa.NewStringBytes(name))
			n++
			return nil
		})
	})
	return a, err
}

func (p *api) createBucket(v *fastjson.Value) (*fastjson.Value, error) {
	bucket := v.GetStringBytes("bucket")
	if len(bucket) == 0 {
		return nil, errors.New("no bucket name")
	}
	var err error

	err = p.db.Update(func(tx *bolt.Tx) error {
		_, err = tx.CreateBucketIfNotExists(bucket)

		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return nil, nil

}

func (p *api) deleteBucket(v *fastjson.Value) (*fastjson.Value, error) {
	bucket := v.GetStringBytes("bucket")
	if len(bucket) == 0 {
		return nil, errors.New("no bucket name")
	}
	var err error
	err = p.db.Update(func(tx *bolt.Tx) error {
		err := tx.DeleteBucket(bucket)

		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		return nil
	})

	return nil, err
}

func (p *api) deleteKey(v *fastjson.Value) (*fastjson.Value, error) {
	bucket := v.GetStringBytes("bucket")
	key := v.GetStringBytes("key")

	if len(bucket) == 0 || len(key) == 0 {
		return nil, errors.New("no bucket name or key")
	}
	var err error
	err = p.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucket)

		if err != nil {

			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Delete(key)

		if err != nil {

			return fmt.Errorf("delete kv: %s", err)
		}

		return nil
	})

	return nil, err
}

func (p *api) put(v *fastjson.Value) (*fastjson.Value, error) {
	bucket := v.GetStringBytes("bucket")
	key := v.GetStringBytes("key")
	value := v.GetStringBytes("value")
	if len(bucket) == 0 || len(key) == 0 {
		return nil, errors.New("no bucket name or key")
	}
	var err error
	err = p.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucket)

		if err != nil {

			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put(key, value)

		if err != nil {

			return fmt.Errorf("create kv: %s", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return nil, nil

}

func (p *api) get(v *fastjson.Value) (*fastjson.Value, error) {
	bucket := v.GetStringBytes("bucket")
	key := v.GetStringBytes("key")

	if len(bucket) == 0 || len(key) == 0 {
		return nil, errors.New("no bucket name or key")
	}
	var o *fastjson.Value

	err := p.db.View(func(tx *bolt.Tx) error {

		b := tx.Bucket(bucket)

		if b != nil {

			v := b.Get(key)

			o = p.aa.NewStringBytes(v)

		} else {
			return bolt.ErrBucketNotFound

		}
		return nil

	})

	return o, err

}

func (p *api) prefixScan(v *fastjson.Value) (*fastjson.Value, error) {
	bucket := v.GetStringBytes("bucket")
	prefix := v.GetStringBytes("prefix")

	if len(bucket) == 0 {
		return nil, errors.New("no bucket name or prefix")
	}
	o := p.aa.NewArray()

	var err error
	var n int
	if len(prefix) == 0 {

		err = p.db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucket)
			if b != nil {
				c := b.Cursor()

				for k, v := c.First(); k != nil; k, v = c.Next() {
					t := p.aa.NewObject()
					t.Set("k", p.aa.NewStringBytes(k))
					t.Set("v", p.aa.NewString(base64.StdEncoding.EncodeToString(v)))
					t.Set("s", p.aa.NewNumberInt(len(v)))
					o.SetArrayItem(n, t)
					n++
					if n > 99 {
						break
					}
				}
			}
			return nil
		})

	} else {
		err = p.db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucket).Cursor()
			if b != nil {
				for k, v := b.Seek(prefix); bytes.HasPrefix(k, prefix); k, v = b.Next() {
					t := p.aa.NewObject()
					t.Set("k", p.aa.NewStringBytes(k))
					t.Set("v", p.aa.NewString(base64.StdEncoding.EncodeToString(v)))
					t.Set("s", p.aa.NewNumberInt(len(v)))
					o.SetArrayItem(n, t)
					n++
					if n > 99 {
						break
					}
				}
			}
			return nil
		})
	}

	return o, err
}
func (p *api) init() {
	p.action = map[string]func(v *fastjson.Value) (*fastjson.Value, error){"bucket": p.buckets,
		"bucket.create": p.createBucket,
		"bucket.delete": p.deleteBucket,
		"bucket.list":   p.buckets,
		"db.close":      p.close,
		"key.delete":    p.deleteKey,
		"key.get":       p.get,
		"key.put":       p.put,
		"key.scan":      p.prefixScan,
	}

}
func (p *api) open(path string) error {
	if len(path) == 0 {
		return bolt.ErrInvalid
	}
	var err error
	err = os.MkdirAll(filepath.Dir(path), os.ModePerm)
	if err != nil {
		return err
	}
	p.db, err = bolt.Open(path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	return err
}

func (p *api) close(_ *fastjson.Value) (*fastjson.Value, error) {
	if p.db == nil {
		return nil, bolt.ErrInvalid
	}
	err := p.db.Close()
	p.db = nil
	return nil, err
}

func (p *api) Execute(method string, params *fastjson.Value) (*fastjson.Value, error) {
	if p.action == nil {
		p.init()
	}
	m, ok := p.action[method]
	if !ok || p.db == nil {
		return nil, bolt.ErrInvalid
	}
	return m(params)
}
