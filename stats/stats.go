package stats

import (
	"sync"
	"sync/atomic"
	"time"
)

// Stats holds all server statistics with atomic counters
type Stats struct {
	// Server info
	StartTime time.Time

	// Request counters
	TotalRequests     atomic.Int64
	LyricsRequests    atomic.Int64
	CacheRequests     atomic.Int64
	StatsRequests     atomic.Int64
	HealthRequests    atomic.Int64
	OtherRequests     atomic.Int64

	// Cache performance
	CacheHits         atomic.Int64
	CacheMisses       atomic.Int64
	NegativeCacheHits atomic.Int64
	StaleCacheHits    atomic.Int64

	// Rate limiting
	RateLimitNormal   atomic.Int64 // Requests served under normal rate limit
	RateLimitCached   atomic.Int64 // Requests served under cached-only tier
	RateLimitExceeded atomic.Int64 // Requests rejected (429)

	// Response status codes
	Status2xx atomic.Int64
	Status4xx atomic.Int64
	Status5xx atomic.Int64

	// Response time tracking (in microseconds for precision)
	totalResponseTime atomic.Int64
	responseCount     atomic.Int64
	minResponseTime   atomic.Int64
	maxResponseTime   atomic.Int64
	responseMu        sync.RWMutex

	// Endpoint response times (microseconds)
	lyricsResponseTime  atomic.Int64
	lyricsResponseCount atomic.Int64
}

// Global stats instance
var global = &Stats{
	StartTime: time.Now(),
}

func init() {
	// Initialize min to a high value
	global.minResponseTime.Store(int64(^uint64(0) >> 1)) // Max int64
}

// Get returns the global stats instance
func Get() *Stats {
	return global
}

// RecordRequest records a request to a specific endpoint
func (s *Stats) RecordRequest(endpoint string) {
	s.TotalRequests.Add(1)
	switch endpoint {
	case "/getLyrics":
		s.LyricsRequests.Add(1)
	case "/cache":
		s.CacheRequests.Add(1)
	case "/stats":
		s.StatsRequests.Add(1)
	case "/health":
		s.HealthRequests.Add(1)
	default:
		s.OtherRequests.Add(1)
	}
}

// RecordCacheHit records a cache hit
func (s *Stats) RecordCacheHit() {
	s.CacheHits.Add(1)
}

// RecordCacheMiss records a cache miss
func (s *Stats) RecordCacheMiss() {
	s.CacheMisses.Add(1)
}

// RecordNegativeCacheHit records a negative cache hit
func (s *Stats) RecordNegativeCacheHit() {
	s.NegativeCacheHits.Add(1)
}

// RecordStaleCacheHit records a stale cache hit (fallback)
func (s *Stats) RecordStaleCacheHit() {
	s.StaleCacheHits.Add(1)
}

// RecordRateLimit records rate limit tier usage
func (s *Stats) RecordRateLimit(tier string) {
	switch tier {
	case "normal":
		s.RateLimitNormal.Add(1)
	case "cached":
		s.RateLimitCached.Add(1)
	case "exceeded":
		s.RateLimitExceeded.Add(1)
	}
}

// RecordStatusCode records a response status code
func (s *Stats) RecordStatusCode(code int) {
	switch {
	case code >= 200 && code < 300:
		s.Status2xx.Add(1)
	case code >= 400 && code < 500:
		s.Status4xx.Add(1)
	case code >= 500:
		s.Status5xx.Add(1)
	}
}

// RecordResponseTime records a response time
func (s *Stats) RecordResponseTime(duration time.Duration, endpoint string) {
	us := duration.Microseconds()

	s.totalResponseTime.Add(us)
	s.responseCount.Add(1)

	// Update min/max atomically
	for {
		current := s.minResponseTime.Load()
		if us >= current || s.minResponseTime.CompareAndSwap(current, us) {
			break
		}
	}
	for {
		current := s.maxResponseTime.Load()
		if us <= current || s.maxResponseTime.CompareAndSwap(current, us) {
			break
		}
	}

	// Track lyrics-specific response times
	if endpoint == "/getLyrics" {
		s.lyricsResponseTime.Add(us)
		s.lyricsResponseCount.Add(1)
	}
}

// Uptime returns the server uptime
func (s *Stats) Uptime() time.Duration {
	return time.Since(s.StartTime)
}

// CacheHitRate returns the cache hit rate as a percentage
func (s *Stats) CacheHitRate() float64 {
	hits := s.CacheHits.Load()
	misses := s.CacheMisses.Load()
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total) * 100
}

// AvgResponseTime returns the average response time
func (s *Stats) AvgResponseTime() time.Duration {
	count := s.responseCount.Load()
	if count == 0 {
		return 0
	}
	return time.Duration(s.totalResponseTime.Load()/count) * time.Microsecond
}

// MinResponseTime returns the minimum response time
func (s *Stats) MinResponseTime() time.Duration {
	min := s.minResponseTime.Load()
	if min == int64(^uint64(0)>>1) {
		return 0
	}
	return time.Duration(min) * time.Microsecond
}

// MaxResponseTime returns the maximum response time
func (s *Stats) MaxResponseTime() time.Duration {
	return time.Duration(s.maxResponseTime.Load()) * time.Microsecond
}

// AvgLyricsResponseTime returns the average response time for lyrics requests
func (s *Stats) AvgLyricsResponseTime() time.Duration {
	count := s.lyricsResponseCount.Load()
	if count == 0 {
		return 0
	}
	return time.Duration(s.lyricsResponseTime.Load()/count) * time.Microsecond
}

// Snapshot returns a point-in-time snapshot of all stats
func (s *Stats) Snapshot() map[string]interface{} {
	uptime := s.Uptime()

	return map[string]interface{}{
		"server": map[string]interface{}{
			"start_time":   s.StartTime.Format(time.RFC3339),
			"uptime":       uptime.String(),
			"uptime_seconds": int64(uptime.Seconds()),
		},
		"requests": map[string]interface{}{
			"total":   s.TotalRequests.Load(),
			"lyrics":  s.LyricsRequests.Load(),
			"cache":   s.CacheRequests.Load(),
			"stats":   s.StatsRequests.Load(),
			"health":  s.HealthRequests.Load(),
			"other":   s.OtherRequests.Load(),
		},
		"cache": map[string]interface{}{
			"hits":          s.CacheHits.Load(),
			"misses":        s.CacheMisses.Load(),
			"negative_hits": s.NegativeCacheHits.Load(),
			"stale_hits":    s.StaleCacheHits.Load(),
			"hit_rate":      s.CacheHitRate(),
		},
		"rate_limiting": map[string]interface{}{
			"normal_tier":   s.RateLimitNormal.Load(),
			"cached_tier":   s.RateLimitCached.Load(),
			"exceeded":      s.RateLimitExceeded.Load(),
		},
		"responses": map[string]interface{}{
			"2xx": s.Status2xx.Load(),
			"4xx": s.Status4xx.Load(),
			"5xx": s.Status5xx.Load(),
		},
		"response_times": map[string]interface{}{
			"avg":       s.AvgResponseTime().String(),
			"min":       s.MinResponseTime().String(),
			"max":       s.MaxResponseTime().String(),
			"avg_lyrics": s.AvgLyricsResponseTime().String(),
		},
	}
}
