package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type stagingSpec struct {
	table    string
	staging  string
	columns  []string
	conflict string
}

var (
	lyricsStaging = stagingSpec{
		table:   "lyrics",
		staging: "lyrics_staging",
		columns: []string{"cache_key", "provider", "base_key", "duration_sec",
			"raw_lyrics", "track_duration_ms", "score", "language", "is_rtl", "format"},
		conflict: "cache_key",
	}
	negativeStaging = stagingSpec{
		table:   "negative_cache",
		staging: "negative_cache_staging",
		columns: []string{"cache_key", "provider", "base_key", "duration_sec",
			"reason", "release_date", "has_time_synced_known", "created_at", "expires_at"},
		conflict: "cache_key",
	}
	metadataStaging = stagingSpec{
		table:   "song_metadata",
		staging: "song_metadata_staging",
		columns: []string{"cache_key", "apple_track_id", "isrc", "track_name", "artist_name",
			"album_name", "duration_ms", "release_date", "raw_attributes", "first_seen", "last_updated"},
		conflict: "cache_key",
	}
	videoMapStaging = stagingSpec{
		table:    "video_map",
		staging:  "video_map_staging",
		columns:  []string{"video_id", "cache_key"},
		conflict: "video_id, cache_key",
	}

	allStagingSpecs = []stagingSpec{lyricsStaging, negativeStaging, metadataStaging, videoMapStaging}
)

// copyAndUpsert bulk-loads rows via COPY into the staging table, then folds them into the
// real table with ON CONFLICT DO NOTHING; the returned count excludes conflicting rows.
func copyAndUpsert(ctx context.Context, tx pgx.Tx, spec stagingSpec, rows [][]any) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	if _, err := tx.CopyFrom(ctx, pgx.Identifier{spec.staging}, spec.columns, pgx.CopyFromRows(rows)); err != nil {
		return 0, fmt.Errorf("copy into %s: %w", spec.staging, err)
	}

	cols := strings.Join(spec.columns, ", ")
	tag, err := tx.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s (%s) SELECT %s FROM %s ON CONFLICT (%s) DO NOTHING`,
		spec.table, cols, cols, spec.staging, spec.conflict))
	if err != nil {
		return 0, fmt.Errorf("insert into %s: %w", spec.table, err)
	}

	if _, err := tx.Exec(ctx, fmt.Sprintf(`TRUNCATE %s`, spec.staging)); err != nil {
		return 0, fmt.Errorf("truncate %s: %w", spec.staging, err)
	}

	return tag.RowsAffected(), nil
}
