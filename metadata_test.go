package main

import (
	"encoding/json"
	"lyrics-api-go/cache"
	"net/http"
	"net/http/httptest"
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

	// Neutralize auth config for tests. A non-empty CACHE_ACCESS_TOKEN loaded
	// from the project's real .env would otherwise make every handler test 401.
	origToken := conf.Configuration.CacheAccessToken
	conf.Configuration.CacheAccessToken = ""

	return func() {
		conf.Configuration.CacheAccessToken = origToken
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
				ReleaseDate:              time.Now().UTC().Format("2006-01-02"),
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
				ReleaseDate:              time.Now().UTC().Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 6 * 60 * 60,
		},
		{
			name: "6 hour TTL for song released 2 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 6 * 60 * 60,
		},
		{
			name: "12 hour TTL for song released 5 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().UTC().AddDate(0, 0, -5).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 12 * 60 * 60,
		},
		{
			name: "1 day TTL for song released 10 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 24 * 60 * 60,
		},
		{
			name: "3 day TTL for song released 20 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().UTC().AddDate(0, 0, -20).Format("2006-01-02"),
				HasTimeSyncedLyricsKnown: true,
			},
			expected: 3 * 24 * 60 * 60,
		},
		{
			name: "default TTL for song released 60 days ago",
			entry: NegativeCacheEntry{
				Reason:                   "no lyrics data found",
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02"),
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
				ReleaseDate:              time.Now().UTC().AddDate(0, 0, -tt.daysAgo).Format("2006-01-02"),
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

func TestEnrichMetadata_ParsesRawAttributes(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	meta := &SongMetadata{
		CacheKey:      "ttml_lyrics:test song test artist",
		TrackName:     "Test Song",
		ArtistName:    "Test Artist",
		RawAttributes: `{"name":"Test Song","artistName":"Test Artist","artwork":{"url":"https://example.com/img.jpg"}}`,
	}

	enriched := enrichMetadata(meta)
	if enriched == nil {
		t.Fatal("enrichMetadata returned nil")
	}

	// rawAttributes should be a parsed object, not an escaped string
	attrs, ok := enriched["rawAttributes"].(map[string]interface{})
	if !ok {
		t.Fatalf("rawAttributes should be a map, got %T: %v", enriched["rawAttributes"], enriched["rawAttributes"])
	}
	if attrs["name"] != "Test Song" {
		t.Errorf("expected rawAttributes.name == 'Test Song', got %v", attrs["name"])
	}
	artwork, ok := attrs["artwork"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested artwork object, got %T", attrs["artwork"])
	}
	if artwork["url"] != "https://example.com/img.jpg" {
		t.Errorf("expected artwork.url preserved, got %v", artwork["url"])
	}
}

func TestEnrichMetadata_InvalidRawAttributesKeepsString(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	meta := &SongMetadata{
		CacheKey:      "ttml_lyrics:broken",
		TrackName:     "Broken",
		ArtistName:    "Artist",
		RawAttributes: `{invalid json`,
	}

	enriched := enrichMetadata(meta)
	// On parse failure we leave the raw string in place (fail-open, never drop data)
	if s, ok := enriched["rawAttributes"].(string); !ok || s != `{invalid json` {
		t.Errorf("invalid RawAttributes should be preserved as string, got %T: %v", enriched["rawAttributes"], enriched["rawAttributes"])
	}
}

func TestEnrichMetadata_LyricsCacheStatus(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	// Case 1: no lyrics cached
	metaA := &SongMetadata{
		CacheKey:   "ttml_lyrics:no cache test",
		TrackName:  "Nope",
		ArtistName: "Artist",
	}
	enrichedA := enrichMetadata(metaA)
	lyricsA, ok := enrichedA["lyrics"].(map[string]interface{})
	if !ok {
		t.Fatalf("lyrics field missing/wrong type: %T", enrichedA["lyrics"])
	}
	if lyricsA["cached"] != false {
		t.Errorf("expected cached=false when no lyrics stored, got %v", lyricsA["cached"])
	}

	// Case 2: real lyrics cached
	metaB := &SongMetadata{
		CacheKey:   "ttml_lyrics:has lyrics",
		TrackName:  "Has",
		ArtistName: "Artist",
	}
	setCachedLyrics(metaB.CacheKey, "<tt>some ttml</tt>", 240000, 0.95, "en", false)
	enrichedB := enrichMetadata(metaB)
	lyricsB := enrichedB["lyrics"].(map[string]interface{})
	if lyricsB["cached"] != true {
		t.Errorf("expected cached=true, got %v", lyricsB["cached"])
	}
	if lyricsB["noLyrics"] != false {
		t.Errorf("expected noLyrics=false, got %v", lyricsB["noLyrics"])
	}
	if lyricsB["ttmlBytes"] != float64(len("<tt>some ttml</tt>")) && lyricsB["ttmlBytes"] != len("<tt>some ttml</tt>") {
		// map[string]interface{} may hold int or float64 depending on how it was added;
		// enrichMetadata adds len() which is int
		t.Errorf("unexpected ttmlBytes: %v (type %T)", lyricsB["ttmlBytes"], lyricsB["ttmlBytes"])
	}
	if lyricsB["language"] != "en" {
		t.Errorf("expected language=en, got %v", lyricsB["language"])
	}

	// Case 3: no-lyrics sentinel
	metaC := &SongMetadata{
		CacheKey:   "ttml_lyrics:sentinel",
		TrackName:  "Sentinel",
		ArtistName: "Artist",
	}
	setCachedLyrics(metaC.CacheKey, NoLyricsSentinel, 0, 0, "", false)
	enrichedC := enrichMetadata(metaC)
	lyricsC := enrichedC["lyrics"].(map[string]interface{})
	if lyricsC["cached"] != true {
		t.Errorf("expected cached=true for sentinel, got %v", lyricsC["cached"])
	}
	if lyricsC["noLyrics"] != true {
		t.Errorf("expected noLyrics=true for sentinel, got %v", lyricsC["noLyrics"])
	}
}

func TestEnrichMetadata_NilInput(t *testing.T) {
	if enrichMetadata(nil) != nil {
		t.Error("enrichMetadata(nil) should return nil")
	}
}

func TestMetadataLookupHandler_ISRC(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	// Populate two metadata entries with the same ISRC (different duration variants)
	isrc := "USUM72401994"
	meta1 := &SongMetadata{
		CacheKey:      "ttml_lyrics:song artist [240s]",
		TrackName:     "Song",
		ArtistName:    "Artist",
		ISRC:          isrc,
		RawAttributes: `{"name":"Song","artistName":"Artist","genreNames":["Pop"]}`,
	}
	meta2 := &SongMetadata{
		CacheKey:   "ttml_lyrics:song artist [241s]",
		TrackName:  "Song",
		ArtistName: "Artist",
		ISRC:       isrc,
	}
	setSongMetadata(meta1)
	setSongMetadata(meta2)
	setCachedLyrics(meta1.CacheKey, "<tt>real lyrics</tt>", 240000, 0.9, "en", false)

	// Call the handler directly
	req := httptest.NewRequest(http.MethodGet, "/metadata?isrc="+isrc, nil)
	rec := httptest.NewRecorder()
	metadataLookupHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ISRC    string                   `json:"isrc"`
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v — body: %s", err, rec.Body.String())
	}
	if resp.ISRC != isrc {
		t.Errorf("expected isrc %q, got %q", isrc, resp.ISRC)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results for ISRC, got %d", len(resp.Results))
	}

	// Find the entry with parsed RawAttributes and verify enrichment
	var withAttrs map[string]interface{}
	for _, r := range resp.Results {
		if r["cacheKey"] == meta1.CacheKey {
			withAttrs = r
			break
		}
	}
	if withAttrs == nil {
		t.Fatal("did not find meta1 in results")
	}
	if attrs, ok := withAttrs["rawAttributes"].(map[string]interface{}); !ok {
		t.Errorf("rawAttributes should be a parsed object, got %T", withAttrs["rawAttributes"])
	} else if attrs["name"] != "Song" {
		t.Errorf("rawAttributes.name = %v, want Song", attrs["name"])
	}
	lyrics, _ := withAttrs["lyrics"].(map[string]interface{})
	if lyrics["cached"] != true {
		t.Errorf("expected meta1 lyrics.cached=true, got %v", lyrics["cached"])
	}
}

func TestMetadataLookupHandler_ISRCNotFound(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/metadata?isrc=DOES_NOT_EXIST", nil)
	rec := httptest.NewRecorder()
	metadataLookupHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown ISRC, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestMetadataLookupHandler_BadRequestMentionsISRC(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/metadata", nil)
	rec := httptest.NewRecorder()
	metadataLookupHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	// Error message should now reference isrc as an option
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errMsg, _ := body["error"].(string)
	if errMsg == "" || !containsAll(errMsg, []string{"isrc"}) {
		t.Errorf("error message should mention isrc, got %q", errMsg)
	}
}

func TestMetadataStatsHandler_Empty(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	origToken := conf.Configuration.CacheAccessToken
	conf.Configuration.CacheAccessToken = "test-token"
	defer func() { conf.Configuration.CacheAccessToken = origToken }()

	req := httptest.NewRequest(http.MethodGet, "/metadata/stats", nil)
	req.Header.Set("Authorization", "test-token")
	rec := httptest.NewRecorder()
	metadataStatsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Metadata map[string]interface{} `json:"metadata"`
		Indexes  map[string]interface{} `json:"indexes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Metadata["totalEntries"].(float64) != 0 {
		t.Errorf("expected 0 metadata entries, got %v", resp.Metadata["totalEntries"])
	}
	if resp.Indexes["totalEntries"].(float64) != 0 {
		t.Errorf("expected 0 index entries, got %v", resp.Indexes["totalEntries"])
	}
}

func TestMetadataStatsHandler_WithEntries(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	origToken := conf.Configuration.CacheAccessToken
	conf.Configuration.CacheAccessToken = "test-token"
	defer func() { conf.Configuration.CacheAccessToken = origToken }()

	setSongMetadata(&SongMetadata{
		CacheKey:      "ttml_lyrics:rich song artist",
		TrackName:     "Rich",
		ArtistName:    "Artist",
		ISRC:          "US1234567890",
		VideoIDs:      []string{"vidA"},
		RawAttributes: `{"name":"Rich","artwork":{"url":"x"}}`,
	})
	setSongMetadata(&SongMetadata{
		CacheKey:   "ttml_lyrics:thin song artist",
		TrackName:  "Thin",
		ArtistName: "Artist",
		VideoIDs:   []string{"vidB"},
	})
	setSongMetadata(&SongMetadata{
		CacheKey:   "ttml_lyrics:bare song artist",
		TrackName:  "Bare",
		ArtistName: "Artist",
	})

	req := httptest.NewRequest(http.MethodGet, "/metadata/stats", nil)
	req.Header.Set("Authorization", "test-token")
	rec := httptest.NewRecorder()
	metadataStatsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Metadata map[string]interface{} `json:"metadata"`
		Indexes  map[string]interface{} `json:"indexes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if int(resp.Metadata["totalEntries"].(float64)) != 3 {
		t.Errorf("totalEntries = %v, want 3", resp.Metadata["totalEntries"])
	}
	if int(resp.Metadata["withVideoIds"].(float64)) != 2 {
		t.Errorf("withVideoIds = %v, want 2", resp.Metadata["withVideoIds"])
	}
	if int(resp.Metadata["withISRC"].(float64)) != 1 {
		t.Errorf("withISRC = %v, want 1", resp.Metadata["withISRC"])
	}
	if int(resp.Metadata["withRawAttributes"].(float64)) != 1 {
		t.Errorf("withRawAttributes = %v, want 1", resp.Metadata["withRawAttributes"])
	}
	if int(resp.Metadata["withArtwork"].(float64)) != 1 {
		t.Errorf("withArtwork = %v, want 1", resp.Metadata["withArtwork"])
	}
	if resp.Metadata["richStatsComplete"].(bool) != true {
		t.Errorf("richStatsComplete should be true when parsedEntries < cap")
	}

	byPrefix := resp.Indexes["byPrefix"].(map[string]interface{})
	if int(byPrefix["video:"].(float64)) != 2 {
		t.Errorf("video: prefix count = %v, want 2", byPrefix["video:"])
	}
	if int(byPrefix["isrc:"].(float64)) != 1 {
		t.Errorf("isrc: prefix count = %v, want 1", byPrefix["isrc:"])
	}
}

func TestMetadataStatsHandler_Unauthorized(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	origToken := conf.Configuration.CacheAccessToken
	conf.Configuration.CacheAccessToken = "test-token"
	defer func() { conf.Configuration.CacheAccessToken = origToken }()

	req := httptest.NewRequest(http.MethodGet, "/metadata/stats", nil)
	req.Header.Set("Authorization", "bad-token")
	rec := httptest.NewRecorder()
	metadataStatsHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/metadata/stats", nil)
	rec = httptest.NewRecorder()
	metadataStatsHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no token, got %d", rec.Code)
	}
}

