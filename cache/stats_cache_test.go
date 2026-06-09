package cache

import (
	"sync"
	"testing"
	"time"
)

func TestStatsCache_InitialStateIsSeeding(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	sc := NewStatsCache(pc)
	snap := sc.Get()
	if snap.Status != StatsStatusSeeding {
		t.Errorf("status = %q, want %q", snap.Status, StatsStatusSeeding)
	}
	if !snap.LastReconciledAt.IsZero() {
		t.Errorf("LastReconciledAt should be zero before first reconcile, got %v", snap.LastReconciledAt)
	}
}

func TestStatsCache_RefreshMovesStatusToReady(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	pc.Set("ttml_lyrics:a", "1")
	pc.Set("kugou_lyrics:b", "2")

	sc := NewStatsCache(pc)
	before := time.Now()
	sc.Refresh()

	snap := sc.Get()
	if snap.Status != StatsStatusReady {
		t.Errorf("status = %q, want %q", snap.Status, StatsStatusReady)
	}
	if snap.LastReconciledAt.Before(before) {
		t.Errorf("LastReconciledAt = %v, expected >= %v", snap.LastReconciledAt, before)
	}

	counts := pc.Counts()
	if counts["ttml"] != 1 || counts["kugou"] != 1 {
		t.Errorf("after reconcile: got %v, want ttml=1 kugou=1", counts)
	}
}

func TestStatsCache_ConcurrentRefreshIsSafe(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	pc.Set("ttml_lyrics:a", "1")
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

	if sc.Get().Status != StatsStatusReady {
		t.Errorf("status = %q, want ready", sc.Get().Status)
	}
}

func TestStatsCache_RefreshOnClosedDBPublishesError(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	// Don't defer cleanup, we close the DB manually below.

	sc := NewStatsCache(pc)
	// Drive the cache into a state where reconcile must fail by closing the
	// underlying DB.
	if err := pc.Close(); err != nil {
		t.Fatal(err)
	}

	sc.Refresh()

	snap := sc.Get()
	if snap.Status != StatsStatusError {
		t.Errorf("status = %q, want %q", snap.Status, StatsStatusError)
	}
	if snap.LastError == "" {
		t.Error("expected non-empty LastError")
	}
	_ = cleanup // not used, db already closed
}

func TestStatsCache_RefreshErrorPreservesLastReconciledAt(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)

	pc.Set("ttml_lyrics:a", "1")
	sc := NewStatsCache(pc)
	sc.Refresh()

	goodSnap := sc.Get()
	if goodSnap.Status != StatsStatusReady {
		t.Fatalf("setup: status = %q, want %q", goodSnap.Status, StatsStatusReady)
	}
	if goodSnap.LastReconciledAt.IsZero() {
		t.Fatal("setup: LastReconciledAt should be set after successful reconcile")
	}

	if err := pc.Close(); err != nil {
		t.Fatal(err)
	}

	sc.Refresh()

	errSnap := sc.Get()
	if errSnap.Status != StatsStatusError {
		t.Errorf("status = %q, want %q", errSnap.Status, StatsStatusError)
	}
	if !errSnap.LastReconciledAt.Equal(goodSnap.LastReconciledAt) {
		t.Errorf("LastReconciledAt = %v, want %v (preserved from last good reconcile)",
			errSnap.LastReconciledAt, goodSnap.LastReconciledAt)
	}
	_ = cleanup
}

func TestStatsCache_StartBackgroundRefreshTriggersInitialReconcile(t *testing.T) {
	pc, _, cleanup := setupTestCache(t, false)
	defer cleanup()

	pc.Set("ttml_lyrics:a", "1")
	pc.Set("ttml_lyrics:b", "2")

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

	if sc.Get().Status != StatsStatusReady {
		t.Fatalf("initial reconcile did not complete; status %q", sc.Get().Status)
	}
}
