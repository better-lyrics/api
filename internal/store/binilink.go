package store

import (
	"context"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// BiniLink's ingress rejects any other ID shape with a 400.
const (
	biniLinkVideoIDPattern      = `^[A-Za-z0-9_-]{11}$`
	biniLinkAppleTrackIDPattern = `^[0-9]{1,128}$`
)

var (
	biniLinkVideoID      = regexp.MustCompile(biniLinkVideoIDPattern)
	biniLinkAppleTrackID = regexp.MustCompile(biniLinkAppleTrackIDPattern)
)

type BiniLinkPending struct {
	VideoID           string
	AppleTrackID      string
	Attempts          int
	UnresolvedRetried bool
}

func (s *Store) EnqueueBiniLink(ctx context.Context, videoID, appleTrackID string) error {
	if !biniLinkVideoID.MatchString(videoID) || !biniLinkAppleTrackID.MatchString(appleTrackID) {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO binilink_links (video_id, apple_track_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		videoID, appleTrackID)
	return err
}

// AddVideoIDAndEnqueueBiniLink records the video like AddVideoID and queues its Apple track in the same round trip.
func (s *Store) AddVideoIDAndEnqueueBiniLink(ctx context.Context, cacheKey, videoID string) error {
	if !biniLinkVideoID.MatchString(videoID) {
		return s.AddVideoID(ctx, cacheKey, videoID)
	}
	if cacheKey == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		WITH mapped AS (
			INSERT INTO video_map (video_id, cache_key) VALUES ($1, $2) ON CONFLICT DO NOTHING
		)
		INSERT INTO binilink_links (video_id, apple_track_id)
		SELECT $1, apple_track_id FROM song_metadata
		WHERE cache_key = $2 AND apple_track_id ~ $3
		ON CONFLICT DO NOTHING`,
		videoID, cacheKey, biniLinkAppleTrackIDPattern)
	return err
}

func (s *Store) ClaimBiniLinkPending(ctx context.Context, limit int) ([]BiniLinkPending, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT video_id, apple_track_id, attempts, unresolved_retried FROM binilink_links
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
		if err := rows.Scan(&p.VideoID, &p.AppleTrackID, &p.Attempts, &p.UnresolvedRetried); err != nil {
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

func (s *Store) MarkBiniLinkUnresolvedRetry(ctx context.Context, videoID string, attempts int, nextAttempt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE binilink_links
		SET status = 'pending', unresolved_retried = TRUE, attempts = $2, next_attempt = $3, updated_at = now()
		WHERE video_id = $1`,
		videoID, attempts, nextAttempt)
	return err
}

// SeedBiniLinkBackfill enqueues one page of historical videos, keeping only those whose
// every cache key has metadata, a valid Apple track ID and the same non-empty ISRC.
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
			WHERE p.video_id ~ $3
			GROUP BY p.video_id
			HAVING bool_and(m.cache_key IS NOT NULL)
			   AND bool_and(m.apple_track_id ~ $4)
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
		after, pageSize, biniLinkVideoIDPattern, biniLinkAppleTrackIDPattern).Scan(&last, &scanned, &inserted)
	if err != nil {
		return 0, "", err
	}
	if last == nil || scanned < pageSize {
		return inserted, "", nil
	}
	return inserted, *last, nil
}

// Session advisory lock: keeps one uploader running while an old and a new deploy overlap.
const (
	biniLinkWorkerLockID     int64 = 0x62696e696c696e6b
	BiniLinkLockQueryTimeout       = 5 * time.Second
)

// BiniLinkWorkerLock holds the lock on its own connection, so it uses no pool slot and its loss shows in Held.
type BiniLinkWorkerLock struct {
	conn *pgx.Conn
}

func (s *Store) TryBiniLinkWorkerLock(ctx context.Context) (*BiniLinkWorkerLock, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, BiniLinkLockQueryTimeout)
	defer cancel()
	cfg := s.pool.Config().ConnConfig.Copy()
	cfg.RuntimeParams["tcp_keepalives_idle"] = "30"
	cfg.RuntimeParams["tcp_keepalives_interval"] = "10"
	cfg.RuntimeParams["tcp_keepalives_count"] = "3"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, false, err
	}
	lock := &BiniLinkWorkerLock{conn: conn}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, biniLinkWorkerLockID).Scan(&ok); err != nil || !ok {
		lock.Release()
		return nil, false, err
	}
	return lock, true, nil
}

func (l *BiniLinkWorkerLock) Held(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, BiniLinkLockQueryTimeout)
	defer cancel()
	var held bool
	err := l.conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_locks
			WHERE locktype = 'advisory' AND granted AND pid = pg_backend_pid()
			  AND classid::bigint = $1 AND objid::bigint = $2 AND objsubid = 1
		)`,
		biniLinkWorkerLockID>>32, biniLinkWorkerLockID&0xffffffff).Scan(&held)
	return err == nil && held
}

// Release ends the session, which frees the lock even if the unlock round trip would fail.
func (l *BiniLinkWorkerLock) Release() {
	ctx, cancel := context.WithTimeout(context.Background(), BiniLinkLockQueryTimeout)
	defer cancel()
	l.conn.Close(ctx)
}
