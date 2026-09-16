package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"

	bolt "go.etcd.io/bbolt"
)

const spotCheckSampleSize = 20

type providerCount struct {
	postgres int64
	bolt     int64
}

type VerifyResult struct {
	Passed      bool
	Providers   map[string]providerCount
	SpotChecked int
	SpotFailed  []string
	Errors      []string
}

func (r VerifyResult) Print(w io.Writer) {
	if r.Passed {
		fmt.Fprintln(w, "VERIFY: PASS")
	} else {
		fmt.Fprintln(w, "VERIFY: FAIL")
	}
	fmt.Fprintf(w, "  spot-checked: %d, failed: %d\n", r.SpotChecked, len(r.SpotFailed))
	for provider, c := range r.Providers {
		if c.postgres != c.bolt {
			fmt.Fprintf(w, "  provider %s: postgres=%d bolt=%d MISMATCH\n", provider, c.postgres, c.bolt)
		}
	}
	for _, key := range r.SpotFailed {
		fmt.Fprintf(w, "  spot-check failed: %s\n", key)
	}
	for _, e := range r.Errors {
		fmt.Fprintf(w, "  error: %s\n", e)
	}
}

func (m *Migrator) Verify(ctx context.Context) (VerifyResult, error) {
	res := VerifyResult{Providers: map[string]providerCount{}}

	pgCounts, err := m.pgProviderCounts(ctx)
	if err != nil {
		return res, fmt.Errorf("query postgres counts: %w", err)
	}

	boltCounts, positiveKeys, err := m.boltProviderCountsAndKeys()
	if err != nil {
		return res, fmt.Errorf("rescan bolt cache bucket: %w", err)
	}

	for provider, c := range pgCounts {
		entry := res.Providers[provider]
		entry.postgres = c
		res.Providers[provider] = entry
	}
	for provider, c := range boltCounts {
		entry := res.Providers[provider]
		entry.bolt = c
		res.Providers[provider] = entry
	}

	mismatch := false
	for _, c := range res.Providers {
		if c.postgres != c.bolt {
			mismatch = true
		}
	}

	for _, key := range sampleKeys(positiveKeys, spotCheckSampleSize) {
		res.SpotChecked++
		ok, err := m.spotCheckKey(ctx, key)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		if !ok {
			res.SpotFailed = append(res.SpotFailed, key)
		}
	}

	res.Passed = !mismatch && len(res.SpotFailed) == 0 && len(res.Errors) == 0
	return res, nil
}

func (m *Migrator) pgProviderCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := m.store.Pool().Query(ctx, `SELECT provider, count(*) FROM lyrics GROUP BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var provider string
		var count int64
		if err := rows.Scan(&provider, &count); err != nil {
			return nil, err
		}
		out[provider] = count
	}
	return out, rows.Err()
}

func (m *Migrator) boltProviderCountsAndKeys() (map[string]int64, []string, error) {
	counts := make(map[string]int64)
	var keys []string

	err := m.bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(cacheBucketName))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			keyStr := string(k)
			if strings.HasPrefix(keyStr, negativeKeyPrefix) {
				return nil
			}
			pk := deriveKey(keyStr)
			counts[pk.Provider]++
			keys = append(keys, keyStr)
			return nil
		})
	})
	return counts, keys, err
}

func (m *Migrator) spotCheckKey(ctx context.Context, cacheKey string) (bool, error) {
	var raw []byte
	err := m.bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(cacheBucketName))
		if b == nil {
			return fmt.Errorf("cache bucket missing")
		}
		v := b.Get([]byte(cacheKey))
		if v == nil {
			return fmt.Errorf("key missing from bolt")
		}
		raw = append([]byte(nil), v...)
		return nil
	})
	if err != nil {
		return false, err
	}

	inner, err := decodeCacheEntryValue(raw)
	if err != nil {
		return false, err
	}
	want := decodePositiveLyrics(inner)

	var blob []byte
	if err := m.store.Pool().QueryRow(ctx,
		`SELECT raw_lyrics FROM lyrics WHERE cache_key = $1`, cacheKey).Scan(&blob); err != nil {
		return false, err
	}
	got, err := gunzipString(blob)
	if err != nil {
		return false, err
	}

	return got == want.TTML, nil
}

func sampleKeys(keys []string, n int) []string {
	if len(keys) <= n {
		return keys
	}
	reservoir := make([]string, n)
	copy(reservoir, keys[:n])
	for i := n; i < len(keys); i++ {
		j := rand.IntN(i + 1)
		if j < n {
			reservoir[j] = keys[i]
		}
	}
	return reservoir
}
