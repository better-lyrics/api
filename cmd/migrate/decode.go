package main

import (
	"encoding/json"
	"fmt"

	"lyrics-api-go/cache"
	"lyrics-api-go/utils"
)

const noLyricsSentinel = "__NO_LYRICS__"

type cachedLyrics struct {
	TTML            string  `json:"ttml"`
	TrackDurationMs int     `json:"trackDurationMs"`
	Score           float64 `json:"score,omitempty"`
	Language        string  `json:"language,omitempty"`
	IsRTL           bool    `json:"isRTL,omitempty"`
}

type negativeCacheEntry struct {
	Reason                   string `json:"reason"`
	Timestamp                int64  `json:"timestamp"`
	ReleaseDate              string `json:"releaseDate,omitempty"`
	HasTimeSyncedLyricsKnown bool   `json:"hasTimeSyncedLyricsKnown,omitempty"`
}

type songMetadata struct {
	CacheKey      string   `json:"cacheKey"`
	VideoIDs      []string `json:"videoIds,omitempty"`
	AppleTrackID  string   `json:"appleTrackId,omitempty"`
	ISRC          string   `json:"isrc,omitempty"`
	TrackName     string   `json:"trackName"`
	ArtistName    string   `json:"artistName"`
	AlbumName     string   `json:"albumName,omitempty"`
	DurationMs    int      `json:"durationMs,omitempty"`
	ReleaseDate   string   `json:"releaseDate,omitempty"`
	RawAttributes string   `json:"rawAttributes,omitempty"`
	FirstSeen     int64    `json:"firstSeen"`
	LastUpdated   int64    `json:"lastUpdated"`
}

func decodeCacheEntryValue(raw []byte) (string, error) {
	var entry cache.CacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", fmt.Errorf("unmarshal cache entry: %w", err)
	}
	decompressed, err := utils.DecompressString(entry.Value)
	if err != nil {
		return "", fmt.Errorf("decompress cache entry: %w", err)
	}
	return decompressed, nil
}

func decodePositiveLyrics(inner string) cachedLyrics {
	var cl cachedLyrics
	if err := json.Unmarshal([]byte(inner), &cl); err == nil && cl.TTML != "" {
		return cl
	}
	return cachedLyrics{TTML: inner}
}

func decodeNegativeEntry(inner string) (negativeCacheEntry, error) {
	var e negativeCacheEntry
	if err := json.Unmarshal([]byte(inner), &e); err != nil {
		return negativeCacheEntry{}, fmt.Errorf("unmarshal negative cache entry: %w", err)
	}
	return e, nil
}

func decodeMetadataValue(raw []byte) (songMetadata, error) {
	decompressed, err := utils.DecompressString(string(raw))
	if err != nil {
		return songMetadata{}, fmt.Errorf("decompress metadata: %w", err)
	}
	var m songMetadata
	if err := json.Unmarshal([]byte(decompressed), &m); err != nil {
		return songMetadata{}, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return m, nil
}
