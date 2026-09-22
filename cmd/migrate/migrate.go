package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	bolt "go.etcd.io/bbolt"

	"lyrics-api-go/internal/store"
)

const (
	cacheBucketName    = "cache"
	metadataBucketName = "metadata"
	negativeKeyPrefix  = "no_lyrics:"
	defaultBatchSize   = 500
)

type Migrator struct {
	store     *store.Store
	bolt      *bolt.DB
	statsPath string
	negTTL    store.NegativeTTL
	batchSize int
}

func NewMigrator(ctx context.Context, boltPath, statsPath, dsn string) (*Migrator, func(), error) {
	db, err := bolt.Open(boltPath, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("open bolt db: %w", err)
	}

	st, err := store.New(ctx, dsn)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("connect postgres: %w", err)
	}

	if err := st.Migrate(ctx); err != nil {
		db.Close()
		st.Close()
		return nil, nil, fmt.Errorf("apply schema migrations: %w", err)
	}

	m := &Migrator{
		store:     st,
		bolt:      db,
		statsPath: statsPath,
		negTTL:    store.NegativeTTL{DefaultDays: 7, NewSongThresholdDays: 30},
		batchSize: defaultBatchSize,
	}

	cleanup := func() {
		db.Close()
		st.Close()
	}
	return m, cleanup, nil
}

type Result struct {
	LyricsInserted   int64
	NegativeInserted int64
	MetadataInserted int64
	VideoMapInserted int64
	Skipped          int
	StatsLoaded      bool
	StatsError       error
}

func (r Result) Print(w io.Writer) {
	fmt.Fprintln(w, "migration complete")
	fmt.Fprintf(w, "  lyrics inserted:    %d\n", r.LyricsInserted)
	fmt.Fprintf(w, "  negative inserted:  %d\n", r.NegativeInserted)
	fmt.Fprintf(w, "  metadata inserted:  %d\n", r.MetadataInserted)
	fmt.Fprintf(w, "  video_map inserted: %d\n", r.VideoMapInserted)
	if r.Skipped > 0 {
		fmt.Fprintf(w, "  skipped (decode errors): %d\n", r.Skipped)
	}
	switch {
	case r.StatsError != nil:
		fmt.Fprintf(w, "  stats: skipped (%v)\n", r.StatsError)
	case r.StatsLoaded:
		fmt.Fprintln(w, "  stats: loaded")
	}
}

func (m *Migrator) Run(ctx context.Context) (Result, error) {
	var res Result

	if err := m.ensureStagingTables(ctx); err != nil {
		return res, err
	}

	lyricsN, negN, skippedCache, err := m.migrateCacheBucket(ctx)
	if err != nil {
		return res, fmt.Errorf("migrate cache bucket: %w", err)
	}
	res.LyricsInserted, res.NegativeInserted = lyricsN, negN
	res.Skipped += skippedCache

	metaN, vidN, skippedMeta, err := m.migrateMetadataBucket(ctx)
	if err != nil {
		return res, fmt.Errorf("migrate metadata bucket: %w", err)
	}
	res.MetadataInserted, res.VideoMapInserted = metaN, vidN
	res.Skipped += skippedMeta

	if err := m.recomputeCounters(ctx); err != nil {
		return res, fmt.Errorf("recompute counters: %w", err)
	}

	if m.statsPath != "" {
		loaded, statsErr := m.migrateStats(ctx)
		res.StatsLoaded = loaded
		res.StatsError = statsErr
	}

	return res, nil
}

func (m *Migrator) ensureStagingTables(ctx context.Context) error {
	for _, spec := range allStagingSpecs {
		_, err := m.store.Pool().Exec(ctx, fmt.Sprintf(
			`CREATE UNLOGGED TABLE IF NOT EXISTS %s (LIKE %s INCLUDING DEFAULTS)`, spec.staging, spec.table))
		if err != nil {
			return fmt.Errorf("create staging table %s: %w", spec.staging, err)
		}
	}
	return nil
}

