package ttml

import (
	"strings"
	"testing"
)

const songByIDFixture = `{"data":[{"id":"1065973704","type":"songs","attributes":{` +
	`"name":"Breathe (In the Air)","artistName":"Pink Floyd","albumName":"The Dark Side of the Moon",` +
	`"albumArtistName":"Pink Floyd","isrc":"GBN9Y1100077","releaseDate":"1973-03-01",` +
	`"durationInMillis":168800,"genreNames":["Rock"],"composerName":"Roger Waters",` +
	`"artwork":{"url":"https://example/{w}x{h}.jpg","width":1400,"height":1400},` +
	`"hasLyrics":true,"hasTimeSyncedLyrics":true}}]}`

func TestParseTrackByIDResponse(t *testing.T) {
	t.Run("parses full attributes into TrackMeta", func(t *testing.T) {
		meta, err := parseTrackByIDResponse([]byte(songByIDFixture))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.TrackID != "1065973704" {
			t.Errorf("TrackID = %q, want 1065973704", meta.TrackID)
		}
		if meta.ISRC != "GBN9Y1100077" {
			t.Errorf("ISRC = %q, want GBN9Y1100077", meta.ISRC)
		}
		if meta.Name != "Breathe (In the Air)" || meta.ArtistName != "Pink Floyd" || meta.AlbumName != "The Dark Side of the Moon" {
			t.Errorf("name/artist/album mismatch: %q / %q / %q", meta.Name, meta.ArtistName, meta.AlbumName)
		}
		if meta.ReleaseDate != "1973-03-01" {
			t.Errorf("ReleaseDate = %q, want 1973-03-01", meta.ReleaseDate)
		}
		if meta.HasTimeSyncedLyrics == nil || !*meta.HasTimeSyncedLyrics {
			t.Errorf("HasTimeSyncedLyrics = %v, want true", meta.HasTimeSyncedLyrics)
		}
		if meta.Source != SourceApple {
			t.Errorf("Source = %q, want %q", meta.Source, SourceApple)
		}
		for _, key := range []string{"genreNames", "artwork", "composerName", "durationInMillis"} {
			if !strings.Contains(meta.RawAttributes, key) {
				t.Errorf("RawAttributes missing %q: %s", key, meta.RawAttributes)
			}
		}
	})

	t.Run("empty data returns error", func(t *testing.T) {
		if _, err := parseTrackByIDResponse([]byte(`{"data":[]}`)); err == nil {
			t.Error("want error for empty data, got nil")
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		if _, err := parseTrackByIDResponse([]byte(`<html>nope`)); err == nil {
			t.Error("want error for malformed JSON, got nil")
		}
	})
}
