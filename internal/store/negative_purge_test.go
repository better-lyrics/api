package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func seedNegatives(t *testing.T, prefix string, n int, expired bool) {
	t.Helper()
	ctx := context.Background()
	cfg := NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30}
	for i := range n {
		key := fmt.Sprintf("ttml_lyrics:%s %d", prefix, i)
		if err := testStore.SetNegative(ctx, key, Key{Provider: "ttml", BaseKey: key},
			NegativeEntry{Reason: "Lyrics not available for this track"}, cfg); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if expired {
		if _, err := testStore.pool.Exec(ctx,
			`UPDATE negative_cache SET expires_at = now() - interval '1 hour' WHERE cache_key LIKE $1`,
			"ttml_lyrics:"+prefix+" %"); err != nil {
			t.Fatalf("expire: %v", err)
		}
	}
}

func negativeCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := testStore.pool.QueryRow(context.Background(), `SELECT count(*) FROM negative_cache`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestPurgeExpiredNegative(t *testing.T) {
	ctx := context.Background()

	t.Run("deletes expired rows up to the batch limit and keeps live ones", func(t *testing.T) {
		resetTables(t)
		seedNegatives(t, "dead", 7, true)
		seedNegatives(t, "live", 3, false)

		deleted, err := testStore.PurgeExpiredNegative(ctx, 5)
		if err != nil {
			t.Fatalf("purge: %v", err)
		}
		if deleted != 5 {
			t.Fatalf("deleted %d, want 5", deleted)
		}
		if got := negativeCount(t); got != 5 {
			t.Fatalf("remaining %d, want 5", got)
		}
	})

	t.Run("live entries stay readable after a purge", func(t *testing.T) {
		resetTables(t)
		seedNegatives(t, "dead", 4, true)
		seedNegatives(t, "live", 2, false)

		if _, err := testStore.PurgeExpiredNegative(ctx, 100); err != nil {
			t.Fatalf("purge: %v", err)
		}
		if _, ok, err := testStore.GetNegative(ctx, "ttml_lyrics:live 0"); err != nil || !ok {
			t.Fatalf("live entry lost: ok=%v err=%v", ok, err)
		}
		if got := negativeCount(t); got != 2 {
			t.Fatalf("remaining %d, want 2", got)
		}
	})

	t.Run("nothing expired deletes nothing", func(t *testing.T) {
		resetTables(t)
		seedNegatives(t, "live", 3, false)
		deleted, err := testStore.PurgeExpiredNegative(ctx, 100)
		if err != nil || deleted != 0 {
			t.Fatalf("deleted=%d err=%v", deleted, err)
		}
	})

	t.Run("empty table deletes nothing", func(t *testing.T) {
		resetTables(t)
		deleted, err := testStore.PurgeExpiredNegative(ctx, 100)
		if err != nil || deleted != 0 {
			t.Fatalf("deleted=%d err=%v", deleted, err)
		}
	})

	t.Run("regression: an entry refreshed after expiry is not purged", func(t *testing.T) {
		resetTables(t)
		seedNegatives(t, "again", 1, true)
		seedNegatives(t, "again", 1, false)
		deleted, err := testStore.PurgeExpiredNegative(ctx, 100)
		if err != nil || deleted != 0 {
			t.Fatalf("deleted=%d err=%v", deleted, err)
		}
	})
}

func TestPurgeAllExpiredNegative(t *testing.T) {
	ctx := context.Background()

	t.Run("drains every expired row across batches", func(t *testing.T) {
		resetTables(t)
		seedNegatives(t, "dead", 23, true)
		seedNegatives(t, "live", 4, false)

		total, err := testStore.PurgeAllExpiredNegative(ctx, 5, 0)
		if err != nil {
			t.Fatalf("purge all: %v", err)
		}
		if total != 23 {
			t.Fatalf("total %d, want 23", total)
		}
		if got := negativeCount(t); got != 4 {
			t.Fatalf("remaining %d, want 4", got)
		}
	})

	t.Run("stops when the context is cancelled", func(t *testing.T) {
		resetTables(t)
		seedNegatives(t, "dead", 10, true)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		if _, err := testStore.PurgeAllExpiredNegative(cancelled, 2, time.Millisecond); err == nil {
			t.Fatal("expected context error")
		}
	})
}
