package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lyrics-api-go/config"
	"lyrics-api-go/internal/store"
	"lyrics-api-go/services/providers"
)

func sweepTestServer() http.Handler {
	return sweepTestServerWith(func(*config.Config) {})
}

func sweepTestServerWith(mutate func(*config.Config)) http.Handler {
	var cfg config.Config
	cfg.Configuration.CacheAccessToken = "admin"
	cfg.Configuration.RateLimitPerSecond = 1000
	cfg.Configuration.RateLimitBurstLimit = 1000
	cfg.Configuration.CachedRateLimitPerSecond = 1000
	cfg.Configuration.CachedRateLimitBurstLimit = 1000
	mutate(&cfg)
	return New(nil, cfg).Handler()
}

func TestLRCRedSweepRefusedInCacheOnlyMode(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/cache/lrcred-sweep", nil)
	req.Header.Set("Authorization", "admin")
	rec := httptest.NewRecorder()
	sweepTestServerWith(func(c *config.Config) { c.FeatureFlags.CacheOnlyMode = true }).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "FF_CACHE_ONLY_MODE is on") {
		t.Fatalf("got %d %q, want 409: a deleted row could not be refetched", rec.Code, rec.Body.String())
	}
}

func TestLRCRedSweepRefusals(t *testing.T) {
	tests := []struct {
		name       string
		auth       string
		query      string
		wantStatus int
		wantBody   string
	}{
		{name: "no admin token", wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "wrong admin token", auth: "nope", wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "limit zero", auth: "admin", query: "limit=0", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 50"},
		{name: "limit above max", auth: "admin", query: "limit=51", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 50"},
		{name: "limit not a number", auth: "admin", query: "limit=ten", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 50"},
		{name: "regression: unknown dry_run value is rejected, not run for real", auth: "admin", query: "dry_run=yes", wantStatus: http.StatusBadRequest, wantBody: "dry_run must be true or false"},
		{name: "empty dry_run value is rejected", auth: "admin", query: "dry_run=", wantStatus: http.StatusBadRequest, wantBody: "dry_run must be true or false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/cache/lrcred-sweep?"+tt.query, nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			sweepTestServer().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}

	t.Run("GET is not routed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/cache/lrcred-sweep", nil)
		req.Header.Set("Authorization", "admin")
		rec := httptest.NewRecorder()
		sweepTestServer().ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatal("a GET ran the destructive sweep")
		}
	})
}

func TestCheckedKeys(t *testing.T) {
	t.Run("a key is claimed once", func(t *testing.T) {
		c := &checkedKeys{max: 10}
		if !c.claim("a") {
			t.Fatal("first claim refused")
		}
		if c.claim("a") {
			t.Fatal("second claim of the same key allowed")
		}
		if !c.claim("b") {
			t.Fatal("another key refused")
		}
	})

	t.Run("regression: looking without claiming does not use up the cap", func(t *testing.T) {
		c := &checkedKeys{max: 2}
		for range 100 {
			if isClaimed(c, "a") {
				t.Fatal("unclaimed key reported as seen")
			}
		}
		c.claim("a")
		c.claim("b")
		if !isClaimed(c, "a") || !isClaimed(c, "b") {
			t.Fatal("claimed keys forgotten before the cap was passed")
		}
	})

	t.Run("a released key can be claimed again and frees its place under the cap", func(t *testing.T) {
		c := &checkedKeys{max: 2}
		c.claim("a")
		c.release("a")
		if isClaimed(c, "a") {
			t.Fatal("released key still seen")
		}
		c.claim("b")
		c.claim("c")
		if !isClaimed(c, "b") || !isClaimed(c, "c") {
			t.Fatal("a released key still counted toward the cap")
		}
		if !c.claim("a") {
			t.Fatal("released key could not be claimed again")
		}
	})

	t.Run("regression: concurrent claims never hold more than the cap", func(t *testing.T) {
		c := &checkedKeys{max: 10}
		var wg sync.WaitGroup
		for i := range 500 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				key := fmt.Sprintf("k%d", i)
				c.claim(key)
				if i%3 == 0 {
					c.release(key)
				}
			}()
		}
		wg.Wait()
		held := 0
		for i := range 500 {
			if isClaimed(c, fmt.Sprintf("k%d", i)) {
				held++
			}
		}
		if held > c.max {
			t.Fatalf("%d keys held, cap is %d", held, c.max)
		}
	})

	t.Run("passing the cap forgets every key", func(t *testing.T) {
		c := &checkedKeys{max: 2}
		c.claim("a")
		c.claim("b")
		c.claim("c")
		if isClaimed(c, "a") || isClaimed(c, "b") {
			t.Fatal("keys survived passing the cap")
		}
	})
}

func TestCheckLRCRedCopyIsFreeWhenBlocked(t *testing.T) {
	s := New(nil, config.Config{})
	cached := store.CachedLyrics{Source: providers.SourceLRCRed}
	allocs := testing.AllocsPerRun(100, func() { s.checkLRCRedCopy("ttml_lyrics:blocked song tester", cached) })
	if allocs != 0 {
		t.Errorf("blocked check allocated %.0f times per hit, want 0", allocs)
	}
	if isClaimed(lrcRedChecked, "ttml_lyrics:blocked song tester") {
		t.Error("blocked check claimed the key")
	}
}

func isClaimed(c *checkedKeys, key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.m[key]
	return ok
}
