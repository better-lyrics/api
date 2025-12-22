package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupTestCache creates a temporary cache for testing
func setupTestCache(t *testing.T, compression bool) (*PersistentCache, string, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_cache.db")
	backupPath := filepath.Join(tmpDir, "backups")

	cache, err := NewPersistentCache(dbPath, backupPath, compression)
	if err != nil {
		t.Fatalf("Failed to create test cache: %v", err)
	}

	// Wait for background preload to complete
	cache.WaitForPreload()

	cleanup := func() {
		cache.Close()
	}

	return cache, tmpDir, cleanup
}

func TestNewPersistentCache(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cache.db")
	backupPath := filepath.Join(tmpDir, "backups")

	cache, err := NewPersistentCache(dbPath, backupPath, true)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer cache.Close()

	if cache.db == nil {
		t.Error("Expected database to be initialized")
	}
	if cache.dbPath != dbPath {
		t.Errorf("Expected dbPath %q, got %q", dbPath, cache.dbPath)
	}
	if cache.backupPath != backupPath {
		t.Errorf("Expected backupPath %q, got %q", backupPath, cache.backupPath)
	}
	if !cache.compressionEnabled {
		t.Error("Expected compression to be enabled")
	}

	// Verify directories were created
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Error("Expected cache directory to be created")
	}
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Error("Expected backup directory to be created")
	}
}

func TestSetAndGet(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	key := "test_key"
	value := "test_value"

	// Set a value
	err := cache.Set(key, value)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	// Get the value
	retrieved, found := cache.Get(key)
	if !found {
		t.Error("Expected to find the key, but it was not found")
	}
	if retrieved != value {
		t.Errorf("Expected value %q, got %q", value, retrieved)
	}
}

func TestSetAndGetWithCompression(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, true)
	defer cleanup()

	key := "compressed_key"
	value := "This is a longer value that should be compressed using gzip compression"

	// Set a value
	err := cache.Set(key, value)
	if err != nil {
		t.Fatalf("Failed to set compressed value: %v", err)
	}

	// Get the value (should be automatically decompressed)
	retrieved, found := cache.Get(key)
	if !found {
		t.Error("Expected to find the compressed key")
	}
	if retrieved != value {
		t.Errorf("Expected decompressed value %q, got %q", value, retrieved)
	}
}

func TestGetNonExistentKey(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	_, found := cache.Get("nonexistent_key")
	if found {
		t.Error("Expected not to find non-existent key")
	}
}

func TestDelete(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	key := "delete_test"
	value := "to_be_deleted"

	// Set a value
	cache.Set(key, value)

	// Verify it exists
	_, found := cache.Get(key)
	if !found {
		t.Error("Expected key to exist before deletion")
	}

	// Delete the key
	err := cache.Delete(key)
	if err != nil {
		t.Fatalf("Failed to delete key: %v", err)
	}

	// Verify it's gone
	_, found = cache.Get(key)
	if found {
		t.Error("Expected key to be deleted")
	}
}

func TestClear(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Add multiple entries
	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")

	// Verify they exist
	numKeys, _ := cache.Stats()
	if numKeys != 3 {
		t.Errorf("Expected 3 keys before clear, got %d", numKeys)
	}

	// Clear the cache
	err := cache.Clear()
	if err != nil {
		t.Fatalf("Failed to clear cache: %v", err)
	}

	// Verify cache is empty
	numKeys, _ = cache.Stats()
	if numKeys != 0 {
		t.Errorf("Expected 0 keys after clear, got %d", numKeys)
	}

	// Verify keys are not retrievable
	_, found := cache.Get("key1")
	if found {
		t.Error("Expected key1 to be cleared")
	}
}

func TestStats(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Empty cache
	numKeys, _ := cache.Stats()
	if numKeys != 0 {
		t.Errorf("Expected 0 keys in empty cache, got %d", numKeys)
	}

	// Add some entries
	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")

	numKeys, sizeInKB := cache.Stats()
	if numKeys != 3 {
		t.Errorf("Expected 3 keys, got %d", numKeys)
	}
	if sizeInKB < 0 {
		t.Errorf("Expected non-negative size, got %d KB", sizeInKB)
	}
}

func TestRange(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Add entries
	entries := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	for k, v := range entries {
		cache.Set(k, v)
	}

	// Range over all entries
	found := make(map[string]string)
	cache.Range(func(key string, entry CacheEntry) bool {
		found[key] = entry.Value
		return true
	})

	if len(found) != len(entries) {
		t.Errorf("Expected %d entries, found %d", len(entries), len(found))
	}

	for key := range entries {
		if _, ok := found[key]; !ok {
			t.Errorf("Expected to find key %q in Range iteration", key)
		}
	}
}

