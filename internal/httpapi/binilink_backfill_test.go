package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBiniLinkBackfillRefusals(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		auth       string
		query      string
		wantStatus int
		wantBody   string
	}{
		{name: "no admin token", method: http.MethodPost, wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "wrong admin token", method: http.MethodPost, auth: "nope", wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "limit zero", method: http.MethodPost, auth: "admin", query: "?limit=0", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 10000"},
		{name: "limit too big", method: http.MethodPost, auth: "admin", query: "?limit=10001", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 10000"},
		{name: "limit not a number", method: http.MethodPost, auth: "admin", query: "?limit=lots", wantStatus: http.StatusBadRequest, wantBody: "limit must be between 1 and 10000"},
		{name: "GET not allowed", method: http.MethodGet, auth: "admin", wantStatus: http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/binilink/backfill"+tt.query, nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			sweepTestServer().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("got %d %q, want %d containing %q", rec.Code, rec.Body.String(), tt.wantStatus, tt.wantBody)
			}
		})
	}
}
