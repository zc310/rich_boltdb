package store

import (
	"fmt"
	"math/rand"
	"os"

	bolt "go.etcd.io/bbolt"
)

// sampleSeed keeps the generated file byte-for-byte reproducible, which makes
// it usable from tests as well as from the "Sample" button.
const sampleSeed = 20240101

// SampleFileName is the name of the generated database inside the system
// temporary directory.
const SampleFileName = "boltdb_viewer_sample.db"

// SamplePath returns the location CreateSample writes to.
func SamplePath() string {
	return os.TempDir() + string(os.PathSeparator) + SampleFileName
}

// CreateSample writes a demonstration database to path, replacing whatever was
// there. The layout keeps the three padded-key buckets the original viewer
// generated and adds nested buckets, so the tree can be exercised at more than
// one level without a real file to hand.
func CreateSample(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Update(writeSample); err != nil {
		return err
	}
	// Close again before returning so the file has no lingering lock or dirty
	// pages when the caller opens it.
	return nil
}

func writeSample(tx *bolt.Tx) error {
	rnd := rand.New(rand.NewSource(sampleSeed))

	// The three flat buckets the original sample used.
	for _, name := range []string{"MyBucket_A", "MyBucket_B", "MyBucket_C"} {
		b, err := tx.CreateBucket([]byte(name))
		if err != nil {
			return err
		}
		suffix := name[len("MyBucket_") : len("MyBucket_")+1]
		for i := range 9 {
			key := fmt.Sprintf("key_%s_%06d", suffix, i)
			if err := b.Put([]byte(key), binaryValue(rnd)); err != nil {
				return err
			}
		}
	}

	// A readable bucket, so the Raw view has something to show.
	users, err := tx.CreateBucket([]byte("Users"))
	if err != nil {
		return err
	}
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("user:%d", i)
		value := fmt.Sprintf(`{"id":%d,"name":"User %d","email":"user%d@example.com"}`, i, i, i)
		if err := users.Put([]byte(key), []byte(value)); err != nil {
			return err
		}
	}

	// Three levels: Users / Profile / Fields.
	profile, err := users.CreateBucket([]byte("Profile"))
	if err != nil {
		return err
	}
	fields, err := profile.CreateBucket([]byte("Fields"))
	if err != nil {
		return err
	}
	for _, name := range []string{"display_name", "locale", "timezone"} {
		if err := fields.Put([]byte(name), []byte("value of "+name)); err != nil {
			return err
		}
	}
	if err := profile.Put([]byte("created"), []byte("2024-01-01")); err != nil {
		return err
	}

	// An empty bucket, to check that a childless bucket is not mistaken for a
	// key holding zero bytes.
	if _, err := tx.CreateBucket([]byte("Empty")); err != nil {
		return err
	}

	// A key containing the path separator. Resolving it must not be confused
	// with the nested bucket Users/Profile.
	if err := users.Put([]byte("a/b"), []byte("separator inside a key")); err != nil {
		return err
	}

	// A bucket whose own name contains the separator.
	odd, err := tx.CreateBucket([]byte("logs/2024"))
	if err != nil {
		return err
	}
	if err := odd.Put([]byte("count"), []byte("3")); err != nil {
		return err
	}

	return nil
}

// CreateFlat writes a database with a single top-level bucket holding the given
// keys, each mapped to its own name as the value. It exists so the tests can
// exercise the case where every key shares one bucket.
func CreateFlat(path string, bucket string, keys []string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucket([]byte(bucket))
		if err != nil {
			return err
		}
		for _, key := range keys {
			if err := b.Put([]byte(key), []byte("value of "+key)); err != nil {
				return err
			}
		}
		return nil
	})
}

// binaryValue returns bytes that are deliberately not valid UTF-8 and include
// control characters, so the Base64 and Hex views are the useful ones.
func binaryValue(rnd *rand.Rand) []byte {
	out := make([]byte, rnd.Intn(99))
	for i := range out {
		out[i] = byte(rnd.Intn(256))
	}
	return out
}
