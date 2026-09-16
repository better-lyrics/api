package store

import (
	"context"
	"testing"
)

func resetTables(t *testing.T) {
	t.Helper()
	_, err := testStore.pool.Exec(context.Background(),
		`TRUNCATE lyrics, negative_cache, song_metadata, video_map, counters, storefront_cache, stats`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func counterValue(t *testing.T, prefix string) int64 {
	t.Helper()
	var n int64
	err := testStore.pool.QueryRow(context.Background(),
		`SELECT count FROM counters WHERE prefix = $1`, prefix).Scan(&n)
	if err != nil {
		return 0
	}
	return n
}
