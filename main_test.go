package main

import (
	"context"
	"encoding/json"
	"errors"
	"lyrics-api-go/cache"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	setNegativeCache(cacheKey, reason, "", false)

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

	setNegativeCache(cacheKey, reason, "", false)

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
			originalKey: "ttml_lyrics:shape of you ed sheeran divide 234s",
			expected:    []string{"ttml_lyrics:shape of you ed sheeran 234s"},
		},
		{
			name:        "With album, no duration",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "Divide",
			durationStr: "",
			originalKey: "ttml_lyrics:shape of you ed sheeran divide",
			expected:    []string{"ttml_lyrics:shape of you ed sheeran"},
		},
		{
			name:        "No album - no fallback",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "",
			durationStr: "234",
			originalKey: "ttml_lyrics:shape of you ed sheeran 234s",
			expected:    []string{},
		},
		{
			name:        "No album, no duration - no fallback",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "",
			durationStr: "",
			originalKey: "ttml_lyrics:shape of you ed sheeran",
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
	score := 0.95
	language := "en"
	isRTL := false

	// Set cached lyrics
	setCachedLyrics(cacheKey, ttml, trackDurationMs, score, language, isRTL)

	// Get and verify
	cached, found := getCachedLyrics(cacheKey)
	if !found {
		t.Error("Expected to find cached lyrics")
	}
	if cached.TTML != ttml {
		t.Errorf("Expected TTML %q, got %q", ttml, cached.TTML)
	}
	if cached.TrackDurationMs != trackDurationMs {
		t.Errorf("Expected duration %d, got %d", trackDurationMs, cached.TrackDurationMs)
	}
	if cached.Score != score {
		t.Errorf("Expected score %f, got %f", score, cached.Score)
	}
	if cached.Language != language {
		t.Errorf("Expected language %q, got %q", language, cached.Language)
	}
	if cached.IsRTL != isRTL {
		t.Errorf("Expected isRTL %v, got %v", isRTL, cached.IsRTL)
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
	cached, found := getCachedLyrics(cacheKey)
	if !found {
		t.Error("Expected to find old format cached lyrics")
	}
	if cached.TTML != oldFormatTTML {
		t.Errorf("Expected TTML %q, got %q", oldFormatTTML, cached.TTML)
	}
	if cached.TrackDurationMs != 0 {
		t.Errorf("Expected duration 0 for old format, got %d", cached.TrackDurationMs)
	}
}

func TestBuildNormalizedCacheKey(t *testing.T) {
	tests := []struct {
		name        string
		songName    string
		artistName  string
		albumName   string
		durationStr string
		expected    string
	}{
		{
			name:        "Basic case - lowercase and trimmed",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "",
			durationStr: "",
			expected:    "ttml_lyrics:shape of you ed sheeran",
		},
		{
			name:        "With album",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "Divide",
			durationStr: "",
			expected:    "ttml_lyrics:shape of you ed sheeran divide",
		},
		{
			name:        "With duration",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "",
			durationStr: "234",
			expected:    "ttml_lyrics:shape of you ed sheeran 234s",
		},
		{
			name:        "With album and duration",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "Divide",
			durationStr: "234",
			expected:    "ttml_lyrics:shape of you ed sheeran divide 234s",
		},
		{
			name:        "Whitespace trimming",
			songName:    "  Shape of You  ",
			artistName:  "  Ed Sheeran  ",
			albumName:   "",
			durationStr: "",
			expected:    "ttml_lyrics:shape of you ed sheeran",
		},
		{
			name:        "Mixed case",
			songName:    "SHAPE OF YOU",
			artistName:  "ED SHEERAN",
			albumName:   "",
			durationStr: "",
			expected:    "ttml_lyrics:shape of you ed sheeran",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildNormalizedCacheKey(tt.songName, tt.artistName, tt.albumName, tt.durationStr)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestBuildLegacyCacheKey(t *testing.T) {
	tests := []struct {
		name        string
		songName    string
		artistName  string
		albumName   string
		durationStr string
		expected    string
	}{
		{
			name:        "Without album - has trailing space",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "",
			durationStr: "",
			expected:    "ttml_lyrics:Shape of You Ed Sheeran ",
		},
		{
			name:        "With album",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "Divide",
			durationStr: "",
			expected:    "ttml_lyrics:Shape of You Ed Sheeran Divide",
		},
		{
			name:        "Without album, with duration - double space",
			songName:    "Shape of You",
			artistName:  "Ed Sheeran",
			albumName:   "",
			durationStr: "234",
			expected:    "ttml_lyrics:Shape of You Ed Sheeran  234s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildLegacyCacheKey(tt.songName, tt.artistName, tt.albumName, tt.durationStr)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// Tests for fuzzy duration cache matching

func TestGetCachedLyricsWithDurationTolerance_ExactMatch(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Cache a song with duration 232s
	cacheKey := buildNormalizedCacheKey("Shape of You", "Ed Sheeran", "", "232")
	ttml := "<tt>test ttml content</tt>"
	setCachedLyrics(cacheKey, ttml, 232000, 0.95, "en", false)

	// Request with exact duration should find it
	cached, foundKey, found := getCachedLyricsWithDurationTolerance("Shape of You", "Ed Sheeran", "", "232")
	if !found {
		t.Error("Expected to find cached lyrics with exact duration match")
	}
	if foundKey != cacheKey {
		t.Errorf("Expected key %q, got %q", cacheKey, foundKey)
	}
	if cached.TTML != ttml {
		t.Errorf("Expected TTML %q, got %q", ttml, cached.TTML)
	}
}

func TestGetCachedLyricsWithDurationTolerance_FuzzyMatch(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Cache a song with duration 232s
	cacheKey := buildNormalizedCacheKey("Shape of You", "Ed Sheeran", "", "232")
	ttml := "<tt>test ttml content</tt>"
	setCachedLyrics(cacheKey, ttml, 232000, 0.95, "en", false)

	tests := []struct {
		name            string
		requestDuration string
		shouldFind      bool
	}{
		{
			name:            "Request 231s (1s less) - within 2s tolerance",
			requestDuration: "231",
			shouldFind:      true,
		},
		{
			name:            "Request 233s (1s more) - within 2s tolerance",
			requestDuration: "233",
			shouldFind:      true,
		},
		{
			name:            "Request 230s (2s less) - at edge of 2s tolerance",
			requestDuration: "230",
			shouldFind:      true,
		},
		{
			name:            "Request 234s (2s more) - at edge of 2s tolerance",
			requestDuration: "234",
			shouldFind:      true,
		},
		{
			name:            "Request 229s (3s less) - outside 2s tolerance",
			requestDuration: "229",
			shouldFind:      false,
		},
		{
			name:            "Request 235s (3s more) - outside 2s tolerance",
			requestDuration: "235",
			shouldFind:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cached, foundKey, found := getCachedLyricsWithDurationTolerance("Shape of You", "Ed Sheeran", "", tt.requestDuration)

			if found != tt.shouldFind {
				t.Errorf("Expected found=%v, got found=%v", tt.shouldFind, found)
				return
			}

			if tt.shouldFind {
				if cached == nil {
					t.Error("Expected non-nil cached lyrics")
					return
				}
				if cached.TTML != ttml {
					t.Errorf("Expected TTML %q, got %q", ttml, cached.TTML)
				}
				if foundKey != cacheKey {
					t.Errorf("Expected foundKey %q, got %q", cacheKey, foundKey)
				}
			}
		})
	}
}

func TestGetCachedLyricsWithDurationTolerance_ClosestMatch(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Cache songs with durations 230s and 234s
	cacheKey230 := buildNormalizedCacheKey("Test Song", "Test Artist", "", "230")
	cacheKey234 := buildNormalizedCacheKey("Test Song", "Test Artist", "", "234")

	setCachedLyrics(cacheKey230, "<tt>230s version</tt>", 230000, 0.95, "en", false)
	setCachedLyrics(cacheKey234, "<tt>234s version</tt>", 234000, 0.95, "en", false)

	// Request 232s - should find 230s (both are 2s away, but we check lower first)
	// Actually with our implementation, we check in order: 231, 233, 230, 234
	// So for 232, we'd check 231 (miss), 233 (miss), 230 (hit!)
	cached, foundKey, found := getCachedLyricsWithDurationTolerance("Test Song", "Test Artist", "", "232")
	if !found {
		t.Error("Expected to find cached lyrics")
		return
	}
	// Should find 230s since it's checked first at offset 2
	if foundKey != cacheKey230 {
		t.Errorf("Expected to find %q, got %q", cacheKey230, foundKey)
	}
	if cached.TTML != "<tt>230s version</tt>" {
		t.Errorf("Expected 230s version, got %q", cached.TTML)
	}
}

func TestGetCachedLyricsWithDurationTolerance_NoDuration(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Cache a song without duration
	cacheKey := buildNormalizedCacheKey("Shape of You", "Ed Sheeran", "", "")
	ttml := "<tt>test ttml content</tt>"
	setCachedLyrics(cacheKey, ttml, 0, 0.95, "en", false)

	// Request without duration should find it
	cached, foundKey, found := getCachedLyricsWithDurationTolerance("Shape of You", "Ed Sheeran", "", "")
	if !found {
		t.Error("Expected to find cached lyrics without duration")
	}
	if foundKey != cacheKey {
		t.Errorf("Expected key %q, got %q", cacheKey, foundKey)
	}
	if cached.TTML != ttml {
		t.Errorf("Expected TTML %q, got %q", ttml, cached.TTML)
	}
}

func TestGetCachedLyricsWithDurationTolerance_LegacyKeyFallback(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Store with legacy key format (uppercase, trailing space)
	legacyKey := buildLegacyCacheKey("Shape of You", "Ed Sheeran", "", "232")
	ttml := "<tt>legacy format</tt>"

	// Manually store with legacy key
	cachedLyrics := CachedLyrics{
		TTML:            ttml,
		TrackDurationMs: 232000,
		Score:           0.95,
	}
	data, _ := json.Marshal(cachedLyrics)
	persistentCache.Set(legacyKey, string(data))

	// Request with normalized format should find the legacy entry
	cached, foundKey, found := getCachedLyricsWithDurationTolerance("Shape of You", "Ed Sheeran", "", "232")
	if !found {
		t.Error("Expected to find cached lyrics via legacy key fallback")
	}
	if foundKey != legacyKey {
		t.Errorf("Expected legacy key %q, got %q", legacyKey, foundKey)
	}
	if cached.TTML != ttml {
		t.Errorf("Expected TTML %q, got %q", ttml, cached.TTML)
	}
}

func TestGetNegativeCacheWithDurationTolerance_ExactMatch(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Set negative cache for duration 232s
	cacheKey := buildNormalizedCacheKey("Unknown Song", "Unknown Artist", "", "232")
	reason := "no track found"
	setNegativeCache(cacheKey, reason, "", false)

	// Request with exact duration should find it
	foundReason, foundKey, found := getNegativeCacheWithDurationTolerance("Unknown Song", "Unknown Artist", "", "232")
	if !found {
		t.Error("Expected to find negative cache with exact duration match")
	}
	if foundKey != cacheKey {
		t.Errorf("Expected key %q, got %q", cacheKey, foundKey)
	}
	if foundReason != reason {
		t.Errorf("Expected reason %q, got %q", reason, foundReason)
	}
}

func TestGetNegativeCacheWithDurationTolerance_FuzzyMatch(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Set negative cache for duration 232s
	cacheKey := buildNormalizedCacheKey("Unknown Song", "Unknown Artist", "", "232")
	reason := "no track found"
	setNegativeCache(cacheKey, reason, "", false)

	tests := []struct {
		name            string
		requestDuration string
		shouldFind      bool
	}{
		{
			name:            "Request 231s (1s less) - within 2s tolerance",
			requestDuration: "231",
			shouldFind:      true,
		},
		{
			name:            "Request 233s (1s more) - within 2s tolerance",
			requestDuration: "233",
			shouldFind:      true,
		},
		{
			name:            "Request 229s (3s less) - outside 2s tolerance",
			requestDuration: "229",
			shouldFind:      false,
		},
		{
			name:            "Request 235s (3s more) - outside 2s tolerance",
			requestDuration: "235",
			shouldFind:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			foundReason, foundKey, found := getNegativeCacheWithDurationTolerance("Unknown Song", "Unknown Artist", "", tt.requestDuration)

			if found != tt.shouldFind {
				t.Errorf("Expected found=%v, got found=%v", tt.shouldFind, found)
				return
			}

			if tt.shouldFind {
				if foundReason != reason {
					t.Errorf("Expected reason %q, got %q", reason, foundReason)
				}
				if foundKey != cacheKey {
					t.Errorf("Expected foundKey %q, got %q", cacheKey, foundKey)
				}
			}
		})
	}
}

func TestGetNegativeCacheWithDurationTolerance_NoDuration(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Set negative cache without duration
	cacheKey := buildNormalizedCacheKey("Unknown Song", "Unknown Artist", "", "")
	reason := "no track found"
	setNegativeCache(cacheKey, reason, "", false)

	// Request without duration should find it
	foundReason, foundKey, found := getNegativeCacheWithDurationTolerance("Unknown Song", "Unknown Artist", "", "")
	if !found {
		t.Error("Expected to find negative cache without duration")
	}
	if foundKey != cacheKey {
		t.Errorf("Expected key %q, got %q", cacheKey, foundKey)
	}
	if foundReason != reason {
		t.Errorf("Expected reason %q, got %q", reason, foundReason)
	}
}

func TestGetCachedLyricsWithDurationTolerance_ZeroDuration(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Cache a song with duration 2s (edge case near zero)
	cacheKey := buildNormalizedCacheKey("Short Song", "Artist", "", "2")
	ttml := "<tt>short song</tt>"
	setCachedLyrics(cacheKey, ttml, 2000, 0.95, "en", false)

	// Request with 0s should find it (2s is within tolerance)
	cached, _, found := getCachedLyricsWithDurationTolerance("Short Song", "Artist", "", "0")
	if !found {
		t.Error("Expected to find cached lyrics for 0s request when 2s is cached")
	}
	if cached.TTML != ttml {
		t.Errorf("Expected TTML %q, got %q", ttml, cached.TTML)
	}
}

func TestGetCachedLyricsWithDurationTolerance_InvalidDuration(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Cache a song with valid duration
	cacheKey := buildNormalizedCacheKey("Test Song", "Test Artist", "", "232")
	setCachedLyrics(cacheKey, "<tt>test</tt>", 232000, 0.95, "en", false)

	// Request with invalid duration string should not find fuzzy match
	// (only exact match would work, which won't exist for "abc")
	_, _, found := getCachedLyricsWithDurationTolerance("Test Song", "Test Artist", "", "abc")
	if found {
		t.Error("Expected not to find cached lyrics with invalid duration")
	}
}

// Tests for overrideHandler

func TestOverrideHandler_RequiresAPIKey(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	req, _ := http.NewRequest("GET", "/override?id=123&s=song&a=artist", nil)
	// No API key context set — should be denied
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", rr.Code)
	}
}

func TestOverrideHandler_RequiresSongAndArtist(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	tests := []struct {
		name  string
		query string
	}{
		{"missing both", "/override?id=123"},
		{"missing artist", "/override?id=123&s=song"},
		{"missing song", "/override?id=123&a=artist"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tt.query, nil)
			req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
			rr := httptest.NewRecorder()
			overrideHandler(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("Expected 400, got %d", rr.Code)
			}
		})
	}
}

func TestOverrideHandler_RequiresTrackIDUnlessDryRun(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	req, _ := http.NewRequest("GET", "/override?s=song&a=artist", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 when id missing and not dry_run, got %d", rr.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &body)
	if !strings.Contains(body["error"].(string), "id parameter") {
		t.Errorf("Expected error about missing id, got %q", body["error"])
	}
}

func TestOverrideHandler_DryRunFindsMatchingKeys(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Populate cache with entries
	setCachedLyrics("ttml_lyrics:viva la vida coldplay", "<tt>old</tt>", 242000, 0.9, "en", false)
	setCachedLyrics("ttml_lyrics:viva la vida coldplay 242s", "<tt>old with dur</tt>", 242000, 0.9, "en", false)
	setCachedLyrics("ttml_lyrics:other song other artist", "<tt>unrelated</tt>", 200000, 0.8, "en", false)

	// Without duration: only finds the no-duration key
	req, _ := http.NewRequest("GET", "/override?s=viva+la+vida&a=coldplay&dry_run=true", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &body)

	if body["dry_run"] != true {
		t.Error("Expected dry_run=true in response")
	}

	count := int(body["count"].(float64))
	if count != 1 {
		t.Errorf("Expected 1 matching key (no-duration key only), got %d", count)
	}

	// With duration: finds both no-duration and duration keys
	req2, _ := http.NewRequest("GET", "/override?s=viva+la+vida&a=coldplay&d=242&dry_run=true", nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), apiKeyAuthenticatedKey, true))
	rr2 := httptest.NewRecorder()
	overrideHandler(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}

	var body2 map[string]interface{}
	json.Unmarshal(rr2.Body.Bytes(), &body2)

	count2 := int(body2["count"].(float64))
	if count2 != 2 {
		t.Errorf("Expected 2 matching keys (with duration), got %d", count2)
	}
}

func TestOverrideHandler_DryRunWithAlbumFilter(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	setCachedLyrics("ttml_lyrics:viva la vida coldplay", "<tt>no album</tt>", 242000, 0.9, "", false)
	setCachedLyrics("ttml_lyrics:viva la vida coldplay viva la vida or death and all his friends", "<tt>with album</tt>", 242000, 0.9, "", false)

	req, _ := http.NewRequest("GET", "/override?s=viva+la+vida&a=coldplay&al=viva+la+vida+or+death+and+all+his+friends&dry_run=true", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	var body map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &body)

	count := int(body["count"].(float64))
	if count != 1 {
		t.Errorf("Expected 1 matching key with album filter, got %d", count)
	}
}

func TestOverrideHandler_DryRunWithDurationFilter(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	setCachedLyrics("ttml_lyrics:viva la vida coldplay 242s", "<tt>242</tt>", 242000, 0.9, "", false)
	setCachedLyrics("ttml_lyrics:viva la vida coldplay 300s", "<tt>300</tt>", 300000, 0.9, "", false)

	// Duration 243 with default 2s tolerance should match 242s but not 300s
	req, _ := http.NewRequest("GET", "/override?s=viva+la+vida&a=coldplay&d=243&dry_run=true", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	var body map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &body)

	count := int(body["count"].(float64))
	if count != 1 {
		t.Errorf("Expected 1 matching key with duration filter, got %d", count)
	}
}

func TestOverrideHandler_NoMatchCreatesNewEntry(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// We can't call the real FetchLyricsByTrackID without configured accounts,
	// so we verify the handler returns an error about no accounts (proving it
	// attempted to fetch rather than returning 404)
	req, _ := http.NewRequest("GET", "/override?id=123&s=nonexistent&a=nobody", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	// Should attempt to fetch (and fail due to no accounts), not 404
	if rr.Code == http.StatusNotFound {
		t.Error("Should not return 404 when no cache matches — should attempt fetch and create")
	}

	var body map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &body)
	// The error should be about fetching, not about "no matching cache entries"
	if errMsg, ok := body["error"].(string); ok {
		if strings.Contains(errMsg, "no matching cache entries") {
			t.Errorf("Should not return 'no matching cache entries' — got: %s", errMsg)
		}
	}
}

func TestOverrideHandler_RejectsNonNumericTrackID(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	tests := []struct {
		name    string
		trackID string
	}{
		{"path traversal", "../1234"},
		{"query injection", "1234?foo=bar"},
		{"letters", "abc"},
		{"mixed", "123abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/override?id="+tt.trackID+"&s=song&a=artist", nil)
			req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
			rr := httptest.NewRecorder()
			overrideHandler(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("Expected 400 for track ID %q, got %d", tt.trackID, rr.Code)
			}

			var body map[string]interface{}
			json.Unmarshal(rr.Body.Bytes(), &body)
			if !strings.Contains(body["error"].(string), "numeric") {
				t.Errorf("Expected error about numeric ID, got %q", body["error"])
			}
		})
	}
}

