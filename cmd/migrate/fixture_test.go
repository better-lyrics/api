package main

import (
	"encoding/json"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"lyrics-api-go/cache"
	"lyrics-api-go/utils"
)

type fixtureKeys struct {
	normalizedKey   string
	normalizedTTML  string
	bracketedKey    string
	bracketedTTML   string
	plainKey        string
	plainTTML       string
	sentinelKey     string
	negativeCK      string
	negativeReason  string
	negativeTS      int64
	metadataKey     string
	metadataVideoID []string
}

func putCacheEntry(t *testing.T, b *bolt.Bucket, key, plainValue string) {
	t.Helper()
	compressed, err := utils.CompressString(plainValue)
	if err != nil {
		t.Fatalf("compress %q: %v", key, err)
	}
	raw, err := json.Marshal(cache.CacheEntry{Value: compressed})
	if err != nil {
		t.Fatalf("marshal cache entry %q: %v", key, err)
	}
	if err := b.Put([]byte(key), raw); err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
}

func putMetadataEntry(t *testing.T, b *bolt.Bucket, key, plainJSON string) {
	t.Helper()
	compressed, err := utils.CompressString(plainJSON)
	if err != nil {
		t.Fatalf("compress metadata %q: %v", key, err)
	}
	if err := b.Put([]byte(key), []byte(compressed)); err != nil {
		t.Fatalf("put metadata %q: %v", key, err)
	}
}

// buildFixture writes a synthetic legacy cache.db covering every on-disk shape the migrator must handle.
func buildFixture(t *testing.T, path string) fixtureKeys {
	t.Helper()

	fx := fixtureKeys{
		normalizedKey:  "ttml_lyrics:song one artist one 200s",
		normalizedTTML: "<tt>normalized one</tt>",
		bracketedKey:   "kugou_lyrics:song two artist two [180s]",
		bracketedTTML:  "<tt>bracketed two</tt>",
		plainKey:       "legacy_lyrics:song three artist three",
		plainTTML:      "<tt>plain three</tt>",
		sentinelKey:    "ttml_lyrics:song five artist five",
		negativeCK:     "ttml_lyrics:song four artist four 150s",
		negativeReason: "no lyrics data found",
		negativeTS:     time.Now().Add(-48 * time.Hour).Unix(),
		metadataKey:    "ttml_lyrics:song one artist one 200s",
		metadataVideoID: []string{
			"vid1", "vid2",
		},
	}

	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatalf("open fixture bolt db: %v", err)
	}
	defer db.Close()

	err = db.Update(func(tx *bolt.Tx) error {
		cacheB, err := tx.CreateBucketIfNotExists([]byte(cacheBucketName))
		if err != nil {
			return err
		}
		metaB, err := tx.CreateBucketIfNotExists([]byte(metadataBucketName))
		if err != nil {
			return err
		}
		idxB, err := tx.CreateBucketIfNotExists([]byte("indexes"))
		if err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte("counters")); err != nil {
			return err
		}

		putCacheEntry(t, cacheB, fx.normalizedKey, mustMarshalCachedLyrics(t, cachedLyrics{
			TTML: fx.normalizedTTML, TrackDurationMs: 200000, Score: 0.91, Language: "en",
		}))
		putCacheEntry(t, cacheB, fx.bracketedKey, mustMarshalCachedLyrics(t, cachedLyrics{
			TTML: fx.bracketedTTML, TrackDurationMs: 180000, Score: 0.5, Language: "zh",
		}))
		putCacheEntry(t, cacheB, fx.plainKey, fx.plainTTML)
		putCacheEntry(t, cacheB, fx.sentinelKey, noLyricsSentinel)

		negEntry := negativeCacheEntry{
			Reason:                   fx.negativeReason,
			Timestamp:                fx.negativeTS,
			ReleaseDate:              "2020-01-01",
			HasTimeSyncedLyricsKnown: true,
		}
		negData, err := json.Marshal(negEntry)
		if err != nil {
			return err
		}
		putCacheEntry(t, cacheB, negativeKeyPrefix+fx.negativeCK, string(negData))

		meta := songMetadata{
			CacheKey:      fx.metadataKey,
			VideoIDs:      fx.metadataVideoID,
			AppleTrackID:  "1234567",
			ISRC:          "USABC1234567",
			TrackName:     "Song One",
			ArtistName:    "Artist One",
			AlbumName:     "Album One",
			DurationMs:    200000,
			ReleaseDate:   "2024-01-01",
			RawAttributes: `{"foo":"bar"}`,
			FirstSeen:     time.Now().Add(-72 * time.Hour).Unix(),
			LastUpdated:   time.Now().Add(-1 * time.Hour).Unix(),
		}
		metaData, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		putMetadataEntry(t, metaB, fx.metadataKey, string(metaData))

		if err := idxB.Put([]byte("video:vid1"), []byte(`["`+fx.metadataKey+`"]`)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed fixture bolt db: %v", err)
	}

	return fx
}

func mustMarshalCachedLyrics(t *testing.T, cl cachedLyrics) string {
	t.Helper()
	data, err := json.Marshal(cl)
	if err != nil {
		t.Fatalf("marshal cached lyrics: %v", err)
	}
	return string(data)
}
