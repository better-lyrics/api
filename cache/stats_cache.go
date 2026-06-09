package cache

import (
	"sync"
	"sync/atomic"
	"time"

	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
)

const (
	StatsStatusComputing = "computing"
	StatsStatusReady     = "ready"
)

// CachedStats is an immutable snapshot of cache statistics.
type CachedStats struct {
	NumKeys    int       `json:"num_keys"`
	SizeKB     int       `json:"size_kb"`
	ComputedAt time.Time `json:"computed_at"`
	DurationMs int64     `json:"duration_ms"`
	Status     string    `json:"status"`
}

// StatsCache holds the most recent stats snapshot computed in the background.
// Reads are O(1) and lock-free; writes are serialized via a TryLock so concurrent
// refreshes collapse into a single scan.
type StatsCache struct {
	value     atomic.Pointer[CachedStats]
	cache     *PersistentCache
	refreshMu sync.Mutex
}

// NewStatsCache returns a StatsCache seeded with a "computing" snapshot.
func NewStatsCache(c *PersistentCache) *StatsCache {
	sc := &StatsCache{cache: c}
	sc.value.Store(&CachedStats{Status: StatsStatusComputing})
	return sc
}

// Get returns the most recent snapshot. Always non-nil.
func (sc *StatsCache) Get() *CachedStats {
	return sc.value.Load()
}

// Refresh computes a fresh snapshot and stores it. If a refresh is already in
// flight, the call is a no-op (the in-flight scan's result will be published).
func (sc *StatsCache) Refresh() {
	if !sc.refreshMu.TryLock() {
		return
	}
	defer sc.refreshMu.Unlock()

	start := time.Now()
	keys, sizeKB := sc.cache.Stats()
	sc.value.Store(&CachedStats{
		NumKeys:    keys,
		SizeKB:     sizeKB,
		ComputedAt: time.Now(),
		DurationMs: time.Since(start).Milliseconds(),
		Status:     StatsStatusReady,
	})
}

// StartBackgroundRefresh kicks off an immediate scan in a goroutine and then
// re-scans every interval. Stops when stop is closed.
func (sc *StatsCache) StartBackgroundRefresh(interval time.Duration, stop <-chan struct{}) {
	go func() {
		log.Infof("%s Computing initial stats snapshot (refresh every %s)", logcolors.LogCache, interval)
		sc.Refresh()
		snap := sc.Get()
		log.Infof("%s Initial stats snapshot ready: %d keys, %d KB (took %dms)", logcolors.LogCache, snap.NumKeys, snap.SizeKB, snap.DurationMs)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sc.Refresh()
				snap := sc.Get()
				log.Infof("%s Stats snapshot refreshed: %d keys, %d KB (took %dms)", logcolors.LogCache, snap.NumKeys, snap.SizeKB, snap.DurationMs)
			case <-stop:
				return
			}
		}
	}()
}
