package cache

import (
	"sync"
	"sync/atomic"
	"time"

	"lyrics-api-go/logcolors"

	log "github.com/sirupsen/logrus"
)

const (
	StatsStatusSeeding = "seeding"
	StatsStatusReady   = "ready"
	StatsStatusError   = "error"
)

// CachedStats reports the lifecycle state of the counter-reconciliation loop.
// The actual key counts are NOT held here: callers read them live from
// PersistentCache.Counts(), which is microseconds.
type CachedStats struct {
	Status           string    `json:"status"`
	LastReconciledAt time.Time `json:"last_reconciled_at"`
	LastDurationMs   int64     `json:"last_duration_ms"`
	LastError        string    `json:"last_error,omitempty"`
}

type StatsCache struct {
	value     atomic.Pointer[CachedStats]
	cache     *PersistentCache
	refreshMu sync.Mutex
}

func NewStatsCache(c *PersistentCache) *StatsCache {
	sc := &StatsCache{cache: c}
	sc.value.Store(&CachedStats{Status: StatsStatusSeeding})
	return sc
}

func (sc *StatsCache) Get() *CachedStats {
	return sc.value.Load()
}

// Refresh runs ReconcileCounters and updates the lifecycle state.
// No-op if another refresh is already in flight. On failure the snapshot moves
// to StatsStatusError, preserves LastReconciledAt from the previous good run,
// and records the error message in LastError.
func (sc *StatsCache) Refresh() {
	if !sc.refreshMu.TryLock() {
		return
	}
	defer sc.refreshMu.Unlock()

	start := time.Now()
	if err := sc.cache.ReconcileCounters(); err != nil {
		log.Errorf("%s Reconcile failed: %v", logcolors.LogCache, err)
		prev := sc.value.Load()
		sc.value.Store(&CachedStats{
			Status:           StatsStatusError,
			LastReconciledAt: prev.LastReconciledAt,
			LastDurationMs:   time.Since(start).Milliseconds(),
			LastError:        err.Error(),
		})
		return
	}
	sc.value.Store(&CachedStats{
		Status:           StatsStatusReady,
		LastReconciledAt: time.Now(),
		LastDurationMs:   time.Since(start).Milliseconds(),
	})
}

// StartBackgroundRefresh runs an immediate seed-reconcile in a goroutine and
// then re-reconciles every interval. Stops when stop is closed.
func (sc *StatsCache) StartBackgroundRefresh(interval time.Duration, stop <-chan struct{}) {
	go func() {
		log.Infof("%s Seeding counters (reconcile cadence: %s)", logcolors.LogCache, interval)
		sc.Refresh()
		if snap := sc.Get(); snap.Status == StatsStatusReady {
			log.Infof("%s Counter seed complete (took %dms)", logcolors.LogCache, snap.LastDurationMs)
		} else {
			log.Errorf("%s Counter seed FAILED (status=%s): %s", logcolors.LogCache, snap.Status, snap.LastError)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sc.Refresh()
				if snap := sc.Get(); snap.Status == StatsStatusReady {
					log.Infof("%s Counters reconciled (took %dms)", logcolors.LogCache, snap.LastDurationMs)
				} else {
					log.Errorf("%s Counter reconcile FAILED (status=%s): %s", logcolors.LogCache, snap.Status, snap.LastError)
				}
			case <-stop:
				return
			}
		}
	}()
}
