package main

import "testing"

func TestDeriveKey(t *testing.T) {
	cases := []struct {
		name       string
		cacheKey   string
		wantProv   string
		wantBase   string
		wantHasDur bool
		wantDurSec int
	}{
		{
			name:       "normalized trailing duration",
			cacheKey:   "ttml_lyrics:song one artist one 200s",
			wantProv:   "ttml",
			wantBase:   "ttml_lyrics:song one artist one",
			wantHasDur: true,
			wantDurSec: 200,
		},
		{
			name:       "bracketed provider duration",
			cacheKey:   "kugou_lyrics:song two artist two [180s]",
			wantProv:   "kugou",
			wantBase:   "kugou_lyrics:song two artist two",
			wantHasDur: true,
			wantDurSec: 180,
		},
		{
			name:       "plain key without duration",
			cacheKey:   "legacy_lyrics:song three artist three",
			wantProv:   "legacy",
			wantBase:   "legacy_lyrics:song three artist three",
			wantHasDur: false,
		},
		{
			name:       "qq provider",
			cacheKey:   "qq_lyrics:song four artist four 90s",
			wantProv:   "qq",
			wantBase:   "qq_lyrics:song four artist four",
			wantHasDur: true,
			wantDurSec: 90,
		},
		{
			name:       "no provider suffix in key at all",
			cacheKey:   "someweirdkey",
			wantProv:   "someweirdkey",
			wantBase:   "someweirdkey",
			wantHasDur: false,
		},
		{
			name:       "artist literally ending in digits and s is a false positive we accept",
			cacheKey:   "ttml_lyrics:song five blink 182s",
			wantProv:   "ttml",
			wantBase:   "ttml_lyrics:song five blink",
			wantHasDur: true,
			wantDurSec: 182,
		},
		{
			name:       "negative cache base key after prefix strip",
			cacheKey:   "ttml_lyrics:song six artist six 150s",
			wantProv:   "ttml",
			wantBase:   "ttml_lyrics:song six artist six",
			wantHasDur: true,
			wantDurSec: 150,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveKey(tc.cacheKey)
			if got.Provider != tc.wantProv {
				t.Errorf("provider = %q, want %q", got.Provider, tc.wantProv)
			}
			if got.BaseKey != tc.wantBase {
				t.Errorf("baseKey = %q, want %q", got.BaseKey, tc.wantBase)
			}
			if tc.wantHasDur {
				if got.DurationSec == nil {
					t.Fatalf("durationSec = nil, want %d", tc.wantDurSec)
				}
				if *got.DurationSec != tc.wantDurSec {
					t.Errorf("durationSec = %d, want %d", *got.DurationSec, tc.wantDurSec)
				}
			} else if got.DurationSec != nil {
				t.Errorf("durationSec = %d, want nil", *got.DurationSec)
			}
		})
	}
}