func TestOverrideHandler_DryRunDoesNotRequireTrackID(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// dry_run=true should work even without id parameter
	req, _ := http.NewRequest("GET", "/override?s=song&a=artist&dry_run=true", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 for dry_run without id, got %d", rr.Code)
	}
}

func TestOverrideHandler_NoLyricsSetsMarker(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	req, _ := http.NewRequest("GET", "/override?s=instrumental+song&a=some+artist&no_lyrics=true", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &body)

	if body["no_lyrics"] != true {
		t.Error("Expected no_lyrics=true in response")
	}

	// Verify the sentinel was stored in cache
	cacheKey := buildNormalizedCacheKey("instrumental song", "some artist", "", "")
	cached, ok := getCachedLyrics(cacheKey)
	if !ok {
		t.Fatal("Expected cache entry to exist")
	}
	if cached.TTML != NoLyricsSentinel {
		t.Errorf("Expected TTML to be %q, got %q", NoLyricsSentinel, cached.TTML)
	}
}

func TestOverrideHandler_NoLyricsOverwritesExisting(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Pre-populate cache with real lyrics
	setCachedLyrics("ttml_lyrics:my song my artist", "<tt>real lyrics</tt>", 200000, 0.9, "en", false)

	req, _ := http.NewRequest("GET", "/override?s=my+song&a=my+artist&no_lyrics=true", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify the sentinel replaced the real lyrics
	cached, ok := getCachedLyrics("ttml_lyrics:my song my artist")
	if !ok {
		t.Fatal("Expected cache entry to exist")
	}
	if cached.TTML != NoLyricsSentinel {
		t.Errorf("Expected TTML to be %q, got %q", NoLyricsSentinel, cached.TTML)
	}
	// Metadata should be preserved
	if cached.TrackDurationMs != 200000 {
		t.Errorf("Expected duration to be preserved (200000), got %d", cached.TrackDurationMs)
	}
}

func TestOverrideHandler_NoLyricsDoesNotRequireTrackID(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// no_lyrics=true should work without id parameter
	req, _ := http.NewRequest("GET", "/override?s=song&a=artist&no_lyrics=true", nil)
	req = req.WithContext(context.WithValue(req.Context(), apiKeyAuthenticatedKey, true))
	rr := httptest.NewRecorder()
	overrideHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 for no_lyrics without id, got %d", rr.Code)
	}
}

func TestGetLyrics_NoLyricsSentinelReturns404(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Store a no-lyrics sentinel
	cacheKey := buildNormalizedCacheKey("instrumental", "artist", "", "")
	setCachedLyrics(cacheKey, NoLyricsSentinel, 0, 0, "", false)

	req, _ := http.NewRequest("GET", "/getLyrics?s=instrumental&a=artist", nil)
	rr := httptest.NewRecorder()
	getLyrics(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("Expected 404 for no-lyrics sentinel, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &body)
	if !strings.Contains(body["error"].(string), "No lyrics available") {
		t.Errorf("Expected 'No lyrics available' error, got %q", body["error"])
	}
}
