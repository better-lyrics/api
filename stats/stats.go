package stats

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Stats holds all server statistics with atomic counters
type Stats struct {
	// Server info
	StartTime time.Time

	// Request counters
	TotalRequests  atomic.Int64
	LyricsRequests atomic.Int64
	CacheRequests  atomic.Int64
	StatsRequests  atomic.Int64
	HealthRequests atomic.Int64
	OtherRequests  atomic.Int64

	// Cache performance
	CacheHits         atomic.Int64
	CacheMisses       atomic.Int64
	NegativeCacheHits atomic.Int64
	StaleCacheHits    atomic.Int64

	// lrc.red flow
	LRCRedFetchAttempts     atomic.Int64
	LRCRedFetchHits         atomic.Int64
	LRCRedFetchMisses       atomic.Int64
	LRCRedFetchErrors       atomic.Int64
	LRCRedContributeSent    atomic.Int64
	LRCRedContributeSkipped atomic.Int64

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

	// Request rate tracking (sliding window)
	requestTimes   []time.Time
	requestTimesMu sync.Mutex

	// Account usage tracking
	accountUsage sync.Map // map[string]*atomic.Int64

	// User agent tracking
	userAgentUsage sync.Map // map[string]*atomic.Int64
	uniqueUACount  atomic.Int64
	uaMu           sync.Mutex

	// Outbound throttle pressure, per token bucket
	outboundAccount outboundBucketStat
	outboundMinted  outboundBucketStat
	outboundScrape  outboundBucketStat
	outboundMint    outboundBucketStat
}

type outboundBucketStat struct {
	waited     atomic.Int64
	rejected   atomic.Int64
	waitMicros atomic.Int64
}

func (b *outboundBucketStat) load() OutboundThrottleStat {
	return OutboundThrottleStat{
		Waited:     b.waited.Load(),
		Rejected:   b.rejected.Load(),
		WaitMicros: b.waitMicros.Load(),
	}
}

// OutboundThrottleStat is the persistable/serializable view of one bucket.
type OutboundThrottleStat struct {
	Waited     int64 `json:"waited"`
	Rejected   int64 `json:"rejected"`
	WaitMicros int64 `json:"wait_micros"`
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

	// Track request time for rate calculation
	s.requestTimesMu.Lock()
	now := time.Now()
	s.requestTimes = append(s.requestTimes, now)
	// Keep only last hour of requests to limit memory
	cutoff := now.Add(-time.Hour)
	idx := sort.Search(len(s.requestTimes), func(i int) bool {
		return s.requestTimes[i].After(cutoff)
	})
	if idx > 0 {
		remaining := make([]time.Time, len(s.requestTimes)-idx)
		copy(remaining, s.requestTimes[idx:])
		s.requestTimes = remaining
	}
	s.requestTimesMu.Unlock()
}

// RecordAccountUsage records a successful request using a specific account
func (s *Stats) RecordAccountUsage(accountName string) {
	counter, _ := s.accountUsage.LoadOrStore(accountName, &atomic.Int64{})
	counter.(*atomic.Int64).Add(1)
}

// RequestsPerMinute returns the number of requests in the last minute
func (s *Stats) RequestsPerMinute() int64 {
	s.requestTimesMu.Lock()
	defer s.requestTimesMu.Unlock()

	cutoff := time.Now().Add(-time.Minute)
	count := int64(0)
	for i := len(s.requestTimes) - 1; i >= 0; i-- {
		if s.requestTimes[i].After(cutoff) {
			count++
		} else {
			break
		}
	}
	return count
}

// RequestsPerHour returns the number of requests in the last hour
func (s *Stats) RequestsPerHour() int64 {
	s.requestTimesMu.Lock()
	defer s.requestTimesMu.Unlock()

	return int64(len(s.requestTimes))
}

// AccountUsageSnapshot returns a map of account names to request counts
func (s *Stats) AccountUsageSnapshot() map[string]int64 {
	result := make(map[string]int64)
	s.accountUsage.Range(func(key, value interface{}) bool {
		result[key.(string)] = value.(*atomic.Int64).Load()
		return true
	})
	return result
}

// maxUniqueUserAgents is the cap on distinct user agent strings tracked.
const maxUniqueUserAgents = 1000

// RecordUserAgent records a request from a specific user agent.
// After maxUniqueUserAgents distinct agents, new ones are bucketed as "(other)".
func (s *Stats) RecordUserAgent(userAgent string) {
	if userAgent == "" {
		userAgent = "(empty)"
	}

	// Fast path: UA already tracked
	if counter, ok := s.userAgentUsage.Load(userAgent); ok {
		counter.(*atomic.Int64).Add(1)
		return
	}

	// Slow path: new UA — acquire lock for cap-safe insertion
	s.uaMu.Lock()

	// Re-check after lock (another goroutine may have added this UA)
	if counter, ok := s.userAgentUsage.Load(userAgent); ok {
		s.uaMu.Unlock()
		counter.(*atomic.Int64).Add(1)
		return
	}

	// Check cap under lock — no TOCTOU possible
	if s.uniqueUACount.Load() >= maxUniqueUserAgents {
		s.uaMu.Unlock()
		counter, _ := s.userAgentUsage.LoadOrStore("(other)", &atomic.Int64{})
		counter.(*atomic.Int64).Add(1)
		return
	}

	// Store new UA and increment count atomically (under lock)
	counter := &atomic.Int64{}
	s.userAgentUsage.Store(userAgent, counter)
	s.uniqueUACount.Add(1)
	s.uaMu.Unlock()
	counter.Add(1)
}

