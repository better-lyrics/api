package stats

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRecordLRCRedFetch(t *testing.T) {
	s := newStats()
	s.RecordLRCRedFetch(true, nil)
	s.RecordLRCRedFetch(false, nil)
	s.RecordLRCRedFetch(false, fmt.Errorf("boom"))
	if got := s.LRCRedFetchAttempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if s.LRCRedFetchHits.Load() != 1 || s.LRCRedFetchMisses.Load() != 1 || s.LRCRedFetchErrors.Load() != 1 {
		t.Fatalf("hit/miss/err = %d/%d/%d, want 1/1/1",
			s.LRCRedFetchHits.Load(), s.LRCRedFetchMisses.Load(), s.LRCRedFetchErrors.Load())
	}
}

func TestRecordLRCRedContribute(t *testing.T) {
	s := newStats()
	s.RecordLRCRedContribute(true)
	s.RecordLRCRedContribute(true)
	s.RecordLRCRedContribute(false)
	if s.LRCRedContributeSent.Load() != 2 || s.LRCRedContributeSkipped.Load() != 1 {
		t.Fatalf("sent/skipped = %d/%d, want 2/1", s.LRCRedContributeSent.Load(), s.LRCRedContributeSkipped.Load())
	}
}

func TestLRCRedCounters_PersistRoundTrip(t *testing.T) {
	s := newStats()
	s.RecordLRCRedFetch(true, nil)
	s.RecordLRCRedContribute(true)
	restored := newStats()
	restored.Restore(s.Serialize())
	if restored.LRCRedFetchHits.Load() != 1 || restored.LRCRedContributeSent.Load() != 1 {
		t.Fatalf("round-trip lost lrc.red counters: hits=%d sent=%d",
			restored.LRCRedFetchHits.Load(), restored.LRCRedContributeSent.Load())
	}
}

// newStats returns a fresh Stats instance for testing (avoids global state).
func newStats() *Stats {
	s := &Stats{StartTime: time.Now()}
	s.minResponseTime.Store(int64(^uint64(0) >> 1))
	return s
}

// ---------------------------------------------------------------------------
// RecordUserAgent
// ---------------------------------------------------------------------------

func TestRecordUserAgent_EmptyBecomes_empty(t *testing.T) {
	s := newStats()
	s.RecordUserAgent("")

	snap := s.UserAgentSnapshot()
	if snap["(empty)"] != 1 {
		t.Fatalf("expected (empty)=1, got %d", snap["(empty)"])
	}
	if _, ok := snap[""]; ok {
		t.Fatal("empty string key should not exist in snapshot")
	}
}

func TestRecordUserAgent_IncrementsExistingUA(t *testing.T) {
	s := newStats()
	ua := "Mozilla/5.0 TestBrowser"
	s.RecordUserAgent(ua)
	s.RecordUserAgent(ua)
	s.RecordUserAgent(ua)

	snap := s.UserAgentSnapshot()
	if snap[ua] != 3 {
		t.Fatalf("expected %s=3, got %d", ua, snap[ua])
	}
}

func TestRecordUserAgent_CapsAtMaxUniqueUserAgents(t *testing.T) {
	s := newStats()

	// Fill up to the cap with distinct UAs
	for i := range maxUniqueUserAgents {
		s.RecordUserAgent(fmt.Sprintf("ua-%d", i))
	}

	if s.uniqueUACount.Load() != maxUniqueUserAgents {
		t.Fatalf("expected uniqueUACount=%d, got %d", maxUniqueUserAgents, s.uniqueUACount.Load())
	}

	// One more distinct UA should go into "(other)"
	s.RecordUserAgent("brand-new-ua")

	snap := s.UserAgentSnapshot()
	if snap["(other)"] != 1 {
		t.Fatalf("expected (other)=1 after cap, got %d", snap["(other)"])
	}
	if _, ok := snap["brand-new-ua"]; ok {
		t.Fatal("brand-new-ua should not be stored after cap reached")
	}
}

