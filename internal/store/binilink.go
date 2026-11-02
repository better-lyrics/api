package store

import (
	"context"
	"time"
)

type BiniLinkPending struct {
	VideoID      string
	AppleTrackID string
	Attempts     int
}

func (s *Store) EnqueueBiniLink(ctx context.Context, videoID, appleTrackID string) error {
	if videoID == "" || appleTrackID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO binilink_links (video_id, apple_track_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		videoID, appleTrackID)
	return err
}

func (s *Store) ClaimBiniLinkPending(ctx context.Context, limit int) ([]BiniLinkPending, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT video_id, apple_track_id, attempts FROM binilink_links
		WHERE status = 'pending' AND next_attempt <= now()
		ORDER BY next_attempt
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BiniLinkPending
	for rows.Next() {
		var p BiniLinkPending
		if err := rows.Scan(&p.VideoID, &p.AppleTrackID, &p.Attempts); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SetBiniLinkResult(ctx context.Context, videoID, status string, attempts int, nextAttempt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE binilink_links
		SET status = $2, attempts = $3, next_attempt = $4, updated_at = now()
		WHERE video_id = $1`,
		videoID, status, attempts, nextAttempt)
	return err
}

// SeedBiniLinkBackfill enqueues one page of historical videos, keeping only those whose
// every cache key has metadata, an Apple track ID and the same non-empty ISRC.
func (s *Store) SeedBiniLinkBackfill(ctx context.Context, after string, pageSize int) (seeded int, next string, err error) {
	var last *string
	var scanned, inserted int
	err = s.pool.QueryRow(ctx, `
		WITH page AS (
			SELECT DISTINCT video_id FROM video_map
			WHERE video_id > $1
			ORDER BY video_id
			LIMIT $2
		),
		candidates AS (
			SELECT p.video_id, min(m.apple_track_id) AS apple_track_id
			FROM page p
			JOIN video_map v ON v.video_id = p.video_id
			LEFT JOIN song_metadata m ON m.cache_key = v.cache_key
			GROUP BY p.video_id
			HAVING bool_and(m.cache_key IS NOT NULL)
			   AND bool_and(m.apple_track_id <> '')
			   AND bool_and(m.isrc <> '')
			   AND count(DISTINCT m.isrc) = 1
		),
		ins AS (
			INSERT INTO binilink_links (video_id, apple_track_id)
			SELECT video_id, apple_track_id FROM candidates
			ON CONFLICT DO NOTHING
			RETURNING 1
		)
		SELECT (SELECT max(video_id) FROM page), (SELECT count(*) FROM page), (SELECT count(*) FROM ins)`,
		after, pageSize).Scan(&last, &scanned, &inserted)
	if err != nil {
		return 0, "", err
	}
	if last == nil || scanned < pageSize {
		return inserted, "", nil
	}
	return inserted, *last, nil
}
