package main

import (
	"lyrics-api-go/cache"
	"sync"
)

type contextKey string

const (
	cacheOnlyModeKey          contextKey = "cacheOnlyMode"
	rateLimitTypeKey          contextKey = "rateLimitType"
	apiKeyRequiredForFreshKey contextKey = "apiKeyRequiredForFresh"
	apiKeyAuthenticatedKey    contextKey = "apiKeyAuthenticated"
	apiKeyInvalidKey          contextKey = "apiKeyInvalid"
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
	wg       sync.WaitGroup
	result   string
	score    float64
	language string
	isRTL    bool
	err      error
}

// CachedLyrics stores lyrics with track metadata
type CachedLyrics struct {
	TTML            string  `json:"ttml"`
	TrackDurationMs int     `json:"trackDurationMs"`
	Score           float64 `json:"score,omitempty"`
	Language        string  `json:"language,omitempty"`
	IsRTL           bool    `json:"isRTL,omitempty"`
}

// NegativeCacheEntry stores info about failed lyrics lookups
type NegativeCacheEntry struct {
	Reason    string `json:"reason"`
	Timestamp int64  `json:"timestamp"`
}

// MigrationJobStatus represents the status of an async migration job
type MigrationJobStatus string

const (
	JobStatusPending   MigrationJobStatus = "pending"
	JobStatusRunning   MigrationJobStatus = "running"
	JobStatusCompleted MigrationJobStatus = "completed"
	JobStatusFailed    MigrationJobStatus = "failed"
)

// MigrationJob tracks an async cache migration
type MigrationJob struct {
	ID          string             `json:"id"`
	Status      MigrationJobStatus `json:"status"`
	StartedAt   int64              `json:"started_at"`
	CompletedAt int64              `json:"completed_at,omitempty"`
	Recompress  bool               `json:"recompress"`
	Progress    MigrationProgress  `json:"progress"`
	Result      *MigrationResult   `json:"result,omitempty"`
	Error       string             `json:"error,omitempty"`
}

// MigrationProgress tracks migration progress
type MigrationProgress struct {
	TotalKeys     int `json:"total_keys"`
	ProcessedKeys int `json:"processed_keys"`
	Percent       int `json:"percent"`
}

// MigrationResult contains the final migration results
type MigrationResult struct {
	Migrated     int      `json:"migrated"`
	Recompressed int      `json:"recompressed"`
	Deleted      int      `json:"deleted"`
	Skipped      int      `json:"skipped"`
	Failed       int      `json:"failed"`
	BytesSaved   int64    `json:"bytes_saved"`
	MigratedKeys []string `json:"migrated_keys,omitempty"`
}

// migrationJobs stores active and completed migration jobs
var migrationJobs = struct {
	sync.RWMutex
	jobs map[string]*MigrationJob
}{jobs: make(map[string]*MigrationJob)}