func TestRecordUserAgent_ExistingUAStillTrackedAfterCap(t *testing.T) {
	s := newStats()

	// Establish one known UA before filling up
	s.RecordUserAgent("known-ua")

	for i := 1; i < maxUniqueUserAgents; i++ {
		s.RecordUserAgent(fmt.Sprintf("ua-%d", i))
	}

	// Hit the known UA again — should still increment it, not go to "(other)"
	s.RecordUserAgent("known-ua")

	snap := s.UserAgentSnapshot()
	if snap["known-ua"] != 2 {
		t.Fatalf("expected known-ua=2, got %d", snap["known-ua"])
	}
	if _, ok := snap["(other)"]; ok {
		t.Fatal("(other) should not appear when hitting an existing UA")
	}
}

func TestRecordUserAgent_OtherBucketAccumulates(t *testing.T) {
	s := newStats()

	for i := range maxUniqueUserAgents {
		s.RecordUserAgent(fmt.Sprintf("ua-%d", i))
	}

	// Multiple new UAs all land in "(other)"
	for i := range 5 {
		s.RecordUserAgent(fmt.Sprintf("overflow-%d", i))
	}

	snap := s.UserAgentSnapshot()
	if snap["(other)"] != 5 {
		t.Fatalf("expected (other)=5, got %d", snap["(other)"])
	}
}

// ---------------------------------------------------------------------------
// RecordRequest — endpoint routing
// ---------------------------------------------------------------------------

func TestRecordRequest_EndpointRouting(t *testing.T) {
	tests := []struct {
		endpoint string
		check    func(s *Stats) int64
	}{
		{"/getLyrics", func(s *Stats) int64 { return s.LyricsRequests.Load() }},
		{"/cache", func(s *Stats) int64 { return s.CacheRequests.Load() }},
		{"/stats", func(s *Stats) int64 { return s.StatsRequests.Load() }},
		{"/health", func(s *Stats) int64 { return s.HealthRequests.Load() }},
		{"/unknown", func(s *Stats) int64 { return s.OtherRequests.Load() }},
	}

	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			s := newStats()
			s.RecordRequest(tt.endpoint)
			if got := tt.check(s); got != 1 {
				t.Fatalf("expected 1 for %s counter, got %d", tt.endpoint, got)
			}
			if s.TotalRequests.Load() != 1 {
				t.Fatalf("expected TotalRequests=1, got %d", s.TotalRequests.Load())
			}
		})
	}
}

func TestRequestRate_WindowEdges(t *testing.T) {
	s := newStats()
	now := time.Unix(1_800_000_000, 0)

	s.recordRequestAt(now.Add(-time.Hour))
	s.recordRequestAt(now.Add(-time.Hour + time.Second))
	s.recordRequestAt(now.Add(-time.Minute))
	s.recordRequestAt(now.Add(-time.Minute + time.Second))
	s.recordRequestAt(now)
	s.recordRequestAt(now)

	if got := s.requestsWithin(now, time.Minute); got != 3 {
		t.Fatalf("expected 3 requests in last minute, got %d", got)
	}
	if got := s.requestsWithin(now, time.Hour); got != 5 {
		t.Fatalf("expected 5 requests in last hour, got %d", got)
	}
}

func TestRequestRate_EmptyAndIdle(t *testing.T) {
	s := newStats()
	now := time.Unix(1_800_000_000, 0)

	if got := s.requestsWithin(now, time.Hour); got != 0 {
		t.Fatalf("expected 0 on fresh stats, got %d", got)
	}

	s.recordRequestAt(now)
	later := now.Add(2 * time.Hour)
	if got := s.requestsWithin(later, time.Hour); got != 0 {
		t.Fatalf("expected 0 after an idle hour, got %d", got)
	}
}

func TestRequestRate_BucketReuseAfterWrap(t *testing.T) {
	s := newStats()
	now := time.Unix(1_800_000_000, 0)

	for range 5 {
		s.recordRequestAt(now)
	}
	// Same ring slot, one full window later: stale count must not leak in.
	wrapped := now.Add(time.Hour)
	s.recordRequestAt(wrapped)

	if got := s.requestsWithin(wrapped, time.Hour); got != 1 {
		t.Fatalf("expected 1 after slot reuse, got %d", got)
	}
}