func TestBackup(t *testing.T) {
	cache, tmpDir, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Add some data
	cache.Set("backup_key1", "backup_value1")
	cache.Set("backup_key2", "backup_value2")

	// Create backup
	backupPath, err := cache.Backup()
	if err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}

	// Verify backup file exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Errorf("Expected backup file to exist at %q", backupPath)
	}

	// Verify backup is in the correct directory
	expectedDir := filepath.Join(tmpDir, "backups")
	if filepath.Dir(backupPath) != expectedDir {
		t.Errorf("Expected backup in %q, got %q", expectedDir, filepath.Dir(backupPath))
	}

	// Verify filename format
	filename := filepath.Base(backupPath)
	if len(filename) < len("cache_backup_") {
		t.Errorf("Unexpected backup filename format: %q", filename)
	}
}

func TestBackupAndClear(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Add some data
	cache.Set("clear_key1", "clear_value1")
	cache.Set("clear_key2", "clear_value2")

	numKeys, _ := cache.Stats()
	if numKeys != 2 {
		t.Errorf("Expected 2 keys before backup and clear, got %d", numKeys)
	}

	// Backup and clear
	backupPath, err := cache.BackupAndClear()
	if err != nil {
		t.Fatalf("Failed to backup and clear: %v", err)
	}

	// Verify backup exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Errorf("Expected backup file to exist at %q", backupPath)
	}

	// Verify cache is cleared
	numKeys, _ = cache.Stats()
	if numKeys != 0 {
		t.Errorf("Expected 0 keys after clear, got %d", numKeys)
	}

	_, found := cache.Get("clear_key1")
	if found {
		t.Error("Expected cache to be cleared")
	}
}

func TestLoadToMemory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "persistent.db")
	backupPath := filepath.Join(tmpDir, "backups")

	// Create cache and add data
	cache1, err := NewPersistentCache(dbPath, backupPath, false)
	if err != nil {
		t.Fatalf("Failed to create first cache: %v", err)
	}

	cache1.Set("persistent_key", "persistent_value")
	cache1.Close()

	// Create new cache instance with same db path
	cache2, err := NewPersistentCache(dbPath, backupPath, false)
	if err != nil {
		t.Fatalf("Failed to create second cache: %v", err)
	}
	defer cache2.Close()

	// Verify data was loaded from disk to memory
	value, found := cache2.Get("persistent_key")
	if !found {
		t.Error("Expected to find key loaded from disk")
	}
	if value != "persistent_value" {
		t.Errorf("Expected value %q, got %q", "persistent_value", value)
	}
}

func TestMemoryCacheFallback(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	key := "memory_test"
	value := "memory_value"

	// Set value (goes to both memory and disk)
	cache.Set(key, value)

	// Get from memory cache (should be fast)
	retrieved, found := cache.Get(key)
	if !found {
		t.Error("Expected to find value in cache")
	}
	if retrieved != value {
		t.Errorf("Expected value %q, got %q", value, retrieved)
	}

	// Clear memory cache only (not touching disk)
	cache.memCache.Delete(key)

	// Get should still work (falls back to disk)
	retrieved, found = cache.Get(key)
	if !found {
		t.Error("Expected to find value in disk cache")
	}
	if retrieved != value {
		t.Errorf("Expected value %q from disk, got %q", value, retrieved)
	}
}

func TestMultipleEntriesWithCompression(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, true)
	defer cleanup()

	entries := map[string]string{
		"json1": `{"name":"John","age":30,"city":"New York"}`,
		"json2": `{"items":["apple","banana","orange"],"count":3}`,
		"text1": "This is a longer text that should compress well with gzip compression algorithm",
		"text2": "Another piece of text with repeated words repeated words repeated words",
	}

	// Set all entries
	for key, value := range entries {
		err := cache.Set(key, value)
		if err != nil {
			t.Fatalf("Failed to set key %q: %v", key, err)
		}
	}

	// Verify all entries can be retrieved correctly
	for key, expectedValue := range entries {
		retrieved, found := cache.Get(key)
		if !found {
			t.Errorf("Expected to find key %q", key)
			continue
		}
		if retrieved != expectedValue {
			t.Errorf("Key %q: expected %q, got %q", key, expectedValue, retrieved)
		}
	}

	// Verify count
	numKeys, _ := cache.Stats()
	if numKeys != len(entries) {
		t.Errorf("Expected %d keys, got %d", len(entries), numKeys)
	}
}

