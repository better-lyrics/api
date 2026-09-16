package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func resetSchema(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `TRUNCATE lyrics, negative_cache, song_metadata, video_map,
		counters, storefront_cache, stats, migration_progress`)
	if err != nil {
		t.Fatalf("reset schema: %v", err)
	}
}

type lyricsRow struct {
	provider        string
	baseKey         string
	durationSec     *int
	ttml            string
	trackDurationMs int
	score           float64
	language        string
	isRTL           bool
	format          string
}

func queryLyricsRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cacheKey string) lyricsRow {
	t.Helper()
	var row lyricsRow
	var blob []byte
	err := pool.QueryRow(ctx, `
		SELECT provider, base_key, duration_sec, raw_lyrics, track_duration_ms, score, language, is_rtl, format
		FROM lyrics WHERE cache_key = $1`, cacheKey).
		Scan(&row.provider, &row.baseKey, &row.durationSec, &blob, &row.trackDurationMs, &row.score, &row.language, &row.isRTL, &row.format)
	if err != nil {
		t.Fatalf("query lyrics row %q: %v", cacheKey, err)
	}
	ttml, err := gunzipString(blob)
	if err != nil {
		t.Fatalf("gunzip raw_lyrics for %q: %v", cacheKey, err)
	}
	row.ttml = ttml
	return row
}

func tableCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (lyrics, negative, metadata, video int64) {
	t.Helper()
	queryCount := func(table string) int64 {
		var n int64
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	return queryCount("lyrics"), queryCount("negative_cache"), queryCount("song_metadata"), queryCount("video_map")
}