func TestRequestRate_ConcurrentRecords(t *testing.T) {
	s := newStats()
	now := time.Unix(1_800_000_000, 0)

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 1000 {
				s.recordRequestAt(now.Add(-time.Duration((g*1000+i)%120) * time.Second))
			}
		}()
	}
	wg.Wait()

	if got := s.requestsWithin(now, time.Hour); got != 8000 {
		t.Fatalf("expected 8000, got %d", got)
	}
}

// regression: a slice of timestamps reallocated and copied ~850k entries per
// request at production traffic, which made GC the top CPU consumer.
func TestRecordRequest_DoesNotAllocate(t *testing.T) {
	s := newStats()
	if allocs := testing.AllocsPerRun(1000, func() { s.RecordRequest("/getLyrics") }); allocs != 0 {
		t.Fatalf("expected 0 allocations per RecordRequest, got %v", allocs)
	}

	now := time.Unix(1_800_000_000, 0)
	if allocs := testing.AllocsPerRun(3*requestWindowSeconds, func() {
		now = now.Add(time.Second)
		s.recordRequestAt(now)
	}); allocs != 0 {
		t.Fatalf("expected 0 allocations while evicting old seconds, got %v", allocs)
	}
}

// ---------------------------------------------------------------------------
// RecordAccountUsage & AccountUsageSnapshot
// ---------------------------------------------------------------------------

func TestRecordAccountUsage(t *testing.T) {
	s := newStats()
	s.RecordAccountUsage("account-1")
	s.RecordAccountUsage("account-1")
	s.RecordAccountUsage("account-2")

	snap := s.AccountUsageSnapshot()
	if snap["account-1"] != 2 {
		t.Fatalf("expected account-1=2, got %d", snap["account-1"])
	}
	if snap["account-2"] != 1 {
		t.Fatalf("expected account-2=1, got %d", snap["account-2"])
	}
}

// ---------------------------------------------------------------------------
// RequestsPerMinute / RequestsPerHour
// ---------------------------------------------------------------------------

func TestRequestsPerMinuteAndHour(t *testing.T) {
	s := newStats()

	// Add recent requests
	s.RecordRequest("/getLyrics")
	s.RecordRequest("/getLyrics")
	s.RecordRequest("/cache")

	rpm := s.RequestsPerMinute()
	rph := s.RequestsPerHour()

	if rpm != 3 {
		t.Fatalf("expected RequestsPerMinute=3, got %d", rpm)
	}
	if rph != 3 {
		t.Fatalf("expected RequestsPerHour=3, got %d", rph)
	}
}

// ---------------------------------------------------------------------------
// Cache recording helpers
// ---------------------------------------------------------------------------

func TestCacheRecording(t *testing.T) {
	s := newStats()

	s.RecordCacheHit()
	s.RecordCacheHit()
	s.RecordCacheMiss()
	s.RecordNegativeCacheHit()
	s.RecordStaleCacheHit()

	if s.CacheHits.Load() != 2 {
		t.Fatalf("expected CacheHits=2, got %d", s.CacheHits.Load())
	}
	if s.CacheMisses.Load() != 1 {
		t.Fatalf("expected CacheMisses=1, got %d", s.CacheMisses.Load())
	}
	if s.NegativeCacheHits.Load() != 1 {
		t.Fatalf("expected NegativeCacheHits=1, got %d", s.NegativeCacheHits.Load())
	}
	if s.StaleCacheHits.Load() != 1 {
		t.Fatalf("expected StaleCacheHits=1, got %d", s.StaleCacheHits.Load())
	}
}

