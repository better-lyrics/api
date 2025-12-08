package main

import (
	"lyrics-api-go/cache"
	"sync"
)

type contextKey string

const (
	cacheOnlyModeKey contextKey = "cacheOnlyMode"
	rateLimitTypeKey contextKey = "rateLimitType"
)

// CacheDump represents the full cache contents
type CacheDump map[string]cache.CacheEntry

// CachePerformance contains cache hit/miss statistics
type CachePerformance struct {
	Hits         int64   `json:"hits"`
	Misses       int64   `json:"misses"`
	NegativeHits int64   `json:"negative_hits"`
	StaleHits    int64   `json:"stale_hits"`
	HitRate      float64 `json:"hit_rate_percent"`
}

// CacheDumpResponse is the response format for /cache endpoint
type CacheDumpResponse struct {
	NumberOfKeys int              `json:"number_of_keys"`
	SizeInKB     int              `json:"size_kb"`
	SizeInMB     float64          `json:"size_mb"`
	Performance  CachePerformance `json:"performance"`
	Cache        CacheDump        `json:"cache"`
}

// InFlightRequest tracks concurrent requests for the same query
type InFlightRequest struct {
	wg     sync.WaitGroup
	result string
	score  float64
	err    error
}

// CachedLyrics stores TTML with track metadata for duration validation
type CachedLyrics struct {
	TTML            string `json:"ttml"`
	TrackDurationMs int    `json:"trackDurationMs"`
}

// NegativeCacheEntry stores info about failed lyrics lookups
type NegativeCacheEntry struct {
	Reason    string `json:"reason"`
	Timestamp int64  `json:"timestamp"`
}
