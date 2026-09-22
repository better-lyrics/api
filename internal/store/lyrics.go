package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Key struct {
	Provider    string
	BaseKey     string
	DurationSec *int
}

type CachedLyrics struct {
	TTML            string
	TrackDurationMs int
	Score           float64
	Language        string
	IsRTL           bool
	Format          string
	Source          string // upstream provenance: "apple", "lrc.red", or "" when unknown
	TimingType      string
	AppleETag       string
	LastCheckedAt   *time.Time
}

func (s *Store) SetLyrics(ctx context.Context, cacheKey string, k Key, l CachedLyrics) error {
	blob, err := gzipBytes(l.TTML)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO lyrics (cache_key, provider, base_key, duration_sec,
		                    raw_lyrics, track_duration_ms, score, language, is_rtl, format, source,
		                    timing_type, apple_etag, last_checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (cache_key) DO UPDATE SET
			raw_lyrics        = EXCLUDED.raw_lyrics,
			track_duration_ms = EXCLUDED.track_duration_ms,
			score             = EXCLUDED.score,
			language          = EXCLUDED.language,
			is_rtl            = EXCLUDED.is_rtl,
			format            = EXCLUDED.format,
			source            = EXCLUDED.source,
			timing_type       = EXCLUDED.timing_type,
			apple_etag        = EXCLUDED.apple_etag,
			last_checked_at   = EXCLUDED.last_checked_at,
			updated_at        = now()
		RETURNING (xmax = 0)`,
		cacheKey, k.Provider, k.BaseKey, k.DurationSec,
		blob, l.TrackDurationMs, l.Score, l.Language, l.IsRTL, l.Format, l.Source,
		l.TimingType, l.AppleETag, l.LastCheckedAt,
	).Scan(&inserted)
	if err != nil {
		return err
	}

	if inserted {
		if _, err := tx.Exec(ctx, `
			INSERT INTO counters (prefix, count) VALUES ($1, 1)
			ON CONFLICT (prefix) DO UPDATE SET count = counters.count + 1`,
			k.Provider); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) GetLyricsExact(ctx context.Context, cacheKey string) (CachedLyrics, bool, error) {
	var blob []byte
	var l CachedLyrics
	err := s.pool.QueryRow(ctx, `
		SELECT raw_lyrics, track_duration_ms, score, language, is_rtl, format, source,
		       timing_type, apple_etag, last_checked_at
		FROM lyrics WHERE cache_key = $1`, cacheKey).
		Scan(&blob, &l.TrackDurationMs, &l.Score, &l.Language, &l.IsRTL, &l.Format, &l.Source,
			&l.TimingType, &l.AppleETag, &l.LastCheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CachedLyrics{}, false, nil
	}
	if err != nil {
		return CachedLyrics{}, false, err
	}
	l.TTML, err = gunzipBytes(blob)
	if err != nil {
		return CachedLyrics{}, false, err
	}
	return l, true, nil
}

func (s *Store) GetLyricsTolerant(ctx context.Context, k Key, deltaSec int) (CachedLyrics, string, bool, error) {
	if k.DurationSec == nil {
		return CachedLyrics{}, "", false, nil
	}
	if deltaSec < 1 {
		deltaSec = 1
	}
	target := *k.DurationSec

	var cacheKey string
	var blob []byte
	var l CachedLyrics
	err := s.pool.QueryRow(ctx, `
		SELECT cache_key, raw_lyrics, track_duration_ms, score, language, is_rtl, format, source,
		       timing_type, apple_etag, last_checked_at
		FROM lyrics
		WHERE base_key = $1
		  AND duration_sec IS NOT NULL
		  AND duration_sec BETWEEN $2 AND $3
		ORDER BY abs(duration_sec - $4), duration_sec ASC
		LIMIT 1`,
		k.BaseKey, target-deltaSec, target+deltaSec, target).
		Scan(&cacheKey, &blob, &l.TrackDurationMs, &l.Score, &l.Language, &l.IsRTL, &l.Format, &l.Source,
			&l.TimingType, &l.AppleETag, &l.LastCheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CachedLyrics{}, "", false, nil
	}
	if err != nil {
		return CachedLyrics{}, "", false, err
	}
	l.TTML, err = gunzipBytes(blob)
	if err != nil {
		return CachedLyrics{}, "", false, err
	}
	return l, cacheKey, true, nil
}