func TestCacheHitRate(t *testing.T) {
	tests := []struct {
		name     string
		hits     int
		misses   int
		expected float64
	}{
		{"no requests", 0, 0, 0},
		{"all hits", 10, 0, 100},
		{"all misses", 0, 10, 0},
		{"50/50", 5, 5, 50},
		{"75/25", 3, 1, 75},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStats()
			for i := 0; i < tt.hits; i++ {
				s.RecordCacheHit()
			}
			for i := 0; i < tt.misses; i++ {
				s.RecordCacheMiss()
			}
			rate := s.CacheHitRate()
			if rate != tt.expected {
				t.Fatalf("expected hit rate %.1f%%, got %.1f%%", tt.expected, rate)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RecordRateLimit
// ---------------------------------------------------------------------------

func TestRecordRateLimit(t *testing.T) {
	s := newStats()

	s.RecordRateLimit("normal")
	s.RecordRateLimit("normal")
	s.RecordRateLimit("cached")
	s.RecordRateLimit("exceeded")
	s.RecordRateLimit("unknown") // should be a no-op

	if s.RateLimitNormal.Load() != 2 {
		t.Fatalf("expected RateLimitNormal=2, got %d", s.RateLimitNormal.Load())
	}
	if s.RateLimitCached.Load() != 1 {
		t.Fatalf("expected RateLimitCached=1, got %d", s.RateLimitCached.Load())
	}
	if s.RateLimitExceeded.Load() != 1 {
		t.Fatalf("expected RateLimitExceeded=1, got %d", s.RateLimitExceeded.Load())
	}
}

// ---------------------------------------------------------------------------
// RecordStatusCode
// ---------------------------------------------------------------------------

func TestRecordStatusCode(t *testing.T) {
	s := newStats()

	codes := []int{200, 201, 204, 400, 404, 422, 500, 503}
	for _, c := range codes {
		s.RecordStatusCode(c)
	}

	if s.Status2xx.Load() != 3 {
		t.Fatalf("expected Status2xx=3, got %d", s.Status2xx.Load())
	}
	if s.Status4xx.Load() != 3 {
		t.Fatalf("expected Status4xx=3, got %d", s.Status4xx.Load())
	}
	if s.Status5xx.Load() != 2 {
		t.Fatalf("expected Status5xx=2, got %d", s.Status5xx.Load())
	}
}

// ---------------------------------------------------------------------------
// RecordResponseTime
// ---------------------------------------------------------------------------

func TestRecordResponseTime(t *testing.T) {
	s := newStats()

	s.RecordResponseTime(100*time.Millisecond, "/getLyrics")
	s.RecordResponseTime(200*time.Millisecond, "/getLyrics")
	s.RecordResponseTime(50*time.Millisecond, "/cache")

	avg := s.AvgResponseTime()
	// (100 + 200 + 50) / 3 ≈ 116.666ms — truncated to 116ms by integer division
	if avg < 116*time.Millisecond || avg > 117*time.Millisecond {
		t.Fatalf("expected avg ~116ms, got %v", avg)
	}

	if s.MinResponseTime() != 50*time.Millisecond {
		t.Fatalf("expected min=50ms, got %v", s.MinResponseTime())
	}
	if s.MaxResponseTime() != 200*time.Millisecond {
		t.Fatalf("expected max=200ms, got %v", s.MaxResponseTime())
	}

	avgLyrics := s.AvgLyricsResponseTime()
	if avgLyrics != 150*time.Millisecond {
		t.Fatalf("expected avg lyrics=150ms, got %v", avgLyrics)
	}
}

func TestResponseTime_NoRequests(t *testing.T) {
	s := newStats()

	if s.AvgResponseTime() != 0 {
		t.Fatal("expected avg=0 with no requests")
	}
	if s.MinResponseTime() != 0 {
		t.Fatal("expected min=0 with no requests")
	}
	if s.MaxResponseTime() != 0 {
		t.Fatal("expected max=0 with no requests")
	}
	if s.AvgLyricsResponseTime() != 0 {
		t.Fatal("expected avg lyrics=0 with no requests")
	}
}

// ---------------------------------------------------------------------------
// Snapshot
// ---------------------------------------------------------------------------

func TestSnapshot_ContainsExpectedKeys(t *testing.T) {
	s := newStats()
	s.RecordRequest("/getLyrics")
	s.RecordCacheHit()
	s.RecordRateLimit("normal")
	s.RecordStatusCode(200)
	s.RecordResponseTime(10*time.Millisecond, "/getLyrics")
	s.RecordAccountUsage("acct-1")

	snap := s.Snapshot()

	expectedTopLevel := []string{"server", "requests", "cache", "rate_limiting", "responses", "response_times", "accounts"}
	for _, key := range expectedTopLevel {
		if _, ok := snap[key]; !ok {
			t.Fatalf("snapshot missing top-level key %q", key)
		}
	}

	requests := snap["requests"].(map[string]any)
	if requests["total"].(int64) != 1 {
		t.Fatal("snapshot requests.total should be 1")
	}

	accounts := snap["accounts"].(map[string]int64)
	if accounts["acct-1"] != 1 {
		t.Fatal("snapshot accounts should include acct-1")
	}
}

// ---------------------------------------------------------------------------
// UserAgentSnapshot
// ---------------------------------------------------------------------------

func TestUserAgentSnapshot_Empty(t *testing.T) {
	s := newStats()
	snap := s.UserAgentSnapshot()
	if len(snap) != 0 {
		t.Fatalf("expected empty snapshot, got %d entries", len(snap))
	}
}

// ---------------------------------------------------------------------------
// Global singleton
// ---------------------------------------------------------------------------

func TestGet_ReturnsGlobalInstance(t *testing.T) {
	g := Get()
	if g == nil {
		t.Fatal("Get() should not return nil")
	}
	if g != global {
		t.Fatal("Get() should return the global instance")
	}
}

// ---------------------------------------------------------------------------
// Concurrent safety (smoke test)
// ---------------------------------------------------------------------------

func TestRecordUserAgent_ConcurrentAccess(t *testing.T) {
	s := newStats()

	const goroutines = 50
	const uasPerGoroutine = 100
	const totalRecords = goroutines * uasPerGoroutine // 5000

	done := make(chan struct{})

	// 50 goroutines × 100 unique UAs each = 5000 distinct UAs (5× the cap)
	for g := range goroutines {
		go func(id int) {
			for i := range uasPerGoroutine {
				s.RecordUserAgent(fmt.Sprintf("ua-g%d-%d", id, i))
			}
			done <- struct{}{}
		}(g)
	}

	for range goroutines {
		<-done
	}

	// Assert uniqueUACount never exceeded the cap (strict — not a soft check)
	uniqueCount := s.uniqueUACount.Load()
	if uniqueCount > maxUniqueUserAgents {
		t.Fatalf("uniqueUACount %d exceeded cap %d", uniqueCount, maxUniqueUserAgents)
	}

	// Assert total record count equals 5000 (no lost records)
	snap := s.UserAgentSnapshot()
	total := int64(0)
	for _, v := range snap {
		total += v
	}
	if total != totalRecords {
		t.Fatalf("expected %d total UA records, got %d", totalRecords, total)
	}

	// Assert tracked UAs + "(other)" bucket doesn't exceed cap + 1
	if len(snap) > maxUniqueUserAgents+1 {
		t.Fatalf("snapshot has %d entries, expected at most %d (cap + other bucket)", len(snap), maxUniqueUserAgents+1)
	}
}

func TestRecordRequest_ConcurrentAccess(t *testing.T) {
	s := newStats()
	done := make(chan struct{})

	for range 10 {
		go func() {
			for range 100 {
				s.RecordRequest("/getLyrics")
			}
			done <- struct{}{}
		}()
	}

	for range 10 {
		<-done
	}

	if s.TotalRequests.Load() != 1000 {
		t.Fatalf("expected 1000 total requests, got %d", s.TotalRequests.Load())
	}
}

// ---------------------------------------------------------------------------
// maxUniqueUserAgents constant
// ---------------------------------------------------------------------------

func TestMaxUniqueUserAgents_Value(t *testing.T) {
	// Ensure the constant hasn't been accidentally changed
	if maxUniqueUserAgents != 1000 {
		t.Fatalf("expected maxUniqueUserAgents=1000, got %d", maxUniqueUserAgents)
	}
}

// ---------------------------------------------------------------------------
// Uptime
// ---------------------------------------------------------------------------

func TestUptime_Positive(t *testing.T) {
	s := newStats()
	// Wait briefly so uptime is non-zero
	time.Sleep(time.Millisecond)
	if s.Uptime() <= 0 {
		t.Fatal("uptime should be positive")
	}
}

// ---------------------------------------------------------------------------
// RecordUserAgent edge: "(other)" is itself a valid UA before cap
// ---------------------------------------------------------------------------

func TestRecordUserAgent_OtherStringBeforeCap(t *testing.T) {
	s := newStats()

	// If someone's actual UA is "(other)" before the cap, it should be
	// tracked normally as a distinct entry.
	s.RecordUserAgent("(other)")
	s.RecordUserAgent("(other)")

	snap := s.UserAgentSnapshot()
	if snap["(other)"] != 2 {
		t.Fatalf("expected (other)=2 before cap, got %d", snap["(other)"])
	}
	// It should consume one slot in the unique count
	if s.uniqueUACount.Load() != 1 {
		t.Fatalf("expected uniqueUACount=1, got %d", s.uniqueUACount.Load())
	}
}

func TestRecordOutboundThrottle(t *testing.T) {
	t.Run("wait increments waited and accumulates duration", func(t *testing.T) {
		s := newStats()
		s.RecordOutboundWait("account", 150*time.Millisecond)
		s.RecordOutboundWait("account", 50*time.Millisecond)
		got := s.OutboundThrottleSnapshot()["account"]
		if got.Waited != 2 {
			t.Fatalf("waited = %d, want 2", got.Waited)
		}
		if got.WaitMicros != 200_000 {
			t.Fatalf("wait micros = %d, want 200000", got.WaitMicros)
		}
		if got.Rejected != 0 {
			t.Fatalf("rejected = %d, want 0", got.Rejected)
		}
	})

	t.Run("reject counts wait micros but not the waited count", func(t *testing.T) {
		s := newStats()
		s.RecordOutboundReject("minted", 250*time.Millisecond)
		got := s.OutboundThrottleSnapshot()["minted"]
		if got.Rejected != 1 {
			t.Fatalf("rejected = %d, want 1", got.Rejected)
		}
		if got.Waited != 0 {
			t.Fatalf("reject bumped waited count: %d", got.Waited)
		}
		if got.WaitMicros != 250_000 {
			t.Fatalf("reject wait micros = %d, want 250000", got.WaitMicros)
		}
	})

	t.Run("reject with zero wait leaves micros untouched", func(t *testing.T) {
		s := newStats()
		s.RecordOutboundReject("minted", 0)
		if got := s.OutboundThrottleSnapshot()["minted"]; got.WaitMicros != 0 {
			t.Fatalf("reject wait micros = %d, want 0", got.WaitMicros)
		}
	})

	t.Run("buckets are independent", func(t *testing.T) {
		s := newStats()
		s.RecordOutboundWait("scrape", time.Second)
		s.RecordOutboundReject("mint", 0)
		snap := s.OutboundThrottleSnapshot()
		if snap["scrape"].Waited != 1 || snap["scrape"].Rejected != 0 {
			t.Fatalf("scrape = %+v", snap["scrape"])
		}
		if snap["mint"].Rejected != 1 || snap["mint"].Waited != 0 {
			t.Fatalf("mint = %+v", snap["mint"])
		}
		if snap["account"].Waited != 0 || snap["account"].Rejected != 0 {
			t.Fatalf("account should be untouched: %+v", snap["account"])
		}
	})

	t.Run("snapshot always reports the four known buckets", func(t *testing.T) {
		s := newStats()
		snap := s.OutboundThrottleSnapshot()
		for _, name := range []string{"account", "minted", "scrape", "mint"} {
			if _, ok := snap[name]; !ok {
				t.Fatalf("bucket %q missing from snapshot", name)
			}
		}
		if len(snap) != 4 {
			t.Fatalf("snapshot has %d buckets, want 4", len(snap))
		}
	})

	t.Run("unknown bucket is a no-op", func(t *testing.T) {
		s := newStats()
		s.RecordOutboundWait("bogus", time.Second)
		s.RecordOutboundReject("bogus", time.Second)
		snap := s.OutboundThrottleSnapshot()
		if _, ok := snap["bogus"]; ok {
			t.Fatal("bogus bucket should not appear in snapshot")
		}
	})
}
