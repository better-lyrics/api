package stats

import (
	"sync/atomic"
	"time"
)

// Serialize captures the cumulative counters into a persistable snapshot.
func (s *Stats) Serialize() PersistedStats {
	return PersistedStats{
		TotalRequests:       s.TotalRequests.Load(),
		LyricsRequests:      s.LyricsRequests.Load(),
		CacheRequests:       s.CacheRequests.Load(),
		StatsRequests:       s.StatsRequests.Load(),
		HealthRequests:      s.HealthRequests.Load(),
		OtherRequests:       s.OtherRequests.Load(),
		CacheHits:           s.CacheHits.Load(),
		CacheMisses:         s.CacheMisses.Load(),
		NegativeCacheHits:   s.NegativeCacheHits.Load(),
		StaleCacheHits:      s.StaleCacheHits.Load(),
		RateLimitNormal:     s.RateLimitNormal.Load(),
		RateLimitCached:     s.RateLimitCached.Load(),
		RateLimitExceeded:   s.RateLimitExceeded.Load(),
		Status2xx:           s.Status2xx.Load(),
		Status4xx:           s.Status4xx.Load(),
		Status5xx:           s.Status5xx.Load(),
		TotalResponseTime:   s.totalResponseTime.Load(),
		ResponseCount:       s.responseCount.Load(),
		MinResponseTime:     s.minResponseTime.Load(),
		MaxResponseTime:     s.maxResponseTime.Load(),
		LyricsResponseTime:  s.lyricsResponseTime.Load(),
		LyricsResponseCount: s.lyricsResponseCount.Load(),
		AccountUsage:        s.AccountUsageSnapshot(),
		UserAgentUsage:      s.UserAgentSnapshot(),
		LastSaved:           time.Now(),
		FirstStarted:        s.StartTime,
	}
}

// Restore applies a persisted snapshot onto the live counters.
func (s *Stats) Restore(p PersistedStats) {
	p.AccountUsage = applyAccountMigrations(p.AccountUsage)

	s.TotalRequests.Store(p.TotalRequests)
	s.LyricsRequests.Store(p.LyricsRequests)
	s.CacheRequests.Store(p.CacheRequests)
	s.StatsRequests.Store(p.StatsRequests)
	s.HealthRequests.Store(p.HealthRequests)
	s.OtherRequests.Store(p.OtherRequests)
	s.CacheHits.Store(p.CacheHits)
	s.CacheMisses.Store(p.CacheMisses)
	s.NegativeCacheHits.Store(p.NegativeCacheHits)
	s.StaleCacheHits.Store(p.StaleCacheHits)
	s.RateLimitNormal.Store(p.RateLimitNormal)
	s.RateLimitCached.Store(p.RateLimitCached)
	s.RateLimitExceeded.Store(p.RateLimitExceeded)
	s.Status2xx.Store(p.Status2xx)
	s.Status4xx.Store(p.Status4xx)
	s.Status5xx.Store(p.Status5xx)
	s.totalResponseTime.Store(p.TotalResponseTime)
	s.responseCount.Store(p.ResponseCount)
	s.lyricsResponseTime.Store(p.LyricsResponseTime)
	s.lyricsResponseCount.Store(p.LyricsResponseCount)

	if p.MinResponseTime > 0 && p.MinResponseTime < int64(^uint64(0)>>1) {
		s.minResponseTime.Store(p.MinResponseTime)
	}
	if p.MaxResponseTime > 0 {
		s.maxResponseTime.Store(p.MaxResponseTime)
	}

	for name, count := range p.AccountUsage {
		counter := &atomic.Int64{}
		counter.Store(count)
		s.accountUsage.Store(name, counter)
	}
	for ua, count := range p.UserAgentUsage {
		counter := &atomic.Int64{}
		counter.Store(count)
		s.userAgentUsage.Store(ua, counter)
	}

	if !p.FirstStarted.IsZero() {
		s.StartTime = p.FirstStarted
	}
}
