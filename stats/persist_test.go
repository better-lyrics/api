package stats

import (
	"testing"
	"time"
)

func TestSerializeRestore_RoundTrip(t *testing.T) {
	s1 := newStats()
	s1.TotalRequests.Store(100)
	s1.LyricsRequests.Store(70)
	s1.CacheHits.Store(40)
	s1.CacheMisses.Store(10)
	s1.NegativeCacheHits.Store(5)
	s1.Status2xx.Store(90)
	s1.Status4xx.Store(8)
	s1.Status5xx.Store(2)
	s1.RecordResponseTime(100*time.Millisecond, "/getLyrics")
	s1.RecordResponseTime(300*time.Millisecond, "/getLyrics")
	s1.RecordAccountUsage("acct-1")
	s1.RecordAccountUsage("acct-1")
	s1.RecordAccountUsage("acct-2")
	s1.RecordUserAgent("ua-x")
	s1.RecordOutboundWait("account", 300*time.Millisecond)
	s1.RecordOutboundWait("account", 200*time.Millisecond)
	s1.RecordOutboundReject("minted")

	p := s1.Serialize()

	s2 := newStats()
	s2.Restore(p)
	got := s2.Serialize()

	if got.TotalRequests != 100 || got.LyricsRequests != 70 {
		t.Fatalf("request counters not restored: %+v", got)
	}
	if got.CacheHits != 40 || got.CacheMisses != 10 || got.NegativeCacheHits != 5 {
		t.Fatalf("cache counters not restored: %+v", got)
	}
	if got.Status2xx != 90 || got.Status4xx != 8 || got.Status5xx != 2 {
		t.Fatalf("status counters not restored: %+v", got)
	}
	if p.MinResponseTime <= 0 || p.MaxResponseTime <= 0 {
		t.Fatalf("precondition: timing not captured before restore: %+v", p)
	}
	if got.MinResponseTime != p.MinResponseTime {
		t.Fatalf("min response time not restored: got %d, want %d", got.MinResponseTime, p.MinResponseTime)
	}
	if got.MaxResponseTime != p.MaxResponseTime {
		t.Fatalf("max response time not restored: got %d, want %d", got.MaxResponseTime, p.MaxResponseTime)
	}
	if got.AccountUsage["acct-1"] != 2 || got.AccountUsage["acct-2"] != 1 {
		t.Fatalf("account usage not restored: %+v", got.AccountUsage)
	}
	if got.UserAgentUsage["ua-x"] != 1 {
		t.Fatalf("user agent usage not restored: %+v", got.UserAgentUsage)
	}
	if ob := s2.OutboundThrottleSnapshot()["account"]; ob.Waited != 2 || ob.WaitMicros != 500_000 {
		t.Fatalf("outbound account not restored: %+v", ob)
	}
	if ob := s2.OutboundThrottleSnapshot()["minted"]; ob.Rejected != 1 {
		t.Fatalf("outbound minted reject not restored: %+v", ob)
	}
	if !got.FirstStarted.Equal(s1.StartTime) {
		t.Fatalf("FirstStarted not preserved: got %v, want %v", got.FirstStarted, s1.StartTime)
	}
}

// The min-response sentinel must survive a restore that carries no timing data,
// otherwise a persisted zero would clamp every future minimum to 0ms.
func TestRestore_ZeroMinDoesNotClobberSentinel(t *testing.T) {
	sentinel := int64(^uint64(0) >> 1)

	s := newStats()
	s.Restore(PersistedStats{MinResponseTime: 0, MaxResponseTime: 0})

	if got := s.Serialize().MinResponseTime; got != sentinel {
		t.Fatalf("expected sentinel min preserved, got %d", got)
	}
}

func TestRestore_EmptyOutboundThrottleLeavesZeros(t *testing.T) {
	s := newStats()
	s.Restore(PersistedStats{})
	for _, name := range []string{"account", "minted", "scrape", "mint"} {
		if ob := s.OutboundThrottleSnapshot()[name]; ob.Waited != 0 || ob.Rejected != 0 || ob.WaitMicros != 0 {
			t.Fatalf("bucket %q not zero after empty restore: %+v", name, ob)
		}
	}
}

func TestRestore_IgnoresZeroFirstStarted(t *testing.T) {
	s := newStats()
	original := s.StartTime
	s.Restore(PersistedStats{}) // zero-value FirstStarted

	if !s.StartTime.Equal(original) {
		t.Fatalf("zero FirstStarted overwrote StartTime: got %v, want %v", s.StartTime, original)
	}
}
