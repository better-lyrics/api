package store

import (
	"context"
	"testing"
	"time"
)

func TestSelectSyncUpgradeCandidates(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	now := time.Now()
	recent := now.AddDate(0, 0, -5).Format("2006-01-02")
	old := now.AddDate(0, 0, -100).Format("2006-01-02")

	seed := func(key, timing, trackID, release string) {
		if err := testStore.SetLyrics(ctx, key, Key{Provider: "ttml_lyrics", BaseKey: key},
			CachedLyrics{TTML: "<tt/>", TimingType: timing}); err != nil {
			t.Fatalf("SetLyrics(%s): %v", key, err)
		}
		if err := testStore.SetSongMetadata(ctx, &SongMetadata{
			CacheKey: key, AppleTrackID: trackID, ISRC: "isrc-" + key, TrackName: key, ReleaseDate: release,
		}); err != nil {
			t.Fatalf("SetSongMetadata(%s): %v", key, err)
		}
	}
	seed("k_line", "line", "1", recent)
	seed("k_word", "word", "2", recent)
	seed("k_old", "line", "3", old)

	windowStart := now.AddDate(0, 0, -42)
	got, err := testStore.SelectSyncUpgradeCandidates(ctx, windowStart, 200)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %d: %+v", len(got), got)
	}
	if got[0].CacheKey != "k_line" || got[0].TimingType != "line" || got[0].AppleTrackID != "1" || got[0].TTML != "<tt/>" {
		t.Fatalf("wrong candidate: %+v", got[0])
	}
}

func seedLyric(t *testing.T, key string, k Key, ttml string) {
	t.Helper()
	if err := testStore.SetLyrics(context.Background(), key, k, CachedLyrics{TTML: ttml}); err != nil {
		t.Fatalf("SetLyrics(%s): %v", key, err)
	}
}

func TestSizeBytes(t *testing.T) {
	resetTables(t)
	n, err := testStore.SizeBytes(context.Background())
	if err != nil {
		t.Fatalf("SizeBytes: %v", err)
	}
	if n <= 0 {
		t.Fatalf("expected positive database size, got %d", n)
	}
}

func TestListCacheKeys(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	seedLyric(t, "ttml_lyrics:alpha artist", Key{Provider: "ttml", BaseKey: "ttml_lyrics:alpha artist"}, "<tt>a</tt>")
	seedLyric(t, "ttml_lyrics:beta artist", Key{Provider: "ttml", BaseKey: "ttml_lyrics:beta artist"}, "<tt>b</tt>")
	seedLyric(t, "kugou_lyrics:gamma artist", Key{Provider: "kugou", BaseKey: "kugou_lyrics:gamma artist"}, "[00:01]g")

	t.Run("prefix filter", func(t *testing.T) {
		got, total, err := testStore.ListCacheKeys(ctx, "ttml_lyrics:", "", 100)
		if err != nil {
			t.Fatalf("ListCacheKeys: %v", err)
		}
		if total != 2 || len(got) != 2 {
			t.Fatalf("expected 2 ttml keys, got len=%d total=%d", len(got), total)
		}
		for _, ki := range got {
			if ki.Provider != "ttml" || ki.Size <= 0 {
				t.Fatalf("unexpected key info %+v", ki)
			}
		}
	})

	t.Run("case-insensitive contains", func(t *testing.T) {
		got, total, err := testStore.ListCacheKeys(ctx, "", "BETA", 100)
		if err != nil {
			t.Fatalf("ListCacheKeys: %v", err)
		}
		if total != 1 || len(got) != 1 || got[0].Key != "ttml_lyrics:beta artist" {
			t.Fatalf("expected the beta key, got %+v (total %d)", got, total)
		}
	})

	t.Run("limit caps rows but not total", func(t *testing.T) {
		got, total, err := testStore.ListCacheKeys(ctx, "", "", 1)
		if err != nil {
			t.Fatalf("ListCacheKeys: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected total 3, got %d", total)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 row under limit, got %d", len(got))
		}
	})
}

func TestClearProvider(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	seedLyric(t, "ttml_lyrics:one artist", Key{Provider: "ttml", BaseKey: "ttml_lyrics:one artist"}, "<tt>1</tt>")
	seedLyric(t, "ttml_lyrics:two artist", Key{Provider: "ttml", BaseKey: "ttml_lyrics:two artist"}, "<tt>2</tt>")
	seedLyric(t, "kugou_lyrics:three artist", Key{Provider: "kugou", BaseKey: "kugou_lyrics:three artist"}, "[00:01]3")

	if got := counterValue(t, "ttml"); got != 2 {
		t.Fatalf("expected ttml counter 2 before clear, got %d", got)
	}

	deleted, err := testStore.ClearProvider(ctx, "ttml")
	if err != nil {
		t.Fatalf("ClearProvider: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 rows deleted, got %d", deleted)
	}
	if got := counterValue(t, "ttml"); got != 0 {
		t.Fatalf("expected ttml counter reset to 0, got %d", got)
	}

	remaining, total, err := testStore.ListCacheKeys(ctx, "", "", 100)
	if err != nil {
		t.Fatalf("ListCacheKeys: %v", err)
	}
	if total != 1 || len(remaining) != 1 || remaining[0].Provider != "kugou" {
		t.Fatalf("expected only the kugou key to survive, got %+v", remaining)
	}
}

func TestClearAll_PreservesMetadata(t *testing.T) {
	resetTables(t)
	ctx := context.Background()
	seedLyric(t, "ttml_lyrics:song artist", Key{Provider: "ttml", BaseKey: "ttml_lyrics:song artist"}, "<tt>x</tt>")
	if err := testStore.SetSongMetadata(ctx, &SongMetadata{
		CacheKey: "ttml_lyrics:song artist",
		ISRC:     "USABC1234567",
		VideoIDs: []string{"vid123"},
	}); err != nil {
		t.Fatalf("SetSongMetadata: %v", err)
	}

	if err := testStore.ClearAll(ctx); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}

	_, total, err := testStore.ListCacheKeys(ctx, "", "", 100)
	if err != nil {
		t.Fatalf("ListCacheKeys: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected lyrics cleared, got total %d", total)
	}

	ms, err := testStore.MetadataStats(ctx)
	if err != nil {
		t.Fatalf("MetadataStats: %v", err)
	}
	if ms.TotalEntries != 1 {
		t.Fatalf("expected metadata preserved after ClearAll, got %d entries", ms.TotalEntries)
	}
	if ms.WithISRC != 1 || ms.VideoMapCount != 1 {
		t.Fatalf("expected ISRC and video associations preserved, got %+v", ms)
	}
}
