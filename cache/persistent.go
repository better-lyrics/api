package cache

import (
	"encoding/json"
	"fmt"
	"lyrics-api-go/utils"
	"os"
	"path/filepath"
	"sync"

	log "github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

const bucketName = "cache"

// PersistentCache wraps BoltDB with an in-memory cache for fast access
type PersistentCache struct {
	db                *bolt.DB
	memCache          sync.Map
	dbPath            string
	compressionEnabled bool
}

// CacheEntry represents a cached value (can be compressed)
type CacheEntry struct {
	Value string `json:"value"`
}

// NewPersistentCache creates a new persistent cache
func NewPersistentCache(dbPath string, compressionEnabled bool) (*PersistentCache, error) {
	// Create directory if it doesn't exist (needed for Railway volumes)
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %v", err)
	}

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache database: %v", err)
	}

	// Create bucket if it doesn't exist
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create cache bucket: %v", err)
	}

	pc := &PersistentCache{
		db:                db,
		dbPath:            dbPath,
		compressionEnabled: compressionEnabled,
	}

	// Load all entries into memory cache
	if err := pc.loadToMemory(); err != nil {
		log.Warnf("[Cache] Failed to preload cache to memory: %v", err)
	}

	log.Infof("[Cache] Persistent cache initialized at %s (compression: %v)", dbPath, compressionEnabled)
	return pc, nil
}

// loadToMemory loads all cache entries from disk to memory
func (pc *PersistentCache) loadToMemory() error {
	count := 0
	err := pc.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}

		return b.ForEach(func(k, v []byte) error {
			var entry CacheEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				log.Warnf("[Cache] Failed to unmarshal cache entry for key %s: %v", string(k), err)
				return nil // Continue to next entry
			}
			pc.memCache.Store(string(k), entry)
			count++
			return nil
		})
	})

	if err != nil {
		return err
	}

	log.Infof("[Cache] Loaded %d entries from disk to memory", count)
	return nil
}

// Get retrieves a value from cache (checks memory first, then disk)
// Returns decompressed value if compression is enabled
func (pc *PersistentCache) Get(key string) (string, bool) {
	// Try memory cache first
	if entry, ok := pc.memCache.Load(key); ok {
		value := entry.(CacheEntry).Value

		// Decompress if needed
		if pc.compressionEnabled {
			decompressed, err := utils.DecompressString(value)
			if err != nil {
				log.Errorf("[Cache] Error decompressing cache value for key %s: %v", key, err)
				return "", false
			}
			return decompressed, true
		}

		return value, true
	}

	// Try disk cache
	var value string
	err := pc.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("key not found")
		}

		var entry CacheEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return err
		}

		value = entry.Value
		// Update memory cache with compressed value
		pc.memCache.Store(key, entry)
		return nil
	})

	if err != nil {
		return "", false
	}

	// Decompress if needed
	if pc.compressionEnabled {
		decompressed, err := utils.DecompressString(value)
		if err != nil {
			log.Errorf("[Cache] Error decompressing cache value for key %s: %v", key, err)
			return "", false
		}
		return decompressed, true
	}

	return value, true
}

// Set stores a value in cache (both memory and disk)
// Compresses value if compression is enabled
func (pc *PersistentCache) Set(key, value string) error {
	var finalValue string
	var err error

	// Compress if needed
	if pc.compressionEnabled {
		finalValue, err = utils.CompressString(value)
		if err != nil {
			log.Errorf("[Cache] Error compressing cache value for key %s: %v", key, err)
			return err
		}
	} else {
		finalValue = value
	}

	entry := CacheEntry{
		Value: finalValue,
	}

	// Store in memory (compressed)
	pc.memCache.Store(key, entry)

	// Store in disk (compressed)
	return pc.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return fmt.Errorf("bucket not found")
		}

		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}

		return b.Put([]byte(key), data)
	})
}

// Delete removes a key from cache
func (pc *PersistentCache) Delete(key string) error {
	pc.memCache.Delete(key)

	return pc.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return fmt.Errorf("bucket not found")
		}
		return b.Delete([]byte(key))
	})
}

// Clear removes all entries from cache
func (pc *PersistentCache) Clear() error {
	// Clear memory cache
	pc.memCache.Range(func(key, value interface{}) bool {
		pc.memCache.Delete(key)
		return true
	})

	// Clear disk cache
	return pc.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket([]byte(bucketName)); err != nil {
			return err
		}
		_, err := tx.CreateBucket([]byte(bucketName))
		return err
	})
}

// Range iterates over all cache entries
func (pc *PersistentCache) Range(fn func(key string, entry CacheEntry) bool) {
	pc.memCache.Range(func(k, v interface{}) bool {
		return fn(k.(string), v.(CacheEntry))
	})
}

// Stats returns cache statistics
func (pc *PersistentCache) Stats() (numKeys int, sizeInKB int) {
	pc.memCache.Range(func(k, v interface{}) bool {
		entry := v.(CacheEntry)
		numKeys++
		sizeInKB += len(k.(string)) + len(entry.Value)
		return true
	})
	sizeInKB = sizeInKB / 1024
	return
}

// Close closes the database connection
func (pc *PersistentCache) Close() error {
	if pc.db != nil {
		return pc.db.Close()
	}
	return nil
}