func TestMetadataSampleHandler(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	origToken := conf.Configuration.CacheAccessToken
	conf.Configuration.CacheAccessToken = "test-token"
	defer func() { conf.Configuration.CacheAccessToken = origToken }()

	for i := 0; i < 5; i++ {
		setSongMetadata(&SongMetadata{
			CacheKey:   "ttml_lyrics:song" + string(rune('0'+i)) + " artist",
			TrackName:  "Song" + string(rune('0'+i)),
			ArtistName: "Artist",
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/metadata/sample?n=3", nil)
	req.Header.Set("Authorization", "test-token")
	rec := httptest.NewRecorder()
	metadataSampleHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Bucket    string                   `json:"bucket"`
		Requested int                      `json:"requested"`
		Returned  int                      `json:"returned"`
		Entries   []map[string]interface{} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Bucket != "metadata" {
		t.Errorf("bucket = %q, want metadata", resp.Bucket)
	}
	if resp.Returned != 3 {
		t.Errorf("returned = %d, want 3", resp.Returned)
	}
	if len(resp.Entries) != 3 {
		t.Errorf("entries len = %d, want 3", len(resp.Entries))
	}
	for i, e := range resp.Entries {
		if _, ok := e["lyrics"].(map[string]interface{}); !ok {
			t.Errorf("entry %d missing lyrics sub-object", i)
		}
	}
}

func TestMetadataSampleHandler_CapsAtMax(t *testing.T) {
	cleanup := setupTestMetadata(t)
	defer cleanup()

	origToken := conf.Configuration.CacheAccessToken
	conf.Configuration.CacheAccessToken = "test-token"
	defer func() { conf.Configuration.CacheAccessToken = origToken }()

	req := httptest.NewRequest(http.MethodGet, "/metadata/sample?n=99999", nil)
	req.Header.Set("Authorization", "test-token")
	rec := httptest.NewRecorder()
	metadataSampleHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Requested int `json:"requested"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Requested != metadataSampleMaxN {
		t.Errorf("requested = %d, want cap %d", resp.Requested, metadataSampleMaxN)
	}
}

func containsAll(s string, substrs []string) bool {
	for _, sub := range substrs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
