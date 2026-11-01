package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lyrics-api-go/config"
)

func sweepTestServer(skipAISynced bool, baseURL string) http.Handler {
	var cfg config.Config
	cfg.Configuration.CacheAccessToken = "admin"
	cfg.Configuration.LRCRedBaseURL = baseURL
	cfg.Configuration.RateLimitPerSecond = 1000
	cfg.Configuration.RateLimitBurstLimit = 1000
	cfg.Configuration.CachedRateLimitPerSecond = 1000
	cfg.Configuration.CachedRateLimitBurstLimit = 1000
	cfg.FeatureFlags.LRCRedSkipAISynced = skipAISynced
	return New(nil, cfg).Handler()
}

func TestLRCRedSweepRefusals(t *testing.T) {
	tests := []struct {
		name         string
		skipAISynced bool
		baseURL      string
		auth         string
		query        string
		wantStatus   int
		wantBody     string
	}{
		{name: "no admin token", skipAISynced: true, baseURL: "https://lrc.red", wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "wrong admin token", skipAISynced: true, baseURL: "https://lrc.red", auth: "nope", wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "flag off", skipAISynced: false, baseURL: "https://lrc.red", auth: "admin", wantStatus: http.StatusConflict, wantBody: "FF_LRC_RED_SKIP_AI_SYNCED is off"},
		{name: "no lrc.red base url", skipAISynced: true, auth: "admin", wantStatus: http.StatusConflict, wantBody: "LRC_RED_BASE_URL is not set"},
		{name: "limit zero", skipAISynced: true, baseURL: "https://lrc.red", auth: "admin", query: "limit=0", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 50"},
		{name: "limit above max", skipAISynced: true, baseURL: "https://lrc.red", auth: "admin", query: "limit=51", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 50"},
		{name: "limit not a number", skipAISynced: true, baseURL: "https://lrc.red", auth: "admin", query: "limit=ten", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/cache/lrcred-sweep?"+tt.query, nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			sweepTestServer(tt.skipAISynced, tt.baseURL).ServeHTTP(rec, req)

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
		sweepTestServer(true, "https://lrc.red").ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatal("a GET ran the destructive sweep")
		}
	})
}
