package store

import (
	"regexp"
	"slices"
	"testing"
)

var cacheTagRe = regexp.MustCompile(`^[sk]-[0-9a-f]{20}$`)

func TestSongCacheTag(t *testing.T) {
	base := SongCacheTag("Hass Und Liebe", "Miss Construction")
	if !cacheTagRe.MatchString(base) || base[0] != 's' {
		t.Fatalf("tag %q does not match the song tag format", base)
	}

	t.Run("ignores case and surrounding or repeated whitespace", func(t *testing.T) {
		if got := SongCacheTag("  hass und  LIEBE ", "miss\tconstruction"); got != base {
			t.Fatalf("got %q, want %q", got, base)
		}
	})

	t.Run("differs per song", func(t *testing.T) {
		if got := SongCacheTag("Other Song", "Miss Construction"); got == base {
			t.Fatalf("different songs share tag %q", got)
		}
	})
}

func TestSongCacheTags(t *testing.T) {
	tags := SongCacheTags("APT.", "ROSÉ, Bruno Mars", "APT.")

	t.Run("carries the song tag and the key tag", func(t *testing.T) {
		if len(tags) != 2 {
			t.Fatalf("got %d tags, want 2: %v", len(tags), tags)
		}
		if !slices.Contains(tags, SongCacheTag("APT.", "ROSÉ, Bruno Mars")) {
			t.Fatalf("tags %v miss the song tag", tags)
		}
	})

	t.Run("unicode input yields printable ASCII tags without spaces", func(t *testing.T) {
		for _, tag := range tags {
			if !cacheTagRe.MatchString(tag) {
				t.Fatalf("tag %q is not a hashed ASCII tag", tag)
			}
		}
	})

	t.Run("empty album still yields a stable key tag", func(t *testing.T) {
		a := SongCacheTags("Song", "Artist", "")
		b := SongCacheTags("song", "ARTIST", "  ")
		if !slices.Equal(a, b) {
			t.Fatalf("got %v and %v", a, b)
		}
	})
}

func TestKeyCacheTag(t *testing.T) {
	headerTags := SongCacheTags("Blinding Lights", "The Weeknd", "After Hours")

	tests := []struct {
		name     string
		cacheKey string
	}{
		{"normalized key", "ttml_lyrics:blinding lights the weeknd after hours"},
		{"normalized key with duration", "ttml_lyrics:blinding lights the weeknd after hours 200s"},
		{"legacy key keeps case", "ttml_lyrics:Blinding Lights The Weeknd After Hours"},
		{"legacy key with duration", "ttml_lyrics:Blinding Lights The Weeknd After Hours 200s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := KeyCacheTag(tt.cacheKey)
			if !slices.Contains(headerTags, got) {
				t.Fatalf("KeyCacheTag(%q) = %q, not among response tags %v", tt.cacheKey, got, headerTags)
			}
		})
	}

	t.Run("regression: legacy key with empty album matches the response tag", func(t *testing.T) {
		got := KeyCacheTag("ttml_lyrics:Song Artist ")
		if !slices.Contains(SongCacheTags("Song", "Artist", ""), got) {
			t.Fatalf("legacy empty-album key tag %q does not match", got)
		}
	})

	t.Run("different album gives a different key tag", func(t *testing.T) {
		if KeyCacheTag("ttml_lyrics:song artist one") == KeyCacheTag("ttml_lyrics:song artist two") {
			t.Fatal("albums share a key tag")
		}
	})
}