func TestCacheWithEmptyValue(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	key := "empty_key"
	value := ""

	err := cache.Set(key, value)
	if err != nil {
		t.Fatalf("Failed to set empty value: %v", err)
	}

	retrieved, found := cache.Get(key)
	if !found {
		t.Error("Expected to find key with empty value")
	}
	if retrieved != value {
		t.Errorf("Expected empty string, got %q", retrieved)
	}
}

func TestListBackups(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Initially no backups
	backups, err := cache.ListBackups()
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("Expected 0 backups initially, got %d", len(backups))
	}

	// Create some data and backup
	cache.Set("key1", "value1")
	backupPath1, err := cache.Backup()
	if err != nil {
		t.Fatalf("Failed to create first backup: %v", err)
	}

	// List should show 1 backup
	backups, err = cache.ListBackups()
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) != 1 {
		t.Errorf("Expected 1 backup, got %d", len(backups))
	}
	if backups[0].FilePath != backupPath1 {
		t.Errorf("Expected backup path %q, got %q", backupPath1, backups[0].FilePath)
	}

	// Create another backup (wait 1 second to ensure unique timestamp)
	time.Sleep(1 * time.Second)
	cache.Set("key2", "value2")
	_, err = cache.Backup()
	if err != nil {
		t.Fatalf("Failed to create second backup: %v", err)
	}

	// List should show 2 backups
	backups, err = cache.ListBackups()
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) != 2 {
		t.Errorf("Expected 2 backups, got %d", len(backups))
	}

	// Verify backup info fields
	for _, b := range backups {
		if b.FileName == "" {
			t.Error("Expected non-empty FileName")
		}
		if b.FilePath == "" {
			t.Error("Expected non-empty FilePath")
		}
		if b.Size <= 0 {
			t.Errorf("Expected positive Size, got %d", b.Size)
		}
		if b.CreatedAt.IsZero() {
			t.Error("Expected non-zero CreatedAt")
		}
	}
}

func TestRestoreFromBackup(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Create initial data
	cache.Set("original_key", "original_value")

	// Create backup
	backupPath, err := cache.Backup()
	if err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}
	cache.WaitForPreload() // Wait for preload after reopenDatabase
	backupFileName := filepath.Base(backupPath)

	// Modify data after backup
	cache.Set("original_key", "modified_value")
	cache.Set("new_key", "new_value")

	// Verify modification
	val, _ := cache.Get("original_key")
	if val != "modified_value" {
		t.Errorf("Expected modified_value, got %q", val)
	}

	// Restore from backup
	err = cache.RestoreFromBackup(backupFileName)
	if err != nil {
		t.Fatalf("Failed to restore from backup: %v", err)
	}
	cache.WaitForPreload() // Wait for preload after reopenDatabase

	// Verify data is restored to backup state
	val, found := cache.Get("original_key")
	if !found {
		t.Error("Expected to find original_key after restore")
	}
	if val != "original_value" {
		t.Errorf("Expected original_value after restore, got %q", val)
	}

	// new_key should not exist (wasn't in backup)
	_, found = cache.Get("new_key")
	if found {
		t.Error("Expected new_key to not exist after restore")
	}
}

func TestRestoreFromBackup_InvalidFile(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Try to restore from non-existent backup
	err := cache.RestoreFromBackup("nonexistent_backup.db")
	if err == nil {
		t.Error("Expected error when restoring from non-existent backup")
	}

	// Try to restore from invalid file extension
	err = cache.RestoreFromBackup("invalid_backup.txt")
	if err == nil {
		t.Error("Expected error when restoring from non-.db file")
	}
}

func TestDeleteBackup(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Create a backup
	cache.Set("key1", "value1")
	backupPath, err := cache.Backup()
	if err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}
	backupFileName := filepath.Base(backupPath)

	// Verify backup exists
	backups, _ := cache.ListBackups()
	if len(backups) != 1 {
		t.Errorf("Expected 1 backup before delete, got %d", len(backups))
	}

	// Delete the backup
	err = cache.DeleteBackup(backupFileName)
	if err != nil {
		t.Fatalf("Failed to delete backup: %v", err)
	}

	// Verify backup is deleted
	backups, _ = cache.ListBackups()
	if len(backups) != 0 {
		t.Errorf("Expected 0 backups after delete, got %d", len(backups))
	}

	// Verify file is gone
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Error("Expected backup file to be deleted from disk")
	}
}

func TestDeleteBackup_InvalidFile(t *testing.T) {
	cache, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	// Try to delete non-existent backup
	err := cache.DeleteBackup("nonexistent_backup.db")
	if err == nil {
		t.Error("Expected error when deleting non-existent backup")
	}

	// Try to delete invalid file extension
	err = cache.DeleteBackup("invalid_backup.txt")
	if err == nil {
		t.Error("Expected error when deleting non-.db file")
	}
}
