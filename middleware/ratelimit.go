package middleware

import (
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// LimiterPair holds both normal and cached tier limiters for an IP
type LimiterPair struct {
	Normal   *rate.Limiter
	Cached   *rate.Limiter
	lastSeen time.Time
}

// GetNormalTokens returns the number of tokens available in the normal tier
func (lp *LimiterPair) GetNormalTokens() int {
	return int(math.Floor(lp.Normal.Tokens()))
}

// GetCachedTokens returns the number of tokens available in the cached tier
func (lp *LimiterPair) GetCachedTokens() int {
	return int(math.Floor(lp.Cached.Tokens()))
}

// IPRateLimiter manages two-tier rate limiting per IP
type IPRateLimiter struct {
	ips         map[string]*LimiterPair
	mu          *sync.RWMutex
	normalRate  rate.Limit
	normalBurst int
	cachedRate  rate.Limit
	cachedBurst int
}

// GetNormalLimit returns the normal tier burst limit
func (i *IPRateLimiter) GetNormalLimit() int {
	return i.normalBurst
}

// GetCachedLimit returns the cached tier burst limit
func (i *IPRateLimiter) GetCachedLimit() int {
	return i.cachedBurst
}

// NewIPRateLimiter creates a new two-tier rate limiter
func NewIPRateLimiter(normalRate rate.Limit, normalBurst int, cachedRate rate.Limit, cachedBurst int) *IPRateLimiter {
	i := &IPRateLimiter{
		ips:         make(map[string]*LimiterPair),
		mu:          &sync.RWMutex{},
		normalRate:  normalRate,
		normalBurst: normalBurst,
		cachedRate:  cachedRate,
		cachedBurst: cachedBurst,
	}

	return i
}

func (i *IPRateLimiter) AddIP(ip string) *LimiterPair {
	i.mu.Lock()
	defer i.mu.Unlock()

	pair := &LimiterPair{
		Normal:   rate.NewLimiter(i.normalRate, i.normalBurst),
		Cached:   rate.NewLimiter(i.cachedRate, i.cachedBurst),
		lastSeen: time.Now(),
	}

	i.ips[ip] = pair

	return pair
}

func (i *IPRateLimiter) GetLimiter(ip string) *LimiterPair {
	i.mu.Lock()
	limiter, exists := i.ips[ip]

	if !exists {
		i.mu.Unlock()
		return i.AddIP(ip)
	}

	limiter.lastSeen = time.Now()
	i.mu.Unlock()

	return limiter
}

// StartCleanup launches a background goroutine that periodically removes
// IP entries that haven't been seen within the given idle timeout.
func (i *IPRateLimiter) StartCleanup(interval, idleTimeout time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			i.cleanup(idleTimeout)
		}
	}()
}

// cleanup removes IP entries that haven't been accessed within the idle timeout.
func (i *IPRateLimiter) cleanup(idleTimeout time.Duration) {
	i.mu.Lock()
	defer i.mu.Unlock()

	cutoff := time.Now().Add(-idleTimeout)
	for ip, pair := range i.ips {
		if pair.lastSeen.Before(cutoff) {
			delete(i.ips, ip)
		}
	}
}

// Len returns the number of tracked IPs (for testing).
func (i *IPRateLimiter) Len() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.ips)
}
