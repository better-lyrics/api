package cache

import (
	"sync"
	"testing"
	"time"
)

func TestStatsCache_InitialStateIsComputing(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	sc := NewStatsCache(pc)
	snap := sc.Get()

	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.Status != StatsStatusComputing {
		t.Errorf("expected status %q, got %q", StatsStatusComputing, snap.Status)
	}
	if snap.NumKeys != 0 {
		t.Errorf("expected 0 keys before first refresh, got %d", snap.NumKeys)
	}
	if !snap.ComputedAt.IsZero() {
		t.Errorf("expected zero ComputedAt before first refresh, got %v", snap.ComputedAt)
	}
}

func TestStatsCache_RefreshPopulatesFromUnderlyingCache(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	pc.Set("a", "1")
	pc.Set("b", "2")
	pc.Set("c", "3")

	sc := NewStatsCache(pc)
	before := time.Now()
	sc.Refresh()

	snap := sc.Get()
	if snap.Status != StatsStatusReady {
		t.Errorf("expected status %q after refresh, got %q", StatsStatusReady, snap.Status)
	}
	if snap.NumKeys != 3 {
		t.Errorf("expected 3 keys, got %d", snap.NumKeys)
	}
	if snap.ComputedAt.Before(before) {
		t.Errorf("expected ComputedAt >= %v, got %v", before, snap.ComputedAt)
	}
}

func TestStatsCache_RefreshReflectsCacheGrowth(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	sc := NewStatsCache(pc)
	sc.Refresh()
	if got := sc.Get().NumKeys; got != 0 {
		t.Fatalf("expected 0 keys initially, got %d", got)
	}

	pc.Set("a", "1")
	pc.Set("b", "2")
	sc.Refresh()

	if got := sc.Get().NumKeys; got != 2 {
		t.Errorf("expected 2 keys after adding entries, got %d", got)
	}
}

func TestStatsCache_GetIsConcurrentSafe(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	pc.Set("a", "1")
	sc := NewStatsCache(pc)
	sc.Refresh()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := sc.Get()
			if snap.NumKeys != 1 {
				t.Errorf("expected 1 key, got %d", snap.NumKeys)
			}
		}()
	}
	wg.Wait()
}

func TestStatsCache_ConcurrentRefreshIsSafe(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	pc.Set("a", "1")
	sc := NewStatsCache(pc)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc.Refresh()
		}()
	}
	wg.Wait()

	snap := sc.Get()
	if snap.Status != StatsStatusReady {
		t.Errorf("expected status %q, got %q", StatsStatusReady, snap.Status)
	}
	if snap.NumKeys != 1 {
		t.Errorf("expected 1 key, got %d", snap.NumKeys)
	}
}

func TestStatsCache_StartBackgroundRefreshSeedsInitialScan(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	pc.Set("a", "1")
	pc.Set("b", "2")

	sc := NewStatsCache(pc)
	stop := make(chan struct{})
	sc.StartBackgroundRefresh(time.Hour, stop)
	defer close(stop)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sc.Get().Status == StatsStatusReady {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	snap := sc.Get()
	if snap.Status != StatsStatusReady {
		t.Fatalf("expected background refresh to complete within deadline; status %q", snap.Status)
	}
	if snap.NumKeys != 2 {
		t.Errorf("expected 2 keys, got %d", snap.NumKeys)
	}
}
