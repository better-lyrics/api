package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type NegativeEntry struct {
	Reason             string
	ReleaseDate        string
	HasTimeSyncedKnown bool
}

type NegativeTTL struct {
	DefaultDays          int
	NewSongThresholdDays int
}

func NegativeTTLSeconds(e NegativeEntry, cfg NegativeTTL, now time.Time) int64 {
	return negativeTTLSeconds(e, cfg, now)
}

func negativeTTLSeconds(e NegativeEntry, cfg NegativeTTL, now time.Time) int64 {
	defaultTTL := int64(cfg.DefaultDays) * 24 * 60 * 60

	if !e.HasTimeSyncedKnown {
		return defaultTTL
	}
	if e.ReleaseDate == "" {
		return defaultTTL
	}
	rd, err := time.Parse("2006-01-02", e.ReleaseDate)
	if err != nil {
		return defaultTTL
	}

	u := now.UTC()
	today := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	daysSinceRelease := int(today.Sub(rd).Hours() / 24)

	if daysSinceRelease >= cfg.NewSongThresholdDays {
		return defaultTTL
	}
	switch {
	case daysSinceRelease <= 3:
		return 6 * 60 * 60
	case daysSinceRelease <= 7:
		return 12 * 60 * 60
	case daysSinceRelease <= 14:
		return 24 * 60 * 60
	default:
		return 3 * 24 * 60 * 60
	}
}

func (s *Store) SetNegative(ctx context.Context, cacheKey string, k Key, e NegativeEntry, cfg NegativeTTL) error {
	ttl := negativeTTLSeconds(e, cfg, time.Now())
	expiresAt := time.Now().Add(time.Duration(ttl) * time.Second)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO negative_cache (cache_key, provider, base_key, duration_sec,
		                            reason, release_date, has_time_synced_known, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $8)
		ON CONFLICT (cache_key) DO UPDATE SET
			reason                = EXCLUDED.reason,
			release_date          = EXCLUDED.release_date,
			has_time_synced_known = EXCLUDED.has_time_synced_known,
			created_at            = now(),
			expires_at            = EXCLUDED.expires_at`,
		cacheKey, k.Provider, k.BaseKey, k.DurationSec,
		e.Reason, e.ReleaseDate, e.HasTimeSyncedKnown, expiresAt)
	return err
}

func (s *Store) GetNegative(ctx context.Context, cacheKey string) (string, bool, error) {
	var reason string
	err := s.pool.QueryRow(ctx, `
		SELECT reason FROM negative_cache WHERE cache_key = $1 AND expires_at > now()`, cacheKey).
		Scan(&reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return reason, true, nil
}

func (s *Store) GetNegativeTolerant(ctx context.Context, k Key, deltaSec int) (string, string, bool, error) {
	if k.DurationSec == nil {
		return "", "", false, nil
	}
	if deltaSec < 1 {
		deltaSec = 1
	}
	target := *k.DurationSec

	var reason, cacheKey string
	err := s.pool.QueryRow(ctx, `
		SELECT cache_key, reason FROM negative_cache
		WHERE base_key = $1
		  AND duration_sec IS NOT NULL
		  AND duration_sec BETWEEN $2 AND $3
		  AND expires_at > now()
		ORDER BY abs(duration_sec - $4), duration_sec ASC
		LIMIT 1`,
		k.BaseKey, target-deltaSec, target+deltaSec, target).
		Scan(&cacheKey, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return reason, cacheKey, true, nil
}

func (s *Store) DeleteNegative(ctx context.Context, cacheKey string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM negative_cache WHERE cache_key = $1`, cacheKey)
	return err
}

// PurgeExpiredNegative deletes up to limit expired rows, oldest first; the ORDER BY keeps Postgres on the expires_at index instead of rescanning the heap.
func (s *Store) PurgeExpiredNegative(ctx context.Context, limit int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM negative_cache WHERE expires_at < now() AND ctid IN (
			SELECT ctid FROM negative_cache WHERE expires_at < now() ORDER BY expires_at LIMIT $1
		)`, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PurgeAllExpiredNegative pauses between batches so a large backlog never holds long locks or saturates IO.
func (s *Store) PurgeAllExpiredNegative(ctx context.Context, batchSize int, pause time.Duration) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := s.PurgeExpiredNegative(ctx, batchSize)
		total += n
		if err != nil {
			return total, err
		}
		if n < int64(batchSize) {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(pause):
		}
	}
}
