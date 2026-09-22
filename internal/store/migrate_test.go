package store

import (
	"context"
	"testing"
)

func TestMigrate_CreatesTables(t *testing.T) {
	ctx := context.Background()

	tables := []string{
		"lyrics", "negative_cache", "song_metadata", "video_map",
		"counters", "stats", "storefront_cache", "migration_progress",
	}
	for _, tbl := range tables {
		var exists bool
		err := testStore.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`,
			tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("table %s does not exist", tbl)
		}
	}

	indexes := []string{
		"lyrics_tolerance_idx",
		"negative_cache_tolerance_idx",
		"negative_cache_expires_idx",
		"song_metadata_isrc_idx",
		"song_metadata_song_artist_idx",
		"video_map_cache_key_idx",
	}
	for _, idx := range indexes {
		var exists bool
		err := testStore.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`,
			idx).Scan(&exists)
		if err != nil {
			t.Fatalf("check index %s: %v", idx, err)
		}
		if !exists {
			t.Errorf("index %s does not exist", idx)
		}
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	ctx := context.Background()
	if err := testStore.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
