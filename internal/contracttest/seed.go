package contracttest

import (
	"encoding/json"
	"strings"
	"time"

	"lyrics-api-go/cache"
	"lyrics-api-go/utils"

	bolt "go.etcd.io/bbolt"
)

type SeedLyrics struct {
	Song            string
	Artist          string
	Album           string
	Duration        string
	TTML            string
	TrackDurationMs int
	Score           float64
	Language        string
	IsRTL           bool
}

type SeedNegative struct {
	Song                     string
	Artist                   string
	Album                    string
	Duration                 string
	Reason                   string
	ReleaseDate              string
	HasTimeSyncedLyricsKnown bool
}

type cachedLyricsJSON struct {
	TTML            string  `json:"ttml"`
	TrackDurationMs int     `json:"trackDurationMs"`
	Score           float64 `json:"score,omitempty"`
	Language        string  `json:"language,omitempty"`
	IsRTL           bool    `json:"isRTL,omitempty"`
}

type negativeEntryJSON struct {
	Reason                   string `json:"reason"`
	Timestamp                int64  `json:"timestamp"`
	ReleaseDate              string `json:"releaseDate,omitempty"`
	HasTimeSyncedLyricsKnown bool   `json:"hasTimeSyncedLyricsKnown,omitempty"`
}

func normalizedCacheKey(song, artist, album, duration string) string {
	s := strings.ToLower(strings.TrimSpace(song))
	a := strings.ToLower(strings.TrimSpace(artist))
	al := strings.ToLower(strings.TrimSpace(album))
	q := s + " " + a
	if al != "" {
		q += " " + al
	}
	if duration != "" {
		q += " " + duration + "s"
	}
	return "ttml_lyrics:" + q
}

func seedCacheDB(path string, lyrics []SeedLyrics, negatives []SeedNegative) error {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("cache"))
		if err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte("counters")); err != nil {
			return err
		}

		put := func(key, logical string) error {
			compressed, err := utils.CompressString(logical)
			if err != nil {
				return err
			}
			data, err := json.Marshal(cache.CacheEntry{Value: compressed})
			if err != nil {
				return err
			}
			return b.Put([]byte(key), data)
		}

		for _, l := range lyrics {
			key := normalizedCacheKey(l.Song, l.Artist, l.Album, l.Duration)
			logical, err := json.Marshal(cachedLyricsJSON{
				TTML:            l.TTML,
				TrackDurationMs: l.TrackDurationMs,
				Score:           l.Score,
				Language:        l.Language,
				IsRTL:           l.IsRTL,
			})
			if err != nil {
				return err
			}
			if err := put(key, string(logical)); err != nil {
				return err
			}
		}

		for _, n := range negatives {
			key := "no_lyrics:" + normalizedCacheKey(n.Song, n.Artist, n.Album, n.Duration)
			logical, err := json.Marshal(negativeEntryJSON{
				Reason:                   n.Reason,
				Timestamp:                time.Now().Unix(),
				ReleaseDate:              n.ReleaseDate,
				HasTimeSyncedLyricsKnown: n.HasTimeSyncedLyricsKnown,
			})
			if err != nil {
				return err
			}
			if err := put(key, string(logical)); err != nil {
				return err
			}
		}
		return nil
	})
}
