package main

import (
	"encoding/json"
	"errors"
	"lyrics-api-go/cache"
	"path/filepath"
	"testing"
	"time"
)

// setupTestEnvironment creates a temporary cache for testing
func setupTestEnvironment(t *testing.T) func() {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_cache.db")
	backupPath := filepath.Join(tmpDir, "backups")

	var err error
	persistentCache, err = cache.NewPersistentCache(dbPath, backupPath, false)
	if err != nil {
		t.Fatalf("Failed to create test cache: %v", err)
	}

	return func() {
		persistentCache.Close()
	}
}

func TestShouldNegativeCache(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "no track found error",
			err:      errors.New("no track found for query: Shape of You"),
			expected: true,
		},
		{
			name:     "no tracks found within duration",
			err:      errors.New("no tracks found within 2000ms of requested duration 234000ms"),
			expected: true,
		},
		{
			name:     "TTML content is empty",
			err:      errors.New("TTML content is empty"),
			expected: true,
		},
		{
			name:     "search failed with no track found",
			err:      errors.New("search failed: no track found for query: Test"),
			expected: true,
		},
		{
			name:     "network error - should not cache",
			err:      errors.New("search failed: connection refused"),
			expected: false,
		},
		{
			name:     "rate limit error - should not cache",
			err:      errors.New("search failed: 429 Too Many Requests"),
			expected: false,
		},
		{
			name:     "generic API error - should not cache",
			err:      errors.New("failed to fetch TTML: server error"),
			expected: false,
		},
		{
			name:     "timeout error - should not cache",
			err:      errors.New("context deadline exceeded"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldNegativeCache(tt.err)
			if result != tt.expected {
				t.Errorf("shouldNegativeCache(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestSetAndGetNegativeCache(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	cacheKey := "ttml_lyrics:Test Song Test Artist"
	reason := "no track found for query: Test Song Test Artist"

	// Initially not in negative cache
	_, found := getNegativeCache(cacheKey)
	if found {
		t.Error("Expected key to not be in negative cache initially")
	}

	// Set negative cache
	setNegativeCache(cacheKey, reason)

	// Should now be found
	retrievedReason, found := getNegativeCache(cacheKey)
	if !found {
		t.Error("Expected key to be in negative cache after setting")
	}
	if retrievedReason != reason {
		t.Errorf("Expected reason %q, got %q", reason, retrievedReason)
	}
}

func TestNegativeCacheExpiration(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	cacheKey := "ttml_lyrics:Expired Song Artist"
	reason := "no track found"

	// Manually create an expired entry
	negativeKey := "no_lyrics:" + cacheKey
	entry := NegativeCacheEntry{
		Reason:    reason,
		Timestamp: time.Now().Add(-8 * 24 * time.Hour).Unix(), // 8 days ago (expired with 7 day TTL)
	}
	data, _ := json.Marshal(entry)
	persistentCache.Set(negativeKey, string(data))

	// Should not be found (expired)
	_, found := getNegativeCache(cacheKey)
	if found {
		t.Error("Expected expired entry to not be found")
	}

	// Entry should be deleted after expiration check
	_, exists := persistentCache.Get(negativeKey)
	if exists {
		t.Error("Expected expired entry to be deleted from cache")
	}
}

func TestNegativeCacheNotExpired(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	cacheKey := "ttml_lyrics:Recent Song Artist"
	reason := "no track found"

	// Manually create a recent entry (1 day ago)
	negativeKey := "no_lyrics:" + cacheKey
	entry := NegativeCacheEntry{
		Reason:    reason,
		Timestamp: time.Now().Add(-1 * 24 * time.Hour).Unix(), // 1 day ago
	}
	data, _ := json.Marshal(entry)
	persistentCache.Set(negativeKey, string(data))

	// Should be found (not expired)
	retrievedReason, found := getNegativeCache(cacheKey)
	if !found {
		t.Error("Expected non-expired entry to be found")
	}
	if retrievedReason != reason {
		t.Errorf("Expected reason %q, got %q", reason, retrievedReason)
	}
}

func TestNegativeCacheInvalidJSON(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	cacheKey := "ttml_lyrics:Invalid JSON Song"
	negativeKey := "no_lyrics:" + cacheKey

	// Store invalid JSON
	persistentCache.Set(negativeKey, "not valid json")

	// Should not be found (invalid JSON)
	_, found := getNegativeCache(cacheKey)
	if found {
		t.Error("Expected invalid JSON entry to not be found")
	}
}

func TestNegativeCacheKeyFormat(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test that negative cache uses correct key prefix
	cacheKey := "ttml_lyrics:Song Artist Album 234s"
	reason := "Lyrics not available for this track"

	setNegativeCache(cacheKey, reason)

	// Verify it's stored with the correct prefix
	expectedNegativeKey := "no_lyrics:" + cacheKey
	stored, found := persistentCache.Get(expectedNegativeKey)
	if !found {
		t.Errorf("Expected negative cache entry at key %q", expectedNegativeKey)
	}

	// Verify the stored entry is valid JSON
	var entry NegativeCacheEntry
	if err := json.Unmarshal([]byte(stored), &entry); err != nil {
		t.Errorf("Expected valid JSON in negative cache, got error: %v", err)
	}
	if entry.Reason != reason {
		t.Errorf("Expected reason %q, got %q", reason, entry.Reason)
	}
	if entry.Timestamp == 0 {
		t.Error("Expected non-zero timestamp")
	}
}

func TestBuildFallbackCacheKeys(t *testing.T) {
	tests := []struct {
		name        string
		songName    string
		artistName  string
		albumName   string
		durationStr string
		originalKey string
		expected    []string
	}{
		{
			name:        "With album and duration",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "Divide",
			durationStr: "234",
			originalKey: "ttml_lyrics:Shape of You Ed Sheeran Divide 234s",
			expected:    []string{"ttml_lyrics:Shape of You Ed Sheeran  234s"},
		},
		{
			name:        "With album, no duration",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "Divide",
			durationStr: "",
			originalKey: "ttml_lyrics:Shape of You Ed Sheeran Divide",
			expected:    []string{"ttml_lyrics:Shape of You Ed Sheeran "},
		},
		{
			name:        "No album - no fallback",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "",
			durationStr: "234",
			originalKey: "ttml_lyrics:Shape of You Ed Sheeran  234s",
			expected:    []string{},
		},
		{
			name:        "No album, no duration - no fallback",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "",
			durationStr: "",
			originalKey: "ttml_lyrics:Shape of You Ed Sheeran ",
			expected:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildFallbackCacheKeys(tt.songName, tt.artistName, tt.albumName, tt.durationStr, tt.originalKey)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d fallback keys, got %d", len(tt.expected), len(result))
				return
			}

			for i, key := range result {
				if key != tt.expected[i] {
					t.Errorf("Expected fallback key %q, got %q", tt.expected[i], key)
				}
			}
		})
	}
}

func TestCachedLyricsJSONFormat(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	cacheKey := "ttml_lyrics:Test Song Artist"
	ttml := "<tt>test ttml content</tt>"
	trackDurationMs := 234000

	// Set cached lyrics
	setCachedLyrics(cacheKey, ttml, trackDurationMs)

	// Get and verify
	retrievedTTML, retrievedDuration, found := getCachedLyrics(cacheKey)
	if !found {
		t.Error("Expected to find cached lyrics")
	}
	if retrievedTTML != ttml {
		t.Errorf("Expected TTML %q, got %q", ttml, retrievedTTML)
	}
	if retrievedDuration != trackDurationMs {
		t.Errorf("Expected duration %d, got %d", trackDurationMs, retrievedDuration)
	}
}

func TestCachedLyricsBackwardsCompatibility(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	cacheKey := "ttml_lyrics:Old Format Song"
	oldFormatTTML := "<tt>old format ttml</tt>"

	// Store in old format (plain string, not JSON)
	persistentCache.Set(cacheKey, oldFormatTTML)

	// Should still be retrievable
	retrievedTTML, retrievedDuration, found := getCachedLyrics(cacheKey)
	if !found {
		t.Error("Expected to find old format cached lyrics")
	}
	if retrievedTTML != oldFormatTTML {
		t.Errorf("Expected TTML %q, got %q", oldFormatTTML, retrievedTTML)
	}
	if retrievedDuration != 0 {
		t.Errorf("Expected duration 0 for old format, got %d", retrievedDuration)
	}
}
