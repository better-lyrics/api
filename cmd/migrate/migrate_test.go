package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func newTestMigrator(t *testing.T, ctx context.Context, boltPath string) (*Migrator, func()) {
	t.Helper()
	m, cleanup, err := NewMigrator(ctx, boltPath, "", testDSN)
	if err != nil {
		t.Fatalf("new migrator: %v", err)
	}
	resetSchema(t, ctx, m.store.Pool())
	return m, cleanup
}

func TestMigrate_FullFixtureAndIdempotency(t *testing.T) {
	ctx := context.Background()

	boltPath := filepath.Join(t.TempDir(), "cache.db")
	fx := buildFixture(t, boltPath)

	m, cleanup := newTestMigrator(t, ctx, boltPath)
	defer cleanup()
	m.batchSize = 2 // force multiple batches per bucket to exercise resumability

	res, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if res.LyricsInserted != 4 {
		t.Errorf("LyricsInserted = %d, want 4", res.LyricsInserted)
	}
	if res.NegativeInserted != 1 {
		t.Errorf("NegativeInserted = %d, want 1", res.NegativeInserted)
	}
	if res.MetadataInserted != 1 {
		t.Errorf("MetadataInserted = %d, want 1", res.MetadataInserted)
	}
	if res.VideoMapInserted != 2 {
		t.Errorf("VideoMapInserted = %d, want 2", res.VideoMapInserted)
	}
	if res.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", res.Skipped)
	}

	pool := m.store.Pool()

	normalized := queryLyricsRow(t, ctx, pool, fx.normalizedKey)
	if normalized.ttml != fx.normalizedTTML {
		t.Errorf("normalized ttml = %q, want %q", normalized.ttml, fx.normalizedTTML)
	}
	if normalized.provider != "ttml" || normalized.baseKey != "ttml_lyrics:song one artist one" {
		t.Errorf("normalized key derivation = %+v", normalized)
	}
	if normalized.durationSec == nil || *normalized.durationSec != 200 {
		t.Errorf("normalized durationSec = %v, want 200", normalized.durationSec)
	}
	if normalized.trackDurationMs != 200000 || normalized.language != "en" || normalized.format != "ttml" {
		t.Errorf("normalized fields = %+v", normalized)
	}

	bracketed := queryLyricsRow(t, ctx, pool, fx.bracketedKey)
	if bracketed.ttml != fx.bracketedTTML {
		t.Errorf("bracketed ttml = %q, want %q", bracketed.ttml, fx.bracketedTTML)
	}
	if bracketed.provider != "kugou" || bracketed.baseKey != "kugou_lyrics:song two artist two" {
		t.Errorf("bracketed key derivation = %+v", bracketed)
	}
	if bracketed.durationSec == nil || *bracketed.durationSec != 180 {
		t.Errorf("bracketed durationSec = %v, want 180", bracketed.durationSec)
	}

	plain := queryLyricsRow(t, ctx, pool, fx.plainKey)
	if plain.ttml != fx.plainTTML {
		t.Errorf("plain ttml = %q, want %q", plain.ttml, fx.plainTTML)
	}
	if plain.provider != "legacy" || plain.baseKey != fx.plainKey || plain.durationSec != nil {
		t.Errorf("plain key derivation = %+v", plain)
	}

	sentinel := queryLyricsRow(t, ctx, pool, fx.sentinelKey)
	if sentinel.ttml != noLyricsSentinel {
		t.Errorf("sentinel ttml = %q, want %q", sentinel.ttml, noLyricsSentinel)
	}

	var negReason, negBaseKey, negProvider string
	var negDurationSec *int
	var createdAtBeforeExpiresAt bool
	err = pool.QueryRow(ctx, `
		SELECT reason, provider, base_key, duration_sec, expires_at > created_at
		FROM negative_cache WHERE cache_key = $1`, fx.negativeCK).
		Scan(&negReason, &negProvider, &negBaseKey, &negDurationSec, &createdAtBeforeExpiresAt)
	if err != nil {
		t.Fatalf("query negative_cache: %v", err)
	}
	if negReason != fx.negativeReason {
		t.Errorf("negative reason = %q, want %q", negReason, fx.negativeReason)
	}
	if negProvider != "ttml" || negBaseKey != "ttml_lyrics:song four artist four" {
		t.Errorf("negative key derivation = provider=%q base_key=%q", negProvider, negBaseKey)
	}
	if negDurationSec == nil || *negDurationSec != 150 {
		t.Errorf("negative durationSec = %v, want 150", negDurationSec)
	}
	if !createdAtBeforeExpiresAt {
		t.Error("expected negative_cache expires_at > created_at")
	}

	var trackName, artistName, isrc string
	err = pool.QueryRow(ctx, `
		SELECT track_name, artist_name, isrc FROM song_metadata WHERE cache_key = $1`, fx.metadataKey).
		Scan(&trackName, &artistName, &isrc)
	if err != nil {
		t.Fatalf("query song_metadata: %v", err)
	}
	if trackName != "Song One" || artistName != "Artist One" || isrc != "USABC1234567" {
		t.Errorf("song_metadata fields = track=%q artist=%q isrc=%q", trackName, artistName, isrc)
	}

	var videoCount int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM video_map WHERE cache_key = $1`, fx.metadataKey).Scan(&videoCount); err != nil {
		t.Fatalf("count video_map: %v", err)
	}
	if videoCount != 2 {
		t.Errorf("video_map rows = %d, want 2", videoCount)
	}

	counts, err := m.store.Counts(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts["ttml"] != 2 {
		t.Errorf("counters[ttml] = %d, want 2", counts["ttml"])
	}
	if counts["kugou"] != 1 {
		t.Errorf("counters[kugou] = %d, want 1", counts["kugou"])
	}
	if counts["legacy"] != 1 {
		t.Errorf("counters[legacy] = %d, want 1", counts["legacy"])
	}

	lyricsN, negN, metaN, vidN := tableCounts(t, ctx, pool)

	res2, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("second run (resumable skip): %v", err)
	}
	if res2.LyricsInserted != 0 || res2.NegativeInserted != 0 || res2.MetadataInserted != 0 || res2.VideoMapInserted != 0 {
		t.Errorf("expected no new rows on idempotent re-run, got %+v", res2)
	}
	lyricsN2, negN2, metaN2, vidN2 := tableCounts(t, ctx, pool)
	if lyricsN2 != lyricsN || negN2 != negN || metaN2 != metaN || vidN2 != vidN {
		t.Errorf("row counts changed after resumable re-run: before=(%d,%d,%d,%d) after=(%d,%d,%d,%d)",
			lyricsN, negN, metaN, vidN, lyricsN2, negN2, metaN2, vidN2)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM migration_progress`); err != nil {
		t.Fatalf("reset migration_progress: %v", err)
	}
	res3, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("third run (forced full rescan): %v", err)
	}
	if res3.LyricsInserted != 0 || res3.NegativeInserted != 0 || res3.MetadataInserted != 0 || res3.VideoMapInserted != 0 {
		t.Errorf("expected ON CONFLICT DO NOTHING to suppress duplicate inserts, got %+v", res3)
	}
	lyricsN3, negN3, metaN3, vidN3 := tableCounts(t, ctx, pool)
	if lyricsN3 != lyricsN || negN3 != negN || metaN3 != metaN || vidN3 != vidN {
		t.Errorf("row counts changed after forced rescan: before=(%d,%d,%d,%d) after=(%d,%d,%d,%d)",
			lyricsN, negN, metaN, vidN, lyricsN3, negN3, metaN3, vidN3)
	}

	vres, err := m.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !vres.Passed {
		t.Errorf("expected verify to pass, got %+v", vres)
	}
	if vres.SpotChecked == 0 {
		t.Error("expected verify to spot-check at least one key")
	}
}

