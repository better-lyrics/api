package ttml

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTrackUnmarshalKeepsRawAttributes proves Track.UnmarshalJSON preserves the
// full Apple attributes blob (including fields we do not model, e.g. trackNumber)
// so the complete metadata can be contributed to lrc.red, not a lossy subset.
func TestTrackUnmarshalKeepsRawAttributes(t *testing.T) {
	raw := `{"id":"1488408568","attributes":{"name":"Blinding Lights","artistName":"The Weeknd","isrc":"USUG11904206","trackNumber":9,"discNumber":1,"contentRating":"clean"}}`

	var tr Track
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if tr.Attributes.Name != "Blinding Lights" || tr.Attributes.ISRC != "USUG11904206" {
		t.Errorf("typed view wrong: %+v", tr.Attributes)
	}

	// Fields we do not model must survive in the raw blob.
	var full map[string]any
	if err := json.Unmarshal(tr.RawAttributes, &full); err != nil {
		t.Fatalf("raw attributes not valid JSON: %v", err)
	}
	for _, k := range []string{"trackNumber", "discNumber", "contentRating"} {
		if _, ok := full[k]; !ok {
			t.Errorf("raw attributes dropped %q; got keys %v", k, full)
		}
	}
}

func mkTrack(id, name, artist, album string, durMs int) Track {
	var tr Track
	tr.ID = id
	tr.Attributes.Name = name
	tr.Attributes.ArtistName = artist
	tr.Attributes.AlbumName = album
	tr.Attributes.DurationInMillis = durMs
	return tr
}

func TestSelectBestTrack(t *testing.T) {
	t.Run("empty results", func(t *testing.T) {
		_, _, err := selectBestTrack(nil, "song", "artist", "", 0)
		if err == nil || !strings.Contains(err.Error(), "no tracks found") {
			t.Fatalf("want 'no tracks found', got %v", err)
		}
	})

	t.Run("best match above threshold", func(t *testing.T) {
		tracks := []Track{
			mkTrack("1", "Blinding Lights", "The Weeknd", "After Hours", 200000),
			mkTrack("2", "Save Your Tears", "The Weeknd", "After Hours", 215000),
		}
		got, score, err := selectBestTrack(tracks, "Blinding Lights", "The Weeknd", "", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "1" {
			t.Errorf("picked track %s, want 1", got.ID)
		}
		if score < 0.6 {
			t.Errorf("score %.3f below threshold, expected a strong match", score)
		}
	})

	t.Run("below threshold returns error", func(t *testing.T) {
		tracks := []Track{mkTrack("1", "Blinding Lights", "The Weeknd", "After Hours", 200000)}
		_, _, err := selectBestTrack(tracks, "zzzzzzz", "qqqqqqq", "", 0)
		if err == nil || !strings.Contains(err.Error(), "below threshold") {
			t.Fatalf("want below-threshold error, got %v", err)
		}
	})

	t.Run("duration filter keeps in-delta track", func(t *testing.T) {
		tracks := []Track{
			mkTrack("1", "Blinding Lights", "The Weeknd", "", 300000), // far
			mkTrack("2", "Blinding Lights", "The Weeknd", "", 200500), // within 2s delta
		}
		got, _, err := selectBestTrack(tracks, "Blinding Lights", "The Weeknd", "", 200000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "2" {
			t.Errorf("picked track %s, want 2 (the in-delta one)", got.ID)
		}
	})

	t.Run("duration filter rejects all", func(t *testing.T) {
		tracks := []Track{mkTrack("1", "Blinding Lights", "The Weeknd", "", 500000)}
		_, _, err := selectBestTrack(tracks, "Blinding Lights", "The Weeknd", "", 200000)
		if err == nil || !strings.Contains(err.Error(), "no tracks within") {
			t.Fatalf("want duration-miss error, got %v", err)
		}
	})

	t.Run("no criteria falls back to first result", func(t *testing.T) {
		tracks := []Track{
			mkTrack("first", "Whatever", "Nobody", "", 123000),
			mkTrack("second", "Other", "Nobody", "", 124000),
		}
		got, score, err := selectBestTrack(tracks, "", "", "", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "first" || score != 1.0 {
			t.Errorf("fallback picked %s score %.3f, want first/1.0", got.ID, score)
		}
	})
}
