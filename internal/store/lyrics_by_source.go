package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type SourcedRow struct {
	CacheKey string
	ISRC     string
}

// source has no index, so each page walks the primary key and filters to keep every query bounded.
func (s *Store) ScanLyricsBySource(ctx context.Context, source, after string, pageSize int) (rows []SourcedRow, next string, err error) {
	q, err := s.pool.Query(ctx, `
		WITH page AS (
			SELECT cache_key, source FROM lyrics
			WHERE cache_key > $1
			ORDER BY cache_key
			LIMIT $2
		)
		SELECT p.cache_key, p.source = $3, coalesce(m.isrc, '')
		FROM page p
		LEFT JOIN song_metadata m ON m.cache_key = p.cache_key AND p.source = $3
		ORDER BY p.cache_key`,
		after, pageSize, source)
	if err != nil {
		return nil, "", err
	}
	defer q.Close()

	scanned := 0
	for q.Next() {
		var r SourcedRow
		var matches bool
		if err := q.Scan(&r.CacheKey, &matches, &r.ISRC); err != nil {
			return nil, "", err
		}
		scanned++
		next = r.CacheKey
		if matches {
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

// DeleteLyricsIfUnchanged locks the row and deletes it only if it still matches what the caller compared, so a concurrent rewrite survives.
func (s *Store) DeleteLyricsIfUnchanged(ctx context.Context, cacheKey, source, ttml string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var provider string
	var blob []byte
	err = tx.QueryRow(ctx, `SELECT provider, raw_lyrics FROM lyrics WHERE cache_key = $1 AND source = $2 FOR UPDATE`, cacheKey, source).Scan(&provider, &blob)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current, err := gunzipBytes(blob)
	if err != nil {
		return false, err
	}
	if current != ttml {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM lyrics WHERE cache_key = $1`, cacheKey); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE counters SET count = count - 1 WHERE prefix = $1 AND count > 0`, provider); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
