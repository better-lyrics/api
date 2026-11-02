package httpapi

import "testing"

func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func FuzzIsExactCacheKey(f *testing.F) {
	for _, seed := range []struct{ song, artist, album, duration, other string }{
		{"Blinding Lights", "The Weeknd", "", "200", "201"},
		{"  Song ", "ARTIST", "Album", "200", "199"},
		{"a", "b", "", "1", " 1"},
		{"", "", "", "", "1"},
		{"song 200s", "x", "", "", "200"},
		{"日本", "アーティスト", "アルバム", "300", "3000"},
		{"x", "y", "z 2", "2", "02"},
		{"İ", "ẞ", "Ω", "10", "1"},
	} {
		f.Add(seed.song, seed.artist, seed.album, seed.duration, seed.other)
	}
	f.Fuzz(func(t *testing.T, song, artist, album, duration, other string) {
		normalized := buildNormalizedCacheKey(song, artist, album, duration)
		if !isExactCacheKey(normalized, song, artist, album, duration) {
			t.Fatalf("normalized key %q is not exact for its own inputs", normalized)
		}
		legacy := buildLegacyCacheKey(song, artist, album, duration)
		if !isExactCacheKey(legacy, song, artist, album, duration) {
			t.Fatalf("legacy key %q is not exact for its own inputs", legacy)
		}
		if other == "" || other == duration {
			return
		}
		neighbour := buildNormalizedCacheKey(song, artist, album, other)
		if isExactCacheKey(neighbour, song, artist, album, duration) && neighbour != normalized && (isDecimal(duration) || duration == "") && isDecimal(other) {
			t.Fatalf("key %q for duration %q counts as exact for duration %q (normalized %q, legacy %q)",
				neighbour, other, duration, normalized, legacy)
		}
	})
}