// UserAgentSnapshot returns a map of user agents to request counts
func (s *Stats) UserAgentSnapshot() map[string]int64 {
	result := make(map[string]int64)
	s.userAgentUsage.Range(func(key, value interface{}) bool {
		result[key.(string)] = value.(*atomic.Int64).Load()
		return true
	})
	return result
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

func (s *Stats) RecordLRCRedFetch(hit bool, err error) {
	s.LRCRedFetchAttempts.Add(1)
	switch {
	case err != nil:
		s.LRCRedFetchErrors.Add(1)
	case hit:
		s.LRCRedFetchHits.Add(1)
	default:
		s.LRCRedFetchMisses.Add(1)
	}
}

func (s *Stats) RecordLRCRedContribute(sent bool) {
	if sent {
		s.LRCRedContributeSent.Add(1)
	} else {
		s.LRCRedContributeSkipped.Add(1)
	}
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

func (s *Stats) outboundBucket(name string) *outboundBucketStat {
	switch name {
	case "account":
		return &s.outboundAccount
	case "minted":
		return &s.outboundMinted
	case "scrape":
		return &s.outboundScrape
	case "mint":
		return &s.outboundMint
	}
	return nil
}

// RecordOutboundWait records that an outbound acquire had to sleep before succeeding.
func (s *Stats) RecordOutboundWait(bucket string, waited time.Duration) {
	if b := s.outboundBucket(bucket); b != nil {
		b.waited.Add(1)
		b.waitMicros.Add(waited.Microseconds())
	}
}

// RecordOutboundReject records that an outbound acquire hit the deadline and was throttled.
// waited is the time spent blocking before the deadline was reached; it counts toward
// total wait time but not toward the waited (successful-wait) counter.
func (s *Stats) RecordOutboundReject(bucket string, waited time.Duration) {
	if b := s.outboundBucket(bucket); b != nil {
		b.rejected.Add(1)
		b.waitMicros.Add(waited.Microseconds())
	}
}

// OutboundThrottleSnapshot returns per-bucket throttle counters for all known buckets.
func (s *Stats) OutboundThrottleSnapshot() map[string]OutboundThrottleStat {
	return map[string]OutboundThrottleStat{
		"account": s.outboundAccount.load(),
		"minted":  s.outboundMinted.load(),
		"scrape":  s.outboundScrape.load(),
		"mint":    s.outboundMint.load(),
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
	reqPerMin := s.RequestsPerMinute()
	reqPerHour := s.RequestsPerHour()

	return map[string]interface{}{
		"server": map[string]interface{}{
			"start_time":     s.StartTime.Format(time.RFC3339),
			"uptime":         uptime.String(),
			"uptime_seconds": int64(uptime.Seconds()),
		},
		"requests": map[string]interface{}{
			"total":      s.TotalRequests.Load(),
			"lyrics":     s.LyricsRequests.Load(),
			"cache":      s.CacheRequests.Load(),
			"stats":      s.StatsRequests.Load(),
			"health":     s.HealthRequests.Load(),
			"other":      s.OtherRequests.Load(),
			"per_minute": reqPerMin,
			"per_hour":   reqPerHour,
		},
		"cache": map[string]interface{}{
			"hits":          s.CacheHits.Load(),
			"misses":        s.CacheMisses.Load(),
			"negative_hits": s.NegativeCacheHits.Load(),
			"stale_hits":    s.StaleCacheHits.Load(),
			"hit_rate":      s.CacheHitRate(),
		},
		"lrcred": map[string]interface{}{
			"fetch_attempts":     s.LRCRedFetchAttempts.Load(),
			"fetch_hits":         s.LRCRedFetchHits.Load(),
			"fetch_misses":       s.LRCRedFetchMisses.Load(),
			"fetch_errors":       s.LRCRedFetchErrors.Load(),
			"contribute_sent":    s.LRCRedContributeSent.Load(),
			"contribute_skipped": s.LRCRedContributeSkipped.Load(),
		},
		"rate_limiting": map[string]interface{}{
			"normal_tier": s.RateLimitNormal.Load(),
			"cached_tier": s.RateLimitCached.Load(),
			"exceeded":    s.RateLimitExceeded.Load(),
		},
		"outbound_throttle": s.OutboundThrottleSnapshot(),
		"responses": map[string]interface{}{
			"2xx": s.Status2xx.Load(),
			"4xx": s.Status4xx.Load(),
			"5xx": s.Status5xx.Load(),
		},
		"response_times": map[string]interface{}{
			"avg":        s.AvgResponseTime().String(),
			"min":        s.MinResponseTime().String(),
			"max":        s.MaxResponseTime().String(),
			"avg_lyrics": s.AvgLyricsResponseTime().String(),
		},
		"accounts": s.AccountUsageSnapshot(),
	}
}
