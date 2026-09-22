package main

import (
	"encoding/json"
	"testing"

	"lyrics-api-go/cache"
	"lyrics-api-go/utils"
)

func encodeCacheEntry(t *testing.T, plainValue string) []byte {
	t.Helper()
	compressed, err := utils.CompressString(plainValue)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	raw, err := json.Marshal(cache.CacheEntry{Value: compressed})
	if err != nil {
		t.Fatalf("marshal cache entry: %v", err)
	}
	return raw
}

func TestDecodeCacheEntryValue(t *testing.T) {
	raw := encodeCacheEntry(t, "hello world")
	got, err := decodeCacheEntryValue(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestDecodeCacheEntryValue_MalformedJSON(t *testing.T) {
	if _, err := decodeCacheEntryValue([]byte("not json")); err == nil {
		t.Error("expected error for malformed json, got nil")
	}
}

func TestDecodeCacheEntryValue_MalformedCompression(t *testing.T) {
	raw, err := json.Marshal(cache.CacheEntry{Value: "not-base64-gzip!!"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := decodeCacheEntryValue(raw); err == nil {
		t.Error("expected error for malformed compression, got nil")
	}
}

func TestDecodePositiveLyrics_JSONFormat(t *testing.T) {
	cl := cachedLyrics{TTML: "<tt>hi</tt>", TrackDurationMs: 1000, Score: 0.5, Language: "en", IsRTL: false}
	data, err := json.Marshal(cl)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := decodePositiveLyrics(string(data))
	if got != cl {
		t.Errorf("got %+v, want %+v", got, cl)
	}
}

func TestDecodePositiveLyrics_PlainFormatFallback(t *testing.T) {
	got := decodePositiveLyrics("<tt>plain old ttml</tt>")
	want := cachedLyrics{TTML: "<tt>plain old ttml</tt>"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDecodePositiveLyrics_JSONWithEmptyTTMLFallsBackToRaw(t *testing.T) {
	// mirrors cache_helpers.go getCachedLyrics: empty TTML field falls back to raw string.
	raw := `{"ttml":"","trackDurationMs":100}`
	got := decodePositiveLyrics(raw)
	want := cachedLyrics{TTML: raw}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDecodePositiveLyrics_Sentinel(t *testing.T) {
	got := decodePositiveLyrics(noLyricsSentinel)
	if got.TTML != noLyricsSentinel {
		t.Errorf("got TTML %q, want sentinel %q", got.TTML, noLyricsSentinel)
	}
}

func TestDecodeNegativeEntry(t *testing.T) {
	entry := negativeCacheEntry{Reason: "no lyrics data found", Timestamp: 1700000000, ReleaseDate: "2024-01-01", HasTimeSyncedLyricsKnown: true}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := decodeNegativeEntry(string(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != entry {
		t.Errorf("got %+v, want %+v", got, entry)
	}
}

func TestDecodeNegativeEntry_Malformed(t *testing.T) {
	if _, err := decodeNegativeEntry("not json"); err == nil {
		t.Error("expected error for malformed negative entry, got nil")
	}
}

func TestDecodeMetadataValue(t *testing.T) {
	meta := songMetadata{
		CacheKey:      "ttml_lyrics:song artist",
		VideoIDs:      []string{"vid1", "vid2"},
		AppleTrackID:  "12345",
		ISRC:          "USABC1234567",
		TrackName:     "Song",
		ArtistName:    "Artist",
		AlbumName:     "Album",
		DurationMs:    200000,
		ReleaseDate:   "2024-01-01",
		RawAttributes: `{"foo":"bar"}`,
		FirstSeen:     1700000000,
		LastUpdated:   1700000100,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	compressed, err := utils.CompressString(string(data))
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	got, err := decodeMetadataValue([]byte(compressed))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.CacheKey != meta.CacheKey || got.TrackName != meta.TrackName || len(got.VideoIDs) != 2 {
		t.Errorf("got %+v, want %+v", got, meta)
	}
}

func TestDecodeMetadataValue_Malformed(t *testing.T) {
	if _, err := decodeMetadataValue([]byte("not compressed")); err == nil {
		t.Error("expected error for malformed metadata bytes, got nil")
	}
}