func (m *Migrator) migrateCacheBucket(ctx context.Context) (lyricsInserted, negInserted int64, skipped int, err error) {
	lastKey, done, err := m.loadProgress(ctx, cacheBucketName)
	if err != nil {
		return 0, 0, 0, err
	}
	if done {
		return 0, 0, 0, nil
	}

	for {
		entries, nextKey, exhausted, serr := scanBucketBatch(m.bolt, cacheBucketName, lastKey, m.batchSize)
		if serr != nil {
			return lyricsInserted, negInserted, skipped, serr
		}

		var lyricsRows, negRows [][]any
		for _, e := range entries {
			keyStr := string(e.key)
			if strings.HasPrefix(keyStr, negativeKeyPrefix) {
				row, derr := buildNegativeRow(keyStr, e.value, m.negTTL)
				if derr != nil {
					skipped++
					continue
				}
				negRows = append(negRows, row)
				continue
			}
			row, derr := buildLyricsRow(keyStr, e.value)
			if derr != nil {
				skipped++
				continue
			}
			lyricsRows = append(lyricsRows, row)
		}

		n1, n2, cerr := m.commitBatch(ctx, cacheBucketName, nextKey, exhausted, func(tx pgx.Tx) (int64, int64, error) {
			n1, err := copyAndUpsert(ctx, tx, lyricsStaging, lyricsRows)
			if err != nil {
				return 0, 0, err
			}
			n2, err := copyAndUpsert(ctx, tx, negativeStaging, negRows)
			return n1, n2, err
		})
		if cerr != nil {
			return lyricsInserted, negInserted, skipped, cerr
		}
		lyricsInserted += n1
		negInserted += n2

		if exhausted {
			break
		}
		lastKey = nextKey
	}

	return lyricsInserted, negInserted, skipped, nil
}

func (m *Migrator) migrateMetadataBucket(ctx context.Context) (metaInserted, videoInserted int64, skipped int, err error) {
	lastKey, done, err := m.loadProgress(ctx, metadataBucketName)
	if err != nil {
		return 0, 0, 0, err
	}
	if done {
		return 0, 0, 0, nil
	}

	for {
		entries, nextKey, exhausted, serr := scanBucketBatch(m.bolt, metadataBucketName, lastKey, m.batchSize)
		if serr != nil {
			return metaInserted, videoInserted, skipped, serr
		}

		var metaRows, videoRows [][]any
		for _, e := range entries {
			row, vids, derr := buildMetadataRows(string(e.key), e.value)
			if derr != nil {
				skipped++
				continue
			}
			metaRows = append(metaRows, row)
			videoRows = append(videoRows, vids...)
		}

		n1, n2, cerr := m.commitBatch(ctx, metadataBucketName, nextKey, exhausted, func(tx pgx.Tx) (int64, int64, error) {
			n1, err := copyAndUpsert(ctx, tx, metadataStaging, metaRows)
			if err != nil {
				return 0, 0, err
			}
			n2, err := copyAndUpsert(ctx, tx, videoMapStaging, videoRows)
			return n1, n2, err
		})
		if cerr != nil {
			return metaInserted, videoInserted, skipped, cerr
		}
		metaInserted += n1
		videoInserted += n2

		if exhausted {
			break
		}
		lastKey = nextKey
	}

	return metaInserted, videoInserted, skipped, nil
}

// commitBatch keeps a batch's data and its migration_progress checkpoint in one transaction.
func (m *Migrator) commitBatch(ctx context.Context, bucket string, nextKey []byte, done bool, work func(pgx.Tx) (int64, int64, error)) (int64, int64, error) {
	tx, err := m.store.Pool().Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)

	n1, n2, err := work(tx)
	if err != nil {
		return 0, 0, err
	}

	if err := upsertProgress(ctx, tx, bucket, nextKey, done); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return n1, n2, nil
}

func (m *Migrator) loadProgress(ctx context.Context, bucket string) ([]byte, bool, error) {
	var lastKey []byte
	var done bool
	err := m.store.Pool().QueryRow(ctx,
		`SELECT last_key, done FROM migration_progress WHERE bucket = $1`, bucket).Scan(&lastKey, &done)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return lastKey, done, nil
}

