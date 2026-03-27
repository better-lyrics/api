package main

import (
	"lyrics-api-go/cache"
	"os"
	"testing"
	"time"
)

func setupTestMetadata(t *testing.T) func() {
	t.Helper()
	tmpFile := t.TempDir() + "/test_metadata.db"
	var err error
	persistentCache, err = cache.NewPersistentCache(tmpFile, t.TempDir(), false)
	if err != nil {
		t.Fatalf("Failed to create test cache: %v", err)
	}
	initMetadataBuckets()
	return func() {
		persistentCache.Close()
		os.Remove(tmpFile)
	}
}

func TestGetNegativeCacheTTLSeconds(t *testing.T) {
	tests := []struct {
		name     string
		entry    NegativeCacheEntry
		expected int64
	}{
		{
			name: "default TTL when hasTimeSyncedLyricsKnown is false",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: false,
			},
			expected: int64(conf.Configuration.NegativeCacheTTLInDays * 24 * 60 * 60),
		},
		{
			name: "default TTL when releaseDate is empty",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              "",
				HasTimeSyncedLyricsKnown: true,
			},
			expected: int64(conf.Configuration.NegativeCacheTTLInDays * 24 * 60 * 60),
		},
		{
			name: "6 hour TTL for song released today",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 6 * 60 * 60,
		},
		{
			name: "6 hour TTL for song released 2 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().AddDate(0, 0, -2).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 6 * 60 * 60,
		},
		{
			name: "12 hour TTL for song released 5 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().AddDate(0, 0, -5).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 12 * 60 * 60,
		},
		{
			name: "1 day TTL for song released 10 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().AddDate(0, 0, -10).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 24 * 60 * 60,
		},
		{
			name: "3 day TTL for song released 20 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().AddDate(0, 0, -20).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 3 * 24 * 60 * 60,
		},
		{
			name: "default TTL for song released 60 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().AddDate(0, 0, -60).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: int64(conf.Configuration.NegativeCacheTTLInDays * 24 * 60 * 60),
		},
		{
			name: "default TTL for invalid releaseDate",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              "not-a-date",
				HasTimeSyncedLyricsKnown: true,
			},
			expected: int64(conf.Configuration.NegativeCacheTTLInDays * 24 * 60 * 60),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getNegativeCacheTTLSeconds(tt.entry)
			if got != tt.expected {
				t.Errorf("getNegativeCacheTTLSeconds() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestMetadataStoreCRUD(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	// Test setSongMetadata and getSongMetadata
	meta := &SongMetadata{
		CacheKey:     "ttml_lyrics:test song test artist",
		AppleTrackID: "12345",
		ISRC:         "US1234567890",
		TrackName:    "Test Song",
		ArtistName:   "Test Artist",
		AlbumName:    "Test Album",
		DurationMs:   240000,
		ReleaseDate:  "2024-01-01",
	}

	setSongMetadata(meta)

	got, ok := getSongMetadata(meta.CacheKey)
	if !ok {
		t.Fatal("Expected to find metadata after set")
	}
	if got.TrackName != "Test Song" {
		t.Errorf("TrackName = %q, want %q", got.TrackName, "Test Song")
	}
	if got.ISRC != "US1234567890" {
		t.Errorf("ISRC = %q, want %q", got.ISRC, "US1234567890")
	}
	if got.FirstSeen == 0 {
		t.Error("FirstSeen should be set")
	}
}

func TestAddVideoID(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	cacheKey := "ttml_lyrics:test song test artist"

	// Add first videoId
	addVideoID(cacheKey, "video1")
	vids := getVideoIDs(cacheKey)
	if len(vids) != 1 || vids[0] != "video1" {
		t.Errorf("Expected [video1], got %v", vids)
	}

	// Add second videoId
	addVideoID(cacheKey, "video2")
	vids = getVideoIDs(cacheKey)
	if len(vids) != 2 {
		t.Errorf("Expected 2 videoIds, got %d", len(vids))
	}

	// Add duplicate - should not increase count
	addVideoID(cacheKey, "video1")
	vids = getVideoIDs(cacheKey)
	if len(vids) != 2 {
		t.Errorf("Expected 2 videoIds after duplicate, got %d", len(vids))
	}
}

func TestVideoIDReverseIndex(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	cacheKey1 := "ttml_lyrics:song artist [240s]"
	cacheKey2 := "ttml_lyrics:song artist [241s]"

	// Same videoId, different cache keys (different durations)
	addVideoID(cacheKey1, "video1")
	addVideoID(cacheKey2, "video1")

	keys := getCacheKeysByVideoID("video1")
	if len(keys) != 2 {
		t.Errorf("Expected 2 cache keys for video1, got %d", len(keys))
	}
}

func TestMetadataPreservesFirstSeen(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	meta := &SongMetadata{
		CacheKey:   "ttml_lyrics:test song test artist",
		TrackName:  "Test Song",
		ArtistName: "Test Artist",
	}
	setSongMetadata(meta)

	got1, _ := getSongMetadata(meta.CacheKey)
	firstSeen := got1.FirstSeen

	// Update with new data
	meta.AlbumName = "Updated Album"
	setSongMetadata(meta)

	got2, _ := getSongMetadata(meta.CacheKey)
	if got2.FirstSeen != firstSeen {
		t.Errorf("FirstSeen changed from %d to %d on update", firstSeen, got2.FirstSeen)
	}
	if got2.AlbumName != "Updated Album" {
		t.Errorf("AlbumName not updated, got %q", got2.AlbumName)
	}
	if got2.LastUpdated < firstSeen {
		t.Error("LastUpdated should be >= FirstSeen after update")
	}
}

func TestMetadataMergesVideoIDs(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	// First set with video1
	meta1 := &SongMetadata{
		CacheKey:   "ttml_lyrics:test",
		TrackName:  "Test",
		ArtistName: "Artist",
		VideoIDs:   []string{"video1"},
	}
	setSongMetadata(meta1)

	// Second set with video2 — should merge, not replace
	meta2 := &SongMetadata{
		CacheKey:   "ttml_lyrics:test",
		TrackName:  "Test",
		ArtistName: "Artist",
		VideoIDs:   []string{"video2"},
	}
	setSongMetadata(meta2)

	got, _ := getSongMetadata("ttml_lyrics:test")
	if len(got.VideoIDs) != 2 {
		t.Errorf("Expected 2 merged videoIds, got %d: %v", len(got.VideoIDs), got.VideoIDs)
	}
}

func TestISRCIndex(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	meta := &SongMetadata{
		CacheKey:   "ttml_lyrics:song artist",
		TrackName:  "Song",
		ArtistName: "Artist",
		ISRC:       "USUM72401994",
	}
	setSongMetadata(meta)

	keys := getIndex("isrc:USUM72401994")
	if len(keys) != 1 || keys[0] != "ttml_lyrics:song artist" {
		t.Errorf("ISRC index lookup failed, got %v", keys)
	}
}

func TestAddVideoIDWithEmptyInputs(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	// Empty cacheKey — should be no-op
	addVideoID("", "video1")
	// Empty videoID — should be no-op
	addVideoID("ttml_lyrics:test", "")

	// Neither should have created any data
	_, ok := getSongMetadata("")
	if ok {
		t.Error("Empty cacheKey should not create metadata")
	}
	vids := getVideoIDs("ttml_lyrics:test")
	if len(vids) != 0 {
		t.Errorf("Empty videoId should not be stored, got %v", vids)
	}
}

func TestGetNegativeCacheTTLBoundaryDays(t *testing.T) {
	// Test the exact boundary days between tiers
	tests := []struct {
		name     string
		daysAgo  int
		expected int64
	}{
		{"day 0 (today)", 0, 6 * 60 * 60},
		{"day 3 (boundary)", 3, 6 * 60 * 60},
		{"day 4 (into 12h tier)", 4, 12 * 60 * 60},
		{"day 7 (boundary)", 7, 12 * 60 * 60},
		{"day 8 (into 1d tier)", 8, 24 * 60 * 60},
		{"day 14 (boundary)", 14, 24 * 60 * 60},
		{"day 15 (into 3d tier)", 15, 3 * 24 * 60 * 60},
		{"day 29 (last day in threshold)", 29, 3 * 24 * 60 * 60},
		{"day 30 (at threshold)", 30, int64(conf.Configuration.NegativeCacheTTLInDays * 24 * 60 * 60)},
		{"day 31 (past threshold)", 31, int64(conf.Configuration.NegativeCacheTTLInDays * 24 * 60 * 60)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().AddDate(0, 0, -tt.daysAgo).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			}
			got := getNegativeCacheTTLSeconds(entry)
			if got != tt.expected {
				t.Errorf("day %d: getNegativeCacheTTLSeconds() = %d, want %d", tt.daysAgo, got, tt.expected)
			}
		})
	}
}

func TestGetAllVideoIDsForSong(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	// Create metadata for two duration variants of the same song
	meta1 := &SongMetadata{
		CacheKey:   "ttml_lyrics:song artist [240s]",
		TrackName:  "Song",
		ArtistName: "Artist",
		VideoIDs:   []string{"video1"},
	}
	meta2 := &SongMetadata{
		CacheKey:   "ttml_lyrics:song artist [241s]",
		TrackName:  "Song",
		ArtistName: "Artist",
		VideoIDs:   []string{"video2"},
	}

	setSongMetadata(meta1)
	setSongMetadata(meta2)

	// Should find both videoIds across duration variants
	allVids := getAllVideoIDsForSong("Song", "Artist")
	if len(allVids) != 2 {
		t.Errorf("Expected 2 videoIds across variants, got %d: %v", len(allVids), allVids)
	}
}