func TestMigrate_StatsBestEffort(t *testing.T) {
	ctx := context.Background()

	boltPath := filepath.Join(t.TempDir(), "cache.db")
	buildFixture(t, boltPath)

	m, cleanup := newTestMigrator(t, ctx, boltPath)
	defer cleanup()

	statsPath := filepath.Join(t.TempDir(), "stats.db")
	db, err := bolt.Open(statsPath, 0600, nil)
	if err != nil {
		t.Fatalf("open stats bolt db: %v", err)
	}
	wantJSON := `{"total_requests":42,"first_started":"2024-01-01T00:00:00Z"}`
	err = db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("stats"))
		if err != nil {
			return err
		}
		return b.Put([]byte("server_stats"), []byte(wantJSON))
	})
	if err != nil {
		t.Fatalf("seed stats bolt db: %v", err)
	}
	db.Close()

	m.statsPath = statsPath

	res, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.StatsLoaded {
		t.Errorf("expected StatsLoaded = true, got StatsError = %v", res.StatsError)
	}
	if res.StatsError != nil {
		t.Errorf("expected no stats error, got %v", res.StatsError)
	}

	raw, err := m.store.LoadStats(ctx)
	if err != nil {
		t.Fatalf("load stats: %v", err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal loaded stats: %v", err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatalf("unmarshal want stats: %v", err)
	}
	if got["total_requests"] != want["total_requests"] {
		t.Errorf("loaded stats total_requests = %v, want %v", got["total_requests"], want["total_requests"])
	}
}

func TestMigrate_StatsMissingIsBestEffort(t *testing.T) {
	ctx := context.Background()

	boltPath := filepath.Join(t.TempDir(), "cache.db")
	buildFixture(t, boltPath)

	m, cleanup := newTestMigrator(t, ctx, boltPath)
	defer cleanup()
	m.statsPath = filepath.Join(t.TempDir(), "does-not-exist.db")

	res, err := m.Run(ctx)
	if err != nil {
		t.Fatalf("run should not fail overall when stats.db is missing: %v", err)
	}
	if res.StatsLoaded {
		t.Error("expected StatsLoaded = false for missing stats.db")
	}
	if res.StatsError == nil {
		t.Error("expected StatsError to be set for missing stats.db")
	}
}

func TestVerify_DetectsMismatch(t *testing.T) {
	ctx := context.Background()

	boltPath := filepath.Join(t.TempDir(), "cache.db")
	fx := buildFixture(t, boltPath)

	m, cleanup := newTestMigrator(t, ctx, boltPath)
	defer cleanup()

	if _, err := m.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	corrupted, err := gzipString("<tt>corrupted</tt>")
	if err != nil {
		t.Fatalf("gzip corrupted payload: %v", err)
	}
	if _, err := m.store.Pool().Exec(ctx,
		`UPDATE lyrics SET raw_lyrics = $1 WHERE cache_key = $2`, corrupted, fx.normalizedKey); err != nil {
		t.Fatalf("corrupt row: %v", err)
	}

	vres, err := m.Verify(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if vres.Passed {
		t.Error("expected verify to fail after corrupting a row")
	}
	found := false
	for _, k := range vres.SpotFailed {
		if k == fx.normalizedKey {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SpotFailed to contain %q, got %v", fx.normalizedKey, vres.SpotFailed)
	}
}
