package cache

import "testing"

func TestPrefixOf(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ttml_lyrics:song artist", "ttml"},
		{"kugou_lyrics:foo", "kugou"},
		{"qq_lyrics:foo", "qq"},
		{"legacy_lyrics:foo", "legacy"},
		{"no_lyrics:song artist", "negative"},
		{"weird_no_colon_key", "unknown"},
		{"", "unknown"},
		{":empty_prefix", "unknown"},
		{"future_provider_lyrics:x", "future_provider"},
	}
	for _, tc := range cases {
		got := prefixOf(tc.in)
		if got != tc.want {
			t.Errorf("prefixOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
