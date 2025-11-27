package cache

import (
	"encoding/json"
	"fmt"
	"io"
	"lyrics-api-go/utils"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

const bucketName = "cache"

// PersistentCache wraps BoltDB with an in-memory cache for fast access
type PersistentCache struct {
	db                 *bolt.DB
	memCache           sync.Map
	dbPath             string
	backupPath         string
	compressionEnabled bool
}

// CacheEntry represents a cached value (can be compressed)
type CacheEntry struct {
	Value string `json:"value"`
}

// NewPersistentCache creates a new persistent cache
func NewPersistentCache(dbPath string, backupPath string, compressionEnabled bool) (*PersistentCache, error) {
	// Create directory if it doesn't exist (needed for Railway volumes)
	dir := filepath.Dir(dbPath)

	// Check if directory exists
	if info, err := os.Stat(dir); err == nil {
		log.Infof("[Cache:Init] Directory %s exists (IsDir: %v)", dir, info.IsDir())
	} else {
		log.Infof("[Cache:Init] Directory %s does not exist, creating...", dir)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %v", err)
	}

	// Create backup directory
	if err := os.MkdirAll(backupPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %v", err)
	}
	log.Infof("[Cache:Init] Backup directory set to: %s", backupPath)

	// Check if database file already exists
	if info, err := os.Stat(dbPath); err == nil {
		log.Infof("[Cache:Init] Found existing database file at: %s (size: %d bytes)", dbPath, info.Size())
	} else {
		log.Infof("[Cache:Init] Creating new database file at: %s", dbPath)
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
		db:                 db,
		dbPath:             dbPath,
		backupPath:         backupPath,
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

// Backup creates a backup of the cache database file
// Returns the backup file path
func (pc *PersistentCache) Backup() (string, error) {
	// Generate backup filename with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	backupFileName := fmt.Sprintf("cache_backup_%s.db", timestamp)
	backupFilePath := filepath.Join(pc.backupPath, backupFileName)

	log.Infof("[Cache:Backup] Creating backup at: %s", backupFilePath)

	// Close the database temporarily to ensure all data is flushed
	if err := pc.db.Close(); err != nil {
		return "", fmt.Errorf("failed to close database for backup: %v", err)
	}

	// Copy the database file to backup location
	if err := copyFile(pc.dbPath, backupFilePath); err != nil {
		// Try to reopen the database even if backup failed
		pc.reopenDatabase()
		return "", fmt.Errorf("failed to copy database file: %v", err)
	}

	// Reopen the database
	if err := pc.reopenDatabase(); err != nil {
		return "", fmt.Errorf("failed to reopen database after backup: %v", err)
	}

	log.Infof("[Cache:Backup] Backup created successfully: %s", backupFilePath)
	return backupFilePath, nil
}

// BackupAndClear creates a backup of the cache and then clears it
func (pc *PersistentCache) BackupAndClear() (string, error) {
	// Create backup first
	backupPath, err := pc.Backup()
	if err != nil {
		return "", fmt.Errorf("failed to create backup: %v", err)
	}

	// Clear the cache
	if err := pc.Clear(); err != nil {
		return backupPath, fmt.Errorf("backup created but failed to clear cache: %v", err)
	}

	log.Infof("[Cache:Clear] Cache cleared successfully (backup: %s)", backupPath)
	return backupPath, nil
}

// reopenDatabase reopens the database connection
func (pc *PersistentCache) reopenDatabase() error {
	db, err := bolt.Open(pc.dbPath, 0600, nil)
	if err != nil {
		return fmt.Errorf("failed to reopen database: %v", err)
	}
	pc.db = db

	// Reload memory cache
	if err := pc.loadToMemory(); err != nil {
		log.Warnf("[Cache] Failed to reload cache to memory: %v", err)
	}

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Sync to ensure data is written to disk
	return destFile.Sync()
}

// Close closes the database connection
func (pc *PersistentCache) Close() error {
	if pc.db != nil {
		return pc.db.Close()
	}
	return nil
}

// BackupInfo contains metadata about a backup file
type BackupInfo struct {
	FileName  string    `json:"fileName"`
	FilePath  string    `json:"filePath"`
	Size      int64     `json:"sizeBytes"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListBackups returns a list of all available backup files
func (pc *PersistentCache) ListBackups() ([]BackupInfo, error) {
	var backups []BackupInfo

	entries, err := os.ReadDir(pc.backupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return backups, nil // No backups directory yet
		}
		return nil, fmt.Errorf("failed to read backup directory: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only include .db files that match our backup pattern
		if filepath.Ext(entry.Name()) != ".db" {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			log.Warnf("[Cache:Backups] Failed to get info for %s: %v", entry.Name(), err)
			continue
		}

		backups = append(backups, BackupInfo{
			FileName:  entry.Name(),
			FilePath:  filepath.Join(pc.backupPath, entry.Name()),
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}

	return backups, nil
}

// RestoreFromBackup replaces the current cache database with a backup
// This will close the current database, replace the file, and reopen it
func (pc *PersistentCache) RestoreFromBackup(backupFileName string) error {
	backupFilePath := filepath.Join(pc.backupPath, backupFileName)

	// Validate backup file exists
	if _, err := os.Stat(backupFilePath); os.IsNotExist(err) {
		return fmt.Errorf("backup file not found: %s", backupFileName)
	}

	// Validate it's a .db file
	if filepath.Ext(backupFileName) != ".db" {
		return fmt.Errorf("invalid backup file: must be a .db file")
	}

	log.Infof("[Cache:Restore] Starting restore from backup: %s", backupFileName)

	// Close the current database
	if err := pc.db.Close(); err != nil {
		return fmt.Errorf("failed to close current database: %v", err)
	}

	// Create a backup of the current database before replacing (safety measure)
	currentBackupPath := pc.dbPath + ".pre-restore"
	if err := copyFile(pc.dbPath, currentBackupPath); err != nil {
		// Try to reopen the original database
		pc.reopenDatabase()
		return fmt.Errorf("failed to backup current database: %v", err)
	}

	// Replace the current database with the backup
	if err := copyFile(backupFilePath, pc.dbPath); err != nil {
		// Try to restore from pre-restore backup
		copyFile(currentBackupPath, pc.dbPath)
		pc.reopenDatabase()
		return fmt.Errorf("failed to restore backup: %v", err)
	}

	// Remove the pre-restore backup on success
	os.Remove(currentBackupPath)

	// Reopen the database with restored data
	if err := pc.reopenDatabase(); err != nil {
		return fmt.Errorf("failed to reopen database after restore: %v", err)
	}

	log.Infof("[Cache:Restore] Successfully restored from backup: %s", backupFileName)
	return nil
}

// DeleteBackup deletes a specific backup file
func (pc *PersistentCache) DeleteBackup(backupFileName string) error {
	backupFilePath := filepath.Join(pc.backupPath, backupFileName)

	// Validate it's a .db file
	if filepath.Ext(backupFileName) != ".db" {
		return fmt.Errorf("invalid backup file: must be a .db file")
	}

	// Validate backup file exists
	if _, err := os.Stat(backupFilePath); os.IsNotExist(err) {
		return fmt.Errorf("backup file not found: %s", backupFileName)
	}

	if err := os.Remove(backupFilePath); err != nil {
		return fmt.Errorf("failed to delete backup: %v", err)
	}

	log.Infof("[Cache:Backup] Deleted backup: %s", backupFileName)
	return nil
}
