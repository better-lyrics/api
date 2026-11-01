package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lyrics-api-go/config"
)

func sweepTestServer() http.Handler {
	var cfg config.Config
	cfg.Configuration.CacheAccessToken = "admin"
	cfg.Configuration.RateLimitPerSecond = 1000
	cfg.Configuration.RateLimitBurstLimit = 1000
	cfg.Configuration.CachedRateLimitPerSecond = 1000
	cfg.Configuration.CachedRateLimitBurstLimit = 1000
	return New(nil, cfg).Handler()
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
