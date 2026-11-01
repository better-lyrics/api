package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type LRCRedRow struct {
	CacheKey string
	ISRC     string
}

// ScanLRCRedLyrics returns the lrc.red rows among the next pageSize rows after the cursor; source has no index, so each page walks the primary key. next is "" at the end.
func (s *Store) ScanLRCRedLyrics(ctx context.Context, after string, pageSize int) (rows []LRCRedRow, next string, err error) {
	q, err := s.pool.Query(ctx, `
		WITH page AS (
			SELECT cache_key, source FROM lyrics
			WHERE cache_key > $1
			ORDER BY cache_key
			LIMIT $2
		)
		SELECT p.cache_key, p.source = 'lrc.red', coalesce(m.isrc, '')
		FROM page p
		LEFT JOIN song_metadata m ON m.cache_key = p.cache_key
		ORDER BY p.cache_key`,
		after, pageSize)
	if err != nil {
		return nil, "", err
	}
	defer q.Close()

	scanned := 0
	for q.Next() {
		var r LRCRedRow
		var fromLRCRed bool
		if err := q.Scan(&r.CacheKey, &fromLRCRed, &r.ISRC); err != nil {
			return nil, "", err
		}
		scanned++
		next = r.CacheKey
		if fromLRCRed {
			rows = append(rows, r)
		}
	}
	if err := q.Err(); err != nil {
		return nil, "", err
	}
	if scanned < pageSize {
		next = ""
	}
	return rows, next, nil
}

// DeleteLRCRedLyrics deletes a row only while it is still sourced from lrc.red, so a concurrent Apple rewrite survives.
func (s *Store) DeleteLRCRedLyrics(ctx context.Context, cacheKey string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var provider string
	err = tx.QueryRow(ctx, `DELETE FROM lyrics WHERE cache_key = $1 AND source = 'lrc.red' RETURNING provider`, cacheKey).Scan(&provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE counters SET count = count - 1 WHERE prefix = $1 AND count > 0`, provider); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