func upsertProgress(ctx context.Context, tx pgx.Tx, bucket string, lastKey []byte, done bool) error {
	if lastKey == nil {
		lastKey = []byte{}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO migration_progress (bucket, last_key, done) VALUES ($1, $2, $3)
		ON CONFLICT (bucket) DO UPDATE SET last_key = EXCLUDED.last_key, done = EXCLUDED.done`,
		bucket, lastKey, done)
	return err
}

func (m *Migrator) recomputeCounters(ctx context.Context) error {
	_, err := m.store.Pool().Exec(ctx, `
		INSERT INTO counters (prefix, count)
		SELECT provider, count(*) FROM lyrics GROUP BY provider
		ON CONFLICT (prefix) DO UPDATE SET count = EXCLUDED.count`)
	return err
}

func (m *Migrator) migrateStats(ctx context.Context) (bool, error) {
	db, err := bolt.Open(m.statsPath, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		return false, fmt.Errorf("open stats db: %w", err)
	}
	defer db.Close()

	var data []byte
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("stats"))
		if b == nil {
			return fmt.Errorf("stats bucket not found")
		}
		v := b.Get([]byte("server_stats"))
		if v == nil {
			return fmt.Errorf("server_stats key not found")
		}
		data = append([]byte(nil), v...)
		return nil
	})
	if err != nil {
		return false, err
	}

	if !json.Valid(data) {
		return false, fmt.Errorf("stats payload is not valid json")
	}

	if err := m.store.SaveStats(ctx, json.RawMessage(data)); err != nil {
		return false, fmt.Errorf("save stats: %w", err)
	}
	return true, nil
}

func buildLyricsRow(cacheKey string, raw []byte) ([]any, error) {
	inner, err := decodeCacheEntryValue(raw)
	if err != nil {
		return nil, err
	}
	cl := decodePositiveLyrics(inner)
	blob, err := gzipString(cl.TTML)
	if err != nil {
		return nil, err
	}
	k := deriveKey(cacheKey)
	return []any{cacheKey, k.Provider, k.BaseKey, k.DurationSec, blob, cl.TrackDurationMs, cl.Score, cl.Language, cl.IsRTL, k.Provider}, nil
}

func buildNegativeRow(negKey string, raw []byte, cfg store.NegativeTTL) ([]any, error) {
	ck := strings.TrimPrefix(negKey, negativeKeyPrefix)
	inner, err := decodeCacheEntryValue(raw)
	if err != nil {
		return nil, err
	}
	entry, err := decodeNegativeEntry(inner)
	if err != nil {
		return nil, err
	}
	k := deriveKey(ck)

	ttl := store.NegativeTTLSeconds(store.NegativeEntry{
		Reason:             entry.Reason,
		ReleaseDate:        entry.ReleaseDate,
		HasTimeSyncedKnown: entry.HasTimeSyncedLyricsKnown,
	}, cfg, time.Now())

	createdAt := time.Unix(entry.Timestamp, 0).UTC()
	expiresAt := time.Unix(entry.Timestamp+ttl, 0).UTC()

	return []any{ck, k.Provider, k.BaseKey, k.DurationSec, entry.Reason, entry.ReleaseDate, entry.HasTimeSyncedLyricsKnown, createdAt, expiresAt}, nil
}

func buildMetadataRows(cacheKey string, raw []byte) ([]any, [][]any, error) {
	meta, err := decodeMetadataValue(raw)
	if err != nil {
		return nil, nil, err
	}

	var rawAttrs any
	if meta.RawAttributes != "" {
		rawAttrs = meta.RawAttributes
	}

	row := []any{
		cacheKey, meta.AppleTrackID, meta.ISRC, meta.TrackName, meta.ArtistName,
		meta.AlbumName, meta.DurationMs, meta.ReleaseDate, rawAttrs,
		time.Unix(meta.FirstSeen, 0).UTC(), time.Unix(meta.LastUpdated, 0).UTC(),
	}

	seen := make(map[string]bool, len(meta.VideoIDs))
	var videoRows [][]any
	for _, v := range meta.VideoIDs {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		videoRows = append(videoRows, []any{v, cacheKey})
	}

	return row, videoRows, nil
}
