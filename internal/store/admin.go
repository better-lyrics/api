package store

import (
	"context"
	"strconv"
	"strings"
	"time"
)

type SyncUpgradeCandidate struct {
	CacheKey     string
	AppleTrackID string
	ISRC         string
	TimingType   string
	AppleETag    string
	TTML         string
	Name         string
	Artist       string
	Album        string
	DurationMs   int
	ReleaseDate  string
	LastChecked  *time.Time
}

func (s *Store) SelectSyncUpgradeCandidates(ctx context.Context, windowStart time.Time, limit int) ([]SyncUpgradeCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.cache_key, m.apple_track_id, m.isrc, l.timing_type, l.apple_etag,
		       l.raw_lyrics, m.track_name, m.artist_name, m.album_name,
		       l.track_duration_ms, m.release_date, l.last_checked_at
		FROM lyrics l
		JOIN song_metadata m ON l.cache_key = m.cache_key
		WHERE l.provider = 'ttml'
		  AND l.timing_type IN ('', 'none', 'line')
		  AND m.apple_track_id <> ''
		  AND m.release_date <> ''
		  AND to_date(m.release_date, 'YYYY-MM-DD') >= $1::date
		ORDER BY l.last_checked_at ASC NULLS FIRST, m.release_date DESC
		LIMIT $2`,
		windowStart, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SyncUpgradeCandidate
	for rows.Next() {
		var c SyncUpgradeCandidate
		var blob []byte
		if err := rows.Scan(&c.CacheKey, &c.AppleTrackID, &c.ISRC, &c.TimingType, &c.AppleETag,
			&blob, &c.Name, &c.Artist, &c.Album,
			&c.DurationMs, &c.ReleaseDate, &c.LastChecked); err != nil {
			return nil, err
		}
		c.TTML, err = gunzipBytes(blob)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SizeBytes returns the total on-disk size of the database.
func (s *Store) SizeBytes(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&n)
	return n, err
}

// ClearAll removes all lyrics, negative-cache entries, and counters. Metadata and
// video associations are preserved (they lived in a separate namespace under BoltDB).
func (s *Store) ClearAll(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `TRUNCATE lyrics, negative_cache, counters`)
	return err
}

// ClearProvider deletes all lyrics and negative-cache entries for one provider and
// resets its counter. It returns the number of lyrics rows deleted.
func (s *Store) ClearProvider(ctx context.Context, provider string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `DELETE FROM lyrics WHERE provider = $1`, provider)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM negative_cache WHERE provider = $1`, provider); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM counters WHERE prefix = $1`, provider); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

type CacheKeyInfo struct {
	Key      string
	Size     int
	Provider string
}

// ListCacheKeys returns lyrics keys filtered by an optional prefix and case-insensitive
// substring, capped at limit, plus the total number of matching keys.
func (s *Store) ListCacheKeys(ctx context.Context, prefix, contains string, limit int) ([]CacheKeyInfo, int64, error) {
	where := "WHERE TRUE"
	args := []any{}
	if prefix != "" {
		args = append(args, prefix+"%")
		where += " AND cache_key LIKE $1"
	}
	if contains != "" {
		args = append(args, "%"+strings.ToLower(contains)+"%")
		where += " AND lower(cache_key) LIKE $" + strconv.Itoa(len(args))
	}

	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM lyrics `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit)
	rows, err := s.pool.Query(ctx,
		`SELECT cache_key, octet_length(raw_lyrics), provider FROM lyrics `+where+
			` ORDER BY cache_key LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []CacheKeyInfo
	for rows.Next() {
		var ki CacheKeyInfo
		if err := rows.Scan(&ki.Key, &ki.Size, &ki.Provider); err != nil {
			return nil, 0, err
		}
		out = append(out, ki)
	}
	return out, total, rows.Err()
}

type MetadataStats struct {
	TotalEntries  int64
	WithVideoIDs  int64
	WithISRC      int64
	WithRawAttrs  int64
	VideoMapCount int64
}

func (s *Store) MetadataStats(ctx context.Context) (MetadataStats, error) {
	var m MetadataStats
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM song_metadata),
			(SELECT count(DISTINCT cache_key) FROM video_map),
			(SELECT count(*) FROM song_metadata WHERE isrc <> ''),
			(SELECT count(*) FROM song_metadata WHERE raw_attributes IS NOT NULL),
			(SELECT count(*) FROM video_map)`).
		Scan(&m.TotalEntries, &m.WithVideoIDs, &m.WithISRC, &m.WithRawAttrs, &m.VideoMapCount)
	return m, err
}

func (s *Store) SampleMetadata(ctx context.Context, n int) ([]*SongMetadata, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT cache_key, apple_track_id, isrc, track_name, artist_name, album_name,
		       duration_ms, release_date, raw_attributes, first_seen, last_updated
		FROM song_metadata ORDER BY last_updated DESC LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SongMetadata
	for rows.Next() {
		m := &SongMetadata{}
		var rawAttrs *string
		var releaseDate string
		if err := rows.Scan(&m.CacheKey, &m.AppleTrackID, &m.ISRC, &m.TrackName, &m.ArtistName,
			&m.AlbumName, &m.DurationMs, &releaseDate, &rawAttrs, &m.FirstSeen, &m.LastUpdated); err != nil {
			return nil, err
		}
		m.ReleaseDate = releaseDate
		if rawAttrs != nil {
			m.RawAttributes = *rawAttrs
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, m := range out {
		vids, err := s.GetVideoIDs(ctx, m.CacheKey)
		if err != nil {
			return nil, err
		}
		m.VideoIDs = vids
	}
	return out, nil
}
