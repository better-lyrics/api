package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

const (
	statsBucketName = "stats"
	statsKey        = "server_stats"
)

// Store handles persistent storage for stats
type Store struct {
	db       *bolt.DB
	dbPath   string
	mu       sync.Mutex
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// PersistedStats represents the stats data that gets persisted to disk
type PersistedStats struct {
	// Cumulative counters (these accumulate across restarts)
	TotalRequests     int64 `json:"total_requests"`
	LyricsRequests    int64 `json:"lyrics_requests"`
	CacheRequests     int64 `json:"cache_requests"`
	StatsRequests     int64 `json:"stats_requests"`
	HealthRequests    int64 `json:"health_requests"`
	OtherRequests     int64 `json:"other_requests"`
	CacheHits         int64 `json:"cache_hits"`
	CacheMisses       int64 `json:"cache_misses"`
	NegativeCacheHits int64 `json:"negative_cache_hits"`
	StaleCacheHits    int64 `json:"stale_cache_hits"`
	RateLimitNormal   int64 `json:"rate_limit_normal"`
	RateLimitCached   int64 `json:"rate_limit_cached"`
	RateLimitExceeded int64 `json:"rate_limit_exceeded"`
	Status2xx         int64 `json:"status_2xx"`
	Status4xx         int64 `json:"status_4xx"`
	Status5xx         int64 `json:"status_5xx"`

	// Response time tracking
	TotalResponseTime   int64 `json:"total_response_time"`
	ResponseCount       int64 `json:"response_count"`
	MinResponseTime     int64 `json:"min_response_time"`
	MaxResponseTime     int64 `json:"max_response_time"`
	LyricsResponseTime  int64 `json:"lyrics_response_time"`
	LyricsResponseCount int64 `json:"lyrics_response_count"`

	// Account usage
	AccountUsage map[string]int64 `json:"account_usage"`

	// Metadata
	LastSaved    time.Time `json:"last_saved"`
	FirstStarted time.Time `json:"first_started"`
}

// NewStore creates a new stats store with a dedicated BoltDB file
func NewStore(dbPath string) (*Store, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create stats directory: %v", err)
	}

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open stats database: %v", err)
	}

	// Create bucket if it doesn't exist
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(statsBucketName))
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create stats bucket: %v", err)
	}

	store := &Store{
		db:       db,
		dbPath:   dbPath,
		stopChan: make(chan struct{}),
	}

	log.Infof("%s Stats store initialized at %s", logcolors.LogStats, dbPath)
	return store, nil
}

// Load reads persisted stats from disk and applies them to the global stats
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var persisted PersistedStats
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(statsBucketName))
		if b == nil {
			return nil
		}

		data := b.Get([]byte(statsKey))
		if data == nil {
			return nil // No persisted stats yet
		}

		return json.Unmarshal(data, &persisted)
	})

	if err != nil {
		return fmt.Errorf("failed to load stats: %v", err)
	}

	// Apply persisted values to global stats
	stats := Get()

	stats.TotalRequests.Store(persisted.TotalRequests)
	stats.LyricsRequests.Store(persisted.LyricsRequests)
	stats.CacheRequests.Store(persisted.CacheRequests)
	stats.StatsRequests.Store(persisted.StatsRequests)
	stats.HealthRequests.Store(persisted.HealthRequests)
	stats.OtherRequests.Store(persisted.OtherRequests)
	stats.CacheHits.Store(persisted.CacheHits)
	stats.CacheMisses.Store(persisted.CacheMisses)
	stats.NegativeCacheHits.Store(persisted.NegativeCacheHits)
	stats.StaleCacheHits.Store(persisted.StaleCacheHits)
	stats.RateLimitNormal.Store(persisted.RateLimitNormal)
	stats.RateLimitCached.Store(persisted.RateLimitCached)
	stats.RateLimitExceeded.Store(persisted.RateLimitExceeded)
	stats.Status2xx.Store(persisted.Status2xx)
	stats.Status4xx.Store(persisted.Status4xx)
	stats.Status5xx.Store(persisted.Status5xx)
	stats.totalResponseTime.Store(persisted.TotalResponseTime)
	stats.responseCount.Store(persisted.ResponseCount)
	stats.lyricsResponseTime.Store(persisted.LyricsResponseTime)
	stats.lyricsResponseCount.Store(persisted.LyricsResponseCount)

	// Only update min/max if we have valid persisted values
	if persisted.MinResponseTime > 0 && persisted.MinResponseTime < int64(^uint64(0)>>1) {
		stats.minResponseTime.Store(persisted.MinResponseTime)
	}
	if persisted.MaxResponseTime > 0 {
		stats.maxResponseTime.Store(persisted.MaxResponseTime)
	}

	// Restore account usage
	for name, count := range persisted.AccountUsage {
		counter := &atomic.Int64{}
		counter.Store(count)
		stats.accountUsage.Store(name, counter)
	}

	// Preserve the original first start time if available
	if !persisted.FirstStarted.IsZero() {
		stats.StartTime = persisted.FirstStarted
	}

	log.Infof("%s Loaded persisted stats (total requests: %d, first started: %s)",
		logcolors.LogStats, persisted.TotalRequests, persisted.FirstStarted.Format(time.RFC3339))

	return nil
}

