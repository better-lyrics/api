package middleware

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestNewIPRateLimiter tests the creation of a new IPRateLimiter.
func TestNewIPRateLimiter(t *testing.T) {
	rl := NewIPRateLimiter(1, 5, 10, 20)
	if rl == nil {
		t.Errorf("Expected IPRateLimiter to be created, got nil")
	}
	if rl.normalRate != 1 {
		t.Errorf("Expected normal rate limit to be 1, got %v", rl.normalRate)
	}
	if rl.normalBurst != 5 {
		t.Errorf("Expected normal burst limit to be 5, got %v", rl.normalBurst)
	}
	if rl.cachedRate != 10 {
		t.Errorf("Expected cached rate limit to be 10, got %v", rl.cachedRate)
	}
	if rl.cachedBurst != 20 {
		t.Errorf("Expected cached burst limit to be 20, got %v", rl.cachedBurst)
	}
}

// TestAddIP tests adding a new IP to the rate limiter.
func TestAddIP(t *testing.T) {
	rl := NewIPRateLimiter(1, 5, 10, 20)
	ip := "192.168.1.1"
	limiterPair := rl.AddIP(ip)
	if limiterPair == nil {
		t.Errorf("Expected limiter pair to be created for IP, got nil")
	}
	if limiterPair.Normal == nil {
		t.Errorf("Expected normal rate limiter to be created, got nil")
	}
	if limiterPair.Cached == nil {
		t.Errorf("Expected cached rate limiter to be created, got nil")
	}
	if _, exists := rl.ips[ip]; !exists {
		t.Errorf("Expected IP to be added to ips map, but it was not found")
	}
}

// TestGetLimiter tests retrieving the rate limiter for an IP.
func TestGetLimiter(t *testing.T) {
	rl := NewIPRateLimiter(1, 5, 10, 20)
	ip := "192.168.1.1"
	limiterPair := rl.GetLimiter(ip)
	if limiterPair == nil {
		t.Errorf("Expected limiter pair to be returned, got nil")
	}
	if limiterPair.Normal == nil {
		t.Errorf("Expected normal rate limiter to be returned, got nil")
	}
	if limiterPair.Cached == nil {
		t.Errorf("Expected cached rate limiter to be returned, got nil")
	}
	if _, exists := rl.ips[ip]; !exists {
		t.Errorf("Expected IP to be in ips map, but it was not found")
	}
}

// TestRateLimiting tests the actual rate limiting functionality.
func TestRateLimiting(t *testing.T) {
	rl := NewIPRateLimiter(rate.Limit(1), 1, rate.Limit(5), 5) // Normal: 1 req/s burst 1, Cached: 5 req/s burst 5
	ip := "192.168.1.1"
	limiterPair := rl.GetLimiter(ip)

	// Allow the first request on normal tier
	if !limiterPair.Normal.Allow() {
		t.Errorf("Expected first request to be allowed on normal tier")
	}

	// Second request should not be allowed on normal tier immediately
	if limiterPair.Normal.Allow() {
		t.Errorf("Expected second request to be denied on normal tier due to rate limiting")
	}

	// But cached tier should still allow requests (has burst of 5)
	if !limiterPair.Cached.Allow() {
		t.Errorf("Expected request to be allowed on cached tier")
	}

	// Wait for 1 second and then the normal tier request should be allowed again
	time.Sleep(1 * time.Second)
	if !limiterPair.Normal.Allow() {
		t.Errorf("Expected request to be allowed on normal tier after waiting")
	}
}

// TestTwoTierRateLimiting tests the two-tier rate limiting behavior.
func TestTwoTierRateLimiting(t *testing.T) {
	rl := NewIPRateLimiter(rate.Limit(1), 1, rate.Limit(2), 2)
	ip := "192.168.1.2"
	limiterPair := rl.GetLimiter(ip)

	// Normal tier: burst of 1
	if !limiterPair.Normal.Allow() {
		t.Errorf("Expected first normal request to be allowed")
	}

	// Normal tier exhausted, but cached tier should work
	if limiterPair.Normal.Allow() {
		t.Errorf("Expected second normal request to be denied")
	}

	// Cached tier should allow (burst of 2)
	if !limiterPair.Cached.Allow() {
		t.Errorf("Expected first cached request to be allowed")
	}
	if !limiterPair.Cached.Allow() {
		t.Errorf("Expected second cached request to be allowed")
	}

	// Both tiers exhausted
	if limiterPair.Normal.Allow() {
		t.Errorf("Expected normal tier to be exhausted")
	}
	if limiterPair.Cached.Allow() {
		t.Errorf("Expected cached tier to be exhausted")
	}
}

// TestLimiterPairTokens tests the token counting methods.
func TestLimiterPairTokens(t *testing.T) {
	rl := NewIPRateLimiter(rate.Limit(10), 10, rate.Limit(20), 20)
	ip := "192.168.1.3"
	limiterPair := rl.GetLimiter(ip)

	// Check initial tokens (should be at burst capacity)
	normalTokens := limiterPair.GetNormalTokens()
	cachedTokens := limiterPair.GetCachedTokens()

	if normalTokens != 10 {
		t.Errorf("Expected 10 normal tokens initially, got %d", normalTokens)
	}
	if cachedTokens != 20 {
		t.Errorf("Expected 20 cached tokens initially, got %d", cachedTokens)
	}

	// Consume a token
	limiterPair.Normal.Allow()
	normalTokens = limiterPair.GetNormalTokens()
	if normalTokens != 9 {
		t.Errorf("Expected 9 normal tokens after one request, got %d", normalTokens)
	}
}

// TestGetLimits tests the limit getter methods.
func TestGetLimits(t *testing.T) {
	rl := NewIPRateLimiter(rate.Limit(2), 5, rate.Limit(10), 20)

	normalLimit := rl.GetNormalLimit()
	cachedLimit := rl.GetCachedLimit()

	if normalLimit != 5 {
		t.Errorf("Expected normal limit to be 5, got %d", normalLimit)
	}
	if cachedLimit != 20 {
		t.Errorf("Expected cached limit to be 20, got %d", cachedLimit)
	}
}
