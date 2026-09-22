package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func setDurationVariant(t *testing.T, song, artist string, sec int, ttml string) string {
	t.Helper()
	base := fmt.Sprintf("ttml_lyrics:%s %s", song, artist)
	key := fmt.Sprintf("%s %ds", base, sec)
	err := testStore.SetLyrics(context.Background(), key,
		Key{Provider: "ttml", BaseKey: base, DurationSec: ptr(sec)},
		CachedLyrics{TTML: ttml, TrackDurationMs: sec * 1000, Format: "ttml"})
	if err != nil {
		t.Fatalf("seed duration %d: %v", sec, err)
	}
	return key
}

func TestSetLyrics_PersistsSyncTracking(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	checked := time.Now().UTC().Truncate(time.Second)
	in := CachedLyrics{TTML: "<tt/>", Source: "apple", TimingType: "line", AppleETag: `"abc--gzip"`, LastCheckedAt: &checked}
	if err := testStore.SetLyrics(ctx, "k1", Key{Provider: "ttml_lyrics", BaseKey: "song artist"}, in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := testStore.GetLyricsExact(ctx, "k1")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if got.TimingType != "line" || got.AppleETag != `"abc--gzip"` || got.LastCheckedAt == nil || !got.LastCheckedAt.Equal(checked) {
		t.Fatalf("sync fields not round-tripped: %+v", got)
	}
}

func TestLyrics_SetGetExact(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	key := "ttml_lyrics:shape of you ed sheeran"
	in := CachedLyrics{TTML: "<tt>hello</tt>", TrackDurationMs: 180000, Score: 0.95, Language: "en", Format: "ttml"}
	if err := testStore.SetLyrics(ctx, key, Key{Provider: "ttml", BaseKey: key}, in); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := testStore.GetLyricsExact(ctx, key)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got != in {
		t.Errorf("got %+v want %+v", got, in)
	}
}

func TestLyrics_GetMissReturnsFalse(t *testing.T) {
	resetTables(t)
	_, ok, err := testStore.GetLyricsExact(context.Background(), "ttml_lyrics:nope nobody")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("expected miss, got hit")
	}
}

func TestLyrics_EdgeCases(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	cases := []struct {
		name string
		key  string
		l    CachedLyrics
	}{
		{"empty_album", "ttml_lyrics:a b", CachedLyrics{TTML: "<tt>x</tt>", Format: "ttml"}},
		{"zero_duration_field", "ttml_lyrics:c d", CachedLyrics{TTML: "<tt>y</tt>"}},
		{"unicode", "ttml_lyrics:粉红色的回忆 韩宝仪", CachedLyrics{TTML: "<tt>回忆</tt>", Language: "zh"}},
		{"empty_ttml", "ttml_lyrics:e f", CachedLyrics{TTML: ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := testStore.SetLyrics(ctx, c.key, Key{Provider: "ttml", BaseKey: c.key}, c.l); err != nil {
				t.Fatalf("set: %v", err)
			}
			got, ok, err := testStore.GetLyricsExact(ctx, c.key)
			if err != nil || !ok {
				t.Fatalf("get: ok=%v err=%v", ok, err)
			}
			if got != c.l {
				t.Errorf("got %+v want %+v", got, c.l)
			}
		})
	}
}

func TestLyrics_SentinelRoundTrips(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	key := "ttml_lyrics:sentinel song x"
	if err := testStore.SetLyrics(ctx, key, Key{Provider: "ttml", BaseKey: key},
		CachedLyrics{TTML: "__NO_LYRICS__"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := testStore.GetLyricsExact(ctx, key)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.TTML != "__NO_LYRICS__" {
		t.Errorf("sentinel not preserved: %q", got.TTML)
	}
}

func TestLyrics_DurationTolerance(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	k := Key{Provider: "ttml", BaseKey: "ttml_lyrics:song artist", DurationSec: ptr(181)}

	t.Run("within_delta_returns_match", func(t *testing.T) {
		resetTables(t)
		setDurationVariant(t, "song", "artist", 179, "<tt>179</tt>")
		_, key, ok, err := testStore.GetLyricsTolerant(ctx, k, 2)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if key != "ttml_lyrics:song artist 179s" {
			t.Errorf("matched %q", key)
		}
	})

	t.Run("outside_delta_misses", func(t *testing.T) {
		resetTables(t)
		setDurationVariant(t, "song", "artist", 178, "<tt>178</tt>")
		_, _, ok, err := testStore.GetLyricsTolerant(ctx, k, 2)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Error("expected miss (178 is 3s from 181), got hit")
		}
	})

	t.Run("closest_wins", func(t *testing.T) {
		resetTables(t)
		setDurationVariant(t, "song", "artist", 179, "<tt>179</tt>")
		setDurationVariant(t, "song", "artist", 182, "<tt>182</tt>")
		got, key, ok, err := testStore.GetLyricsTolerant(ctx, k, 3)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if got.TTML != "<tt>182</tt>" || key != "ttml_lyrics:song artist 182s" {
			t.Errorf("expected 182 (diff 1) over 179 (diff 2), got %q", got.TTML)
		}
	})

	t.Run("tie_prefers_lower_duration", func(t *testing.T) {
		resetTables(t)
		setDurationVariant(t, "song", "artist", 180, "<tt>180</tt>")
		setDurationVariant(t, "song", "artist", 182, "<tt>182</tt>")
		got, _, ok, err := testStore.GetLyricsTolerant(ctx, k, 3)
		if err != nil || !ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if got.TTML != "<tt>180</tt>" {
			t.Errorf("tie should prefer lower duration 180, got %q", got.TTML)
		}
	})

	t.Run("nil_duration_no_tolerance", func(t *testing.T) {
		resetTables(t)
		setDurationVariant(t, "song", "artist", 180, "<tt>180</tt>")
		_, _, ok, err := testStore.GetLyricsTolerant(ctx, Key{Provider: "ttml", BaseKey: "ttml_lyrics:song artist"}, 2)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Error("nil duration should not tolerance-match")
		}
	})
}

func TestLyrics_CounterIncrementsOnNewNotOverwrite(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	k := Key{Provider: "ttml", BaseKey: "ttml_lyrics:s a"}
	if err := testStore.SetLyrics(ctx, "ttml_lyrics:s a", k, CachedLyrics{TTML: "<tt>1</tt>"}); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetLyrics(ctx, "ttml_lyrics:s2 a", Key{Provider: "ttml", BaseKey: "ttml_lyrics:s2 a"}, CachedLyrics{TTML: "<tt>2</tt>"}); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(t, "ttml"); got != 2 {
		t.Fatalf("counter after 2 inserts = %d, want 2", got)
	}
	if err := testStore.SetLyrics(ctx, "ttml_lyrics:s a", k, CachedLyrics{TTML: "<tt>updated</tt>"}); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(t, "ttml"); got != 2 {
		t.Errorf("counter after overwrite = %d, want 2 (no increment)", got)
	}
}
