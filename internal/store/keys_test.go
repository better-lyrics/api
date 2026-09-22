package store

import "testing"

func TestDeriveKey(t *testing.T) {
	tests := []struct {
		name        string
		cacheKey    string
		provider    string
		baseKey     string
		durationSec *int
	}{
		{
			name:        "bracketed duration",
			cacheKey:    "ttml_lyrics:blinding lights the weeknd [200s]",
			provider:    "ttml",
			baseKey:     "ttml_lyrics:blinding lights the weeknd",
			durationSec: ptr(200),
		},
		{
			name:        "normalized trailing duration",
			cacheKey:    "ttml_lyrics:song artist 200s",
			provider:    "ttml",
			baseKey:     "ttml_lyrics:song artist",
			durationSec: ptr(200),
		},
		{
			name:        "provider prefix without lyrics suffix stays intact after colon split",
			cacheKey:    "kugou_lyrics:song artist",
			provider:    "kugou",
			baseKey:     "kugou_lyrics:song artist",
			durationSec: nil,
		},
		{
			name:        "no duration token",
			cacheKey:    "ttml_lyrics:song artist",
			provider:    "ttml",
			baseKey:     "ttml_lyrics:song artist",
			durationSec: nil,
		},
		{
			name:        "greedy base captures earlier second-like token",
			cacheKey:    "ttml_lyrics:song 5s artist 200s",
			provider:    "ttml",
			baseKey:     "ttml_lyrics:song 5s artist",
			durationSec: ptr(200),
		},
		{
			name:        "no colon strips lyrics suffix from provider only",
			cacheKey:    "foo_lyrics",
			provider:    "foo",
			baseKey:     "foo_lyrics",
			durationSec: nil,
		},
		{
			name:        "bracketed wins even when a bare number precedes it",
			cacheKey:    "ttml_lyrics:track 12 [90s]",
			provider:    "ttml",
			baseKey:     "ttml_lyrics:track 12",
			durationSec: ptr(90),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveKey(tt.cacheKey)
			if got.Provider != tt.provider {
				t.Errorf("provider: got %q, want %q", got.Provider, tt.provider)
			}
			if got.BaseKey != tt.baseKey {
				t.Errorf("baseKey: got %q, want %q", got.BaseKey, tt.baseKey)
			}
			switch {
			case tt.durationSec == nil && got.DurationSec != nil:
				t.Errorf("duration: got %d, want nil", *got.DurationSec)
			case tt.durationSec != nil && got.DurationSec == nil:
				t.Errorf("duration: got nil, want %d", *tt.durationSec)
			case tt.durationSec != nil && *got.DurationSec != *tt.durationSec:
				t.Errorf("duration: got %d, want %d", *got.DurationSec, *tt.durationSec)
			}
		})
	}
}
