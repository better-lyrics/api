package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type SongMetadata struct {
	CacheKey      string
	VideoIDs      []string
	AppleTrackID  string
	ISRC          string
	TrackName     string
	ArtistName    string
	AlbumName     string
	DurationMs    int
	ReleaseDate   string
	RawAttributes string
	FirstSeen     time.Time
	LastUpdated   time.Time
}

func (s *Store) SetSongMetadata(ctx context.Context, m *SongMetadata) error {
	if m.CacheKey == "" {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var rawAttrs any
	if m.RawAttributes != "" {
		rawAttrs = m.RawAttributes
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO song_metadata (cache_key, apple_track_id, isrc, track_name, artist_name,
		                           album_name, duration_ms, release_date, raw_attributes,
		                           first_seen, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
		ON CONFLICT (cache_key) DO UPDATE SET
			apple_track_id = EXCLUDED.apple_track_id,
			isrc           = EXCLUDED.isrc,
			track_name     = EXCLUDED.track_name,
			artist_name    = EXCLUDED.artist_name,
			album_name     = EXCLUDED.album_name,
			duration_ms    = EXCLUDED.duration_ms,
			release_date   = EXCLUDED.release_date,
			raw_attributes = EXCLUDED.raw_attributes,
			last_updated   = now()`,
		m.CacheKey, m.AppleTrackID, m.ISRC, m.TrackName, m.ArtistName,
		m.AlbumName, m.DurationMs, m.ReleaseDate, rawAttrs); err != nil {
		return err
	}

	for _, v := range m.VideoIDs {
		if v == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO video_map (video_id, cache_key) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			v, m.CacheKey); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) GetSongMetadata(ctx context.Context, cacheKey string) (*SongMetadata, bool, error) {
	m := &SongMetadata{CacheKey: cacheKey}
	var rawAttrs *string
	err := s.pool.QueryRow(ctx, `
		SELECT apple_track_id, isrc, track_name, artist_name, album_name,
		       duration_ms, release_date, raw_attributes, first_seen, last_updated
		FROM song_metadata WHERE cache_key = $1`, cacheKey).
		Scan(&m.AppleTrackID, &m.ISRC, &m.TrackName, &m.ArtistName, &m.AlbumName,
			&m.DurationMs, &m.ReleaseDate, &rawAttrs, &m.FirstSeen, &m.LastUpdated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if rawAttrs != nil {
		m.RawAttributes = *rawAttrs
	}
	vids, err := s.GetVideoIDs(ctx, cacheKey)
	if err != nil {
		return nil, false, err
	}
	m.VideoIDs = vids
	return m, true, nil
}

func (s *Store) AddVideoID(ctx context.Context, cacheKey, videoID string) error {
	if cacheKey == "" || videoID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO video_map (video_id, cache_key) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		videoID, cacheKey)
	return err
}

func (s *Store) GetVideoIDs(ctx context.Context, cacheKey string) ([]string, error) {
	return scanStrings(ctx, s.pool, `SELECT video_id FROM video_map WHERE cache_key = $1 ORDER BY video_id`, cacheKey)
}

func (s *Store) GetCacheKeysByVideoID(ctx context.Context, videoID string) ([]string, error) {
	return scanStrings(ctx, s.pool, `SELECT cache_key FROM video_map WHERE video_id = $1 ORDER BY cache_key`, videoID)
}

func (s *Store) GetCacheKeysByISRC(ctx context.Context, isrc string) ([]string, error) {
	return scanStrings(ctx, s.pool, `SELECT cache_key FROM song_metadata WHERE isrc = $1 ORDER BY cache_key`, isrc)
}

func (s *Store) GetCacheKeysBySongArtist(ctx context.Context, song, artist string) ([]string, error) {
	return scanStrings(ctx, s.pool,
		`SELECT cache_key FROM song_metadata WHERE lower(track_name) = lower($1) AND lower(artist_name) = lower($2) ORDER BY cache_key`,
		song, artist)
}

func (s *Store) GetAllVideoIDsForSong(ctx context.Context, song, artist string) ([]string, error) {
	return scanStrings(ctx, s.pool, `
		SELECT DISTINCT vm.video_id
		FROM video_map vm
		JOIN song_metadata sm ON sm.cache_key = vm.cache_key
		WHERE lower(sm.track_name) = lower($1) AND lower(sm.artist_name) = lower($2)
		ORDER BY vm.video_id`,
		song, artist)
}

func scanStrings(ctx context.Context, pool interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, sql string, args ...any) ([]string, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