// Save persists current stats to disk
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := Get()

	persisted := PersistedStats{
		TotalRequests:       stats.TotalRequests.Load(),
		LyricsRequests:      stats.LyricsRequests.Load(),
		CacheRequests:       stats.CacheRequests.Load(),
		StatsRequests:       stats.StatsRequests.Load(),
		HealthRequests:      stats.HealthRequests.Load(),
		OtherRequests:       stats.OtherRequests.Load(),
		CacheHits:           stats.CacheHits.Load(),
		CacheMisses:         stats.CacheMisses.Load(),
		NegativeCacheHits:   stats.NegativeCacheHits.Load(),
		StaleCacheHits:      stats.StaleCacheHits.Load(),
		RateLimitNormal:     stats.RateLimitNormal.Load(),
		RateLimitCached:     stats.RateLimitCached.Load(),
		RateLimitExceeded:   stats.RateLimitExceeded.Load(),
		Status2xx:           stats.Status2xx.Load(),
		Status4xx:           stats.Status4xx.Load(),
		Status5xx:           stats.Status5xx.Load(),
		TotalResponseTime:   stats.totalResponseTime.Load(),
		ResponseCount:       stats.responseCount.Load(),
		MinResponseTime:     stats.minResponseTime.Load(),
		MaxResponseTime:     stats.maxResponseTime.Load(),
		LyricsResponseTime:  stats.lyricsResponseTime.Load(),
		LyricsResponseCount: stats.lyricsResponseCount.Load(),
		AccountUsage:        stats.AccountUsageSnapshot(),
		LastSaved:           time.Now(),
		FirstStarted:        stats.StartTime,
	}

	data, err := json.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("failed to marshal stats: %v", err)
	}

	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(statsBucketName))
		if b == nil {
			return fmt.Errorf("stats bucket not found")
		}
		return b.Put([]byte(statsKey), data)
	})

	if err != nil {
		return fmt.Errorf("failed to save stats: %v", err)
	}

	return nil
}

// StartAutoSave begins periodic saving of stats
func (s *Store) StartAutoSave(interval time.Duration) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := s.Save(); err != nil {
					log.Warnf("%s Failed to auto-save stats: %v", logcolors.LogStats, err)
				}
			case <-s.stopChan:
				return
			}
		}
	}()
	log.Infof("%s Started auto-save with interval %v", logcolors.LogStats, interval)
}

// Close saves stats and closes the database
func (s *Store) Close() error {
	// Signal auto-save goroutine to stop
	close(s.stopChan)
	s.wg.Wait()

	// Final save before closing
	if err := s.Save(); err != nil {
		log.Warnf("%s Failed to save stats on close: %v", logcolors.LogStats, err)
	} else {
		log.Infof("%s Stats saved on shutdown", logcolors.LogStats)
	}

	return s.db.Close()
}
