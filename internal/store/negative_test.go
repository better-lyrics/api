package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestNegativeTTLSeconds(t *testing.T) {
	cfg := NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30}
	now := time.Date(2026, 9, 17, 2, 0, 0, 0, time.UTC)
	const day = int64(24 * 60 * 60)

	cases := []struct {
		name  string
		entry NegativeEntry
		want  int64
	}{
		{"unknown_field_uses_default", NegativeEntry{Reason: "x", ReleaseDate: "2026-09-16", HasTimeSyncedKnown: false}, 7 * day},
		{"empty_release_date_uses_default", NegativeEntry{Reason: "x", HasTimeSyncedKnown: true}, 7 * day},
		{"bad_release_date_uses_default", NegativeEntry{Reason: "x", ReleaseDate: "nonsense", HasTimeSyncedKnown: true}, 7 * day},
		{"released_today_tier_6h", NegativeEntry{ReleaseDate: "2026-09-17", HasTimeSyncedKnown: true}, 6 * 60 * 60},
		{"released_3d_tier_6h", NegativeEntry{ReleaseDate: "2026-09-14", HasTimeSyncedKnown: true}, 6 * 60 * 60},
		{"released_5d_tier_12h", NegativeEntry{ReleaseDate: "2026-09-12", HasTimeSyncedKnown: true}, 12 * 60 * 60},
		{"released_10d_tier_24h", NegativeEntry{ReleaseDate: "2026-09-07", HasTimeSyncedKnown: true}, day},
		{"released_20d_tier_3d", NegativeEntry{ReleaseDate: "2026-08-28", HasTimeSyncedKnown: true}, 3 * day},
		{"released_over_threshold_default", NegativeEntry{ReleaseDate: "2026-01-01", HasTimeSyncedKnown: true}, 7 * day},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := negativeTTLSeconds(c.entry, cfg, now); got != c.want {
				t.Errorf("got %d want %d", got, c.want)
			}
		})
	}
}

func TestNegativeTTL_CalendarDayRegression(t *testing.T) {
	cfg := NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30}
	now := time.Date(2026, 9, 17, 2, 0, 0, 0, time.UTC)
	e := NegativeEntry{ReleaseDate: "2026-09-13", HasTimeSyncedKnown: true}
	if got := negativeTTLSeconds(e, cfg, now); got != 12*60*60 {
		t.Errorf("released 4 calendar days ago must be tier 12h, got %d", got)
	}
}

func TestNegative_SetGet(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	cfg := NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30}
	key := "ttml_lyrics:neg song neg artist"
	k := Key{Provider: "ttml", BaseKey: key}

	if err := testStore.SetNegative(ctx, key, k, NegativeEntry{Reason: "Lyrics not available for this track"}, cfg); err != nil {
		t.Fatalf("set: %v", err)
	}
	reason, ok, err := testStore.GetNegative(ctx, key)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if reason != "Lyrics not available for this track" {
		t.Errorf("reason %q", reason)
	}
}

func TestNegative_ExpiredIsFiltered(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	cfg := NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30}
	key := "ttml_lyrics:expired song x"
	if err := testStore.SetNegative(ctx, key, Key{Provider: "ttml", BaseKey: key},
		NegativeEntry{Reason: "gone"}, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.pool.Exec(ctx,
		`UPDATE negative_cache SET expires_at = now() - interval '1 hour' WHERE cache_key = $1`, key); err != nil {
		t.Fatal(err)
	}
	_, ok, err := testStore.GetNegative(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expired negative entry should not be returned")
	}
}

func TestNegative_DurationTolerance(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	cfg := NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30}
	base := "ttml_lyrics:s a"
	set := func(sec int) {
		key := fmt.Sprintf("%s %ds", base, sec)
		if err := testStore.SetNegative(ctx, key,
			Key{Provider: "ttml", BaseKey: base, DurationSec: ptr(sec)},
			NegativeEntry{Reason: "none"}, cfg); err != nil {
			t.Fatal(err)
		}
	}
	set(179)
	set(182)
	k := Key{Provider: "ttml", BaseKey: base, DurationSec: ptr(181)}
	reason, key, ok, err := testStore.GetNegativeTolerant(ctx, k, 3)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if reason != "none" {
		t.Errorf("reason %q", reason)
	}
	if key == "" {
		t.Error("expected a matched cache key")
	}

	_, _, ok, err = testStore.GetNegativeTolerant(ctx, Key{Provider: "ttml", BaseKey: base, DurationSec: ptr(200)}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("200s is outside tolerance of seeded 179/182, expected miss")
	}
}

func TestNegative_Delete(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	cfg := NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30}
	key := "ttml_lyrics:del song x"
	if err := testStore.SetNegative(ctx, key, Key{Provider: "ttml", BaseKey: key},
		NegativeEntry{Reason: "temp"}, cfg); err != nil {
		t.Fatal(err)
	}
	if err := testStore.DeleteNegative(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := testStore.GetNegative(ctx, key); ok {
		t.Error("entry should be deleted")
	}
}
