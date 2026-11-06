package store

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

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

// SeedBiniLinkBackfill enqueues one page of historical videos that biniLinkBackfillTrack resolves.
func (s *Store) SeedBiniLinkBackfill(ctx context.Context, after string, pageSize, durationDeltaMs int) (seeded int, next string, err error) {
	rows, err := s.pool.Query(ctx, `
		WITH page AS (
			SELECT DISTINCT video_id FROM video_map
			WHERE video_id > $1
			ORDER BY video_id
			LIMIT $2
		)
		SELECT p.video_id, coalesce(v.cache_key, ''), coalesce(m.apple_track_id, ''), coalesce(m.isrc, ''), coalesce(m.duration_ms, 0),
			coalesce(m.track_name, ''), coalesce(m.artist_name, ''), coalesce(m.album_name, '')
		FROM page p
		LEFT JOIN video_map v ON v.video_id = p.video_id AND p.video_id ~ $3
		LEFT JOIN song_metadata m ON m.cache_key = v.cache_key
		ORDER BY p.video_id`,
		after, pageSize, biniLinkVideoIDPattern)
	if err != nil {
		return 0, "", err
	}
	keysByVideo := map[string][]biniLinkKey{}
	var order []string
	for rows.Next() {
		var videoID string
		var k biniLinkKey
		if err := rows.Scan(&videoID, &k.CacheKey, &k.AppleTrackID, &k.ISRC, &k.DurationMs, &k.TrackName, &k.ArtistName, &k.AlbumName); err != nil {
			rows.Close()
			return 0, "", err
		}
		if _, seen := keysByVideo[videoID]; !seen {
			order = append(order, videoID)
		}
		keysByVideo[videoID] = append(keysByVideo[videoID], k)
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}

	var videoIDs, trackIDs []string
	for _, v := range order {
		if track, ok := biniLinkBackfillTrack(keysByVideo[v], durationDeltaMs); ok {
			videoIDs = append(videoIDs, v)
			trackIDs = append(trackIDs, track)
		}
	}
	if len(videoIDs) > 0 {
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO binilink_links (video_id, apple_track_id)
			SELECT * FROM unnest($1::text[], $2::text[])
			ON CONFLICT DO NOTHING`,
			videoIDs, trackIDs)
		if err != nil {
			return 0, "", err
		}
		seeded = int(tag.RowsAffected())
	}

	if len(order) < pageSize {
		return seeded, "", nil
	}
	return seeded, order[len(order)-1], nil
}

type biniLinkKey struct {
	CacheKey     string
	AppleTrackID string
	ISRC         string
	DurationMs   int
	TrackName    string
	ArtistName   string
	AlbumName    string
}

// biniLinkBackfillTrack skips a video when any key without a track ID may be another song or version.
func biniLinkBackfillTrack(keys []biniLinkKey, durationDeltaMs int) (string, bool) {
	var resolved, ignored []biniLinkKey
	for _, k := range keys {
		switch {
		case biniLinkAppleTrackID.MatchString(k.AppleTrackID):
			resolved = append(resolved, k)
		case k.AppleTrackID != "":
			return "", false
		default:
			ignored = append(ignored, k)
		}
	}
	if len(resolved) == 0 {
		return "", false
	}
	track := resolved[0].AppleTrackID
	for _, r := range resolved {
		if r.ISRC == "" || r.ISRC != resolved[0].ISRC {
			return "", false
		}
		if r.AppleTrackID < track {
			track = r.AppleTrackID
		}
	}
	for _, i := range ignored {
		if i.ISRC != "" && i.ISRC != resolved[0].ISRC {
			return "", false
		}
		if !slices.ContainsFunc(resolved, func(r biniLinkKey) bool { return sameRecording(i, r, durationDeltaMs) }) {
			return "", false
		}
	}
	return track, true
}

// sameRecording rejects any word unknown for the track, so another artist, "remix" or "re-recorded" blocks.
func sameRecording(ignored, resolved biniLinkKey, durationDeltaMs int) bool {
	ik := DeriveKey(ignored.CacheKey)
	if ik.DurationSec == nil || *ik.DurationSec == 0 || resolved.DurationMs == 0 {
		return false
	}
	if diff := *ik.DurationSec*1000 - resolved.DurationMs; diff < -durationDeltaMs || diff > durationDeltaMs {
		return false
	}
	_, query, _ := strings.Cut(ik.BaseKey, ":")
	title := keyWords(biniLinkFeatBracket.ReplaceAllString(resolved.TrackName, " "))
	if len(title) == 0 || !startsWithTitle(keyWords(query), title, keyWords(resolved.ArtistName)) {
		return false
	}
	known := keyWords(DeriveKey(resolved.CacheKey).BaseKey + " " + resolved.TrackName + " " + resolved.ArtistName + " " + resolved.AlbumName)
	return !slices.ContainsFunc(keyWords(ik.BaseKey), func(w string) bool {
		return !slices.Contains(known, w) && !slices.Contains(biniLinkJoinerWords, w)
	})
}

// startsWithTitle anchors the title where keys put it and needs the artist next, so "glass and ash" never stands in for "glass".
func startsWithTitle(query, title, artist []string) bool {
	if len(query) <= len(title) || !slices.Equal(query[:len(title)], title) {
		return false
	}
	next := query[len(title)]
	return slices.Contains(artist, next) || next == "feat" || next == "ft" || next == "featuring"
}

// Only featured-artist brackets are optional; "(Live)" or "(Taylor's Version)" names another recording.
var biniLinkFeatBracket = regexp.MustCompile(`(?i)[(\[](feat|ft|featuring)\b[^)\]]*[)\]]`)

// Words that join artist names in localized keys ("a y b", "a 和 b") and carry no recording identity.
var biniLinkJoinerWords = []string{"and", "y", "e", "et", "und", "feat", "ft", "featuring", "con", "avec", "mit", "x", "和", "và", "ve", "cu", "și", "и", "oraz", "with", "single", "ep"}

func keyWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.In(r, unicode.L, unicode.M, unicode.N) })
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
