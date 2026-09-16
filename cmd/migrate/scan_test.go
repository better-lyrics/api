package main

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func openScanTestDB(t *testing.T, keys []string) *bolt.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scan.db")
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	err = db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("cache"))
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := b.Put([]byte(k), []byte("v-"+k)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed bolt: %v", err)
	}
	return db
}

func TestScanBucketBatch_EmptyBucketMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	defer db.Close()

	entries, next, exhausted, err := scanBucketBatch(db, "cache", nil, 10)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %d, want 0", len(entries))
	}
	if !exhausted {
		t.Error("expected exhausted = true for missing bucket")
	}
	if next != nil {
		t.Errorf("next = %v, want nil", next)
	}
}

func TestScanBucketBatch_SingleBatchCoversAll(t *testing.T) {
	db := openScanTestDB(t, []string{"a", "b", "c"})

	entries, next, exhausted, err := scanBucketBatch(db, "cache", nil, 10)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if !exhausted {
		t.Error("expected exhausted = true")
	}
	if string(next) != "c" {
		t.Errorf("next = %q, want %q", next, "c")
	}
}

func TestScanBucketBatch_ResumesFromCheckpoint(t *testing.T) {
	db := openScanTestDB(t, []string{"a", "b", "c", "d", "e"})

	entries1, next1, exhausted1, err := scanBucketBatch(db, "cache", nil, 2)
	if err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	if len(entries1) != 2 || string(entries1[0].key) != "a" || string(entries1[1].key) != "b" {
		t.Fatalf("unexpected batch 1: %+v", entries1)
	}
	if exhausted1 {
		t.Error("batch 1 should not be exhausted")
	}
	if string(next1) != "b" {
		t.Errorf("next1 = %q, want %q", next1, "b")
	}

	entries2, next2, exhausted2, err := scanBucketBatch(db, "cache", next1, 2)
	if err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if len(entries2) != 2 || string(entries2[0].key) != "c" || string(entries2[1].key) != "d" {
		t.Fatalf("unexpected batch 2: %+v", entries2)
	}
	if exhausted2 {
		t.Error("batch 2 should not be exhausted")
	}

	entries3, _, exhausted3, err := scanBucketBatch(db, "cache", next2, 2)
	if err != nil {
		t.Fatalf("scan 3: %v", err)
	}
	if len(entries3) != 1 || string(entries3[0].key) != "e" {
		t.Fatalf("unexpected batch 3: %+v", entries3)
	}
	if !exhausted3 {
		t.Error("batch 3 should be exhausted")
	}
}

func TestScanBucketBatch_ResumeAtExactEnd(t *testing.T) {
	db := openScanTestDB(t, []string{"a", "b"})

	entries, next, exhausted, err := scanBucketBatch(db, "cache", nil, 10)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 2 || !exhausted {
		t.Fatalf("unexpected first scan: entries=%d exhausted=%v", len(entries), exhausted)
	}

	entries2, _, exhausted2, err := scanBucketBatch(db, "cache", next, 10)
	if err != nil {
		t.Fatalf("scan resume: %v", err)
	}
	if len(entries2) != 0 {
		t.Errorf("entries2 = %d, want 0", len(entries2))
	}
	if !exhausted2 {
		t.Error("expected exhausted = true when resuming past the end")
	}
}
