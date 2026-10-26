package httpapi

import (
	"fmt"
	"strings"
	"testing"

	"lyrics-api-go/internal/store"
	"lyrics-api-go/services/providers"
)

func TestBuildNormalizedCacheKey(t *testing.T) {
	tests := []struct {
		name                             string
		song, artist, album, durationStr string
		want                             string
	}{
		{"lowercases and trims", "  Blinding Lights ", "The Weeknd", "", "", "ttml_lyrics:blinding lights the weeknd"},
		{"with album", "Song", "Artist", "Album", "", "ttml_lyrics:song artist album"},
		{"with duration", "Song", "Artist", "", "200", "ttml_lyrics:song artist 200s"},
		{"album and duration", "Song", "Artist", "Album", "200", "ttml_lyrics:song artist album 200s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildNormalizedCacheKey(tt.song, tt.artist, tt.album, tt.durationStr); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildLegacyCacheKey_PreservesCaseAndEmptyAlbumSpacing(t *testing.T) {
	// The legacy key is a frozen contract: it does NOT lowercase and always joins
	// the album slot, so an empty album leaves a double space. Cache lookups depend
	// on reproducing this exactly.
	if got := buildLegacyCacheKey("Song", "Artist", "", ""); got != "ttml_lyrics:Song Artist " {
		t.Fatalf("got %q, want %q", got, "ttml_lyrics:Song Artist ")
	}
	if got := buildLegacyCacheKey("Song", "Artist", "Album", "200"); got != "ttml_lyrics:Song Artist Album 200s" {
		t.Fatalf("got %q, want %q", got, "ttml_lyrics:Song Artist Album 200s")
	}
}

func TestBuildProviderCacheKey(t *testing.T) {
	tests := []struct {
		name                             string
		prefix, song, artist, album, dur string
		want                             string
	}{
		{"no album no duration", "kugou", " Song ", "Artist", "", "", "kugou:song artist"},
		{"bracketed album", "kugou", "Song", "Artist", "Album", "", "kugou:song artist [album]"},
		{"bracketed duration", "kugou", "Song", "Artist", "", "200", "kugou:song artist [200s]"},
		{"album and duration", "kugou", "Song", "Artist", "Album", "200", "kugou:song artist [album] [200s]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildProviderCacheKey(tt.prefix, tt.song, tt.artist, tt.album, tt.dur); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildProviderSongArtistKey(t *testing.T) {
	tests := []struct {
		name                 string
		prefix, song, artist string
		want                 string
	}{
		{"lowercases and trims", "qq_lyrics", " Projections ", " Night Tapes ", "qq_lyrics:projections night tapes"},
		{"no artist keeps the separator", "qq_lyrics", "Song", "", "qq_lyrics:song "},
		{"unicode", "kugou_lyrics", "À Peu Près", "Pomme", "kugou_lyrics:à peu près pomme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildProviderSongArtistKey(tt.prefix, tt.song, tt.artist); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderCacheKeysExtendTheirSongArtistKey(t *testing.T) {
	for _, artist := range []string{"Artist", ""} {
		for _, album := range []string{"", "Album", "Album [Deluxe]"} {
			for _, dur := range []string{"", "200"} {
				songArtist := buildProviderSongArtistKey("qq_lyrics", "Song [Live]", artist)
				base := store.DeriveKey(buildProviderCacheKey("qq_lyrics", "Song [Live]", artist, album, dur)).BaseKey
				if base != songArtist && base != strings.TrimRight(songArtist, " ") && !strings.HasPrefix(base, songArtist+" [") {
					t.Errorf("artist=%q album=%q dur=%q: base_key %q does not extend song-artist key %q", artist, album, dur, base, songArtist)
				}
			}
		}
	}
}

func TestIsEmptySearchError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"qq empty search", providers.NewProviderError("qq", "no songs found for: projections - Night Tapes", nil), true},
		{"kugou empty search", providers.NewProviderError("kugou", "no songs found for: Rise - Skillet", nil), true},
		{"duration filter miss", providers.NewProviderError("qq", "no songs within 2000ms of duration 260000ms", nil), false},
		{"search request failure", providers.NewProviderError("qq", "song search failed", fmt.Errorf("request failed")), false},
		{"empty lyrics", providers.NewProviderError("qq", "failed to fetch QRC lyrics: lyrics content is empty", nil), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEmptySearchError(tt.err); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseDurationSec(t *testing.T) {
	tests := []struct {
		in   string
		want *int
	}{
		{"", nil},
		{"abc", nil},
		{"200", intPtr(200)},
		{"0", intPtr(0)},
		{"200s", intPtr(200)}, // Sscanf reads leading digits, ignores trailing "s"
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := parseDurationSec(tt.in)
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("got %d, want nil", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("got nil, want %d", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Fatalf("got %d, want %d", *got, *tt.want)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func TestShouldNegativeCache(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"ttml duration miss", fmt.Errorf("no tracks within 2000ms of duration 170000ms (closest: a - b at 180000ms, diff: 10000ms)"), true},
		{"regression: kugou duration miss is permanent", fmt.Errorf("kugou: no songs within 2000ms of duration 312000ms"), true},
		{"regression: qq duration miss is permanent", fmt.Errorf("qq: no songs within 2000ms of duration 312000ms"), true},
		{"provider search miss", fmt.Errorf("qq: no songs found for: Hass Und Liebe - Miss Construction"), true},
		{"empty lyrics", fmt.Errorf("lyrics content is empty"), true},
		{"timeout is transient", fmt.Errorf("context deadline exceeded"), false},
		{"upstream 500 is transient", fmt.Errorf("kugou: search returned 500"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldNegativeCache(tt.err); got != tt.want {
				t.Fatalf("shouldNegativeCache(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
