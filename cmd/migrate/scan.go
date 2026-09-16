package main

import (
	"bytes"

	bolt "go.etcd.io/bbolt"
)

type kv struct {
	key   []byte
	value []byte
}

// scanBucketBatch reads up to batchSize entries starting strictly after afterKey.
func scanBucketBatch(db *bolt.DB, bucketName string, afterKey []byte, batchSize int) (entries []kv, nextKey []byte, exhausted bool, err error) {
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			exhausted = true
			return nil
		}

		c := b.Cursor()
		var k, v []byte
		if len(afterKey) == 0 {
			k, v = c.First()
		} else {
			k, v = c.Seek(afterKey)
			if k != nil && bytes.Equal(k, afterKey) {
				k, v = c.Next()
			}
		}

		for k != nil && len(entries) < batchSize {
			entries = append(entries, kv{
				key:   append([]byte(nil), k...),
				value: append([]byte(nil), v...),
			})
			k, v = c.Next()
		}
		if k == nil {
			exhausted = true
		}
		return nil
	})

	if len(entries) > 0 {
		nextKey = entries[len(entries)-1].key
	} else {
		nextKey = afterKey
	}
	return entries, nextKey, exhausted, err
}
