package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *Store) Counts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT prefix, count FROM counters`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var prefix string
		var count int64
		if err := rows.Scan(&prefix, &count); err != nil {
			return nil, err
		}
		out[prefix] = count
	}
	return out, rows.Err()
}

func (s *Store) LoadStats(ctx context.Context) (json.RawMessage, error) {
	var data json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT data FROM stats WHERE id = 1`).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Store) SaveStats(ctx context.Context, data json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO stats (id, data, updated_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`, data)
	return err
}

func (s *Store) GetStorefront(ctx context.Context, mutHash string) (string, bool, error) {
	var code string
	err := s.pool.QueryRow(ctx, `SELECT storefront FROM storefront_cache WHERE mut_hash = $1`, mutHash).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return code, true, nil
}

func (s *Store) SetStorefront(ctx context.Context, mutHash, code string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO storefront_cache (mut_hash, storefront, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (mut_hash) DO UPDATE SET storefront = EXCLUDED.storefront, updated_at = now()`,
		mutHash, code)
	return err
}
