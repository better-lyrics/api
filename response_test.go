package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIResponse_SetCacheStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{"HIT status", "HIT", "HIT"},
		{"MISS status", "MISS", "MISS"},
		{"NEGATIVE_HIT status", "NEGATIVE_HIT", "NEGATIVE_HIT"},
		{"STALE status", "STALE", "STALE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/test", nil)

			Respond(w, r).SetCacheStatus(tt.status).JSON(map[string]string{"test": "data"})

			if got := w.Header().Get("X-Cache-Status"); got != tt.expected {
				t.Errorf("X-Cache-Status = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestAPIResponse_AuthModeFromContext(t *testing.T) {
	tests := []struct {
		name         string
		contextSetup func(context.Context) context.Context
		expected     string
	}{
		{
			name: "authenticated user",
			contextSetup: func(ctx context.Context) context.Context {
				return context.WithValue(ctx, apiKeyAuthenticatedKey, true)
			},
			expected: "authenticated",
		},
		{
			name: "invalid API key",
			contextSetup: func(ctx context.Context) context.Context {
				ctx = context.WithValue(ctx, apiKeyInvalidKey, true)
				return context.WithValue(ctx, apiKeyRequiredForFreshKey, true)
			},
			expected: "invalid",
		},
		{
			name: "cache-only mode (no API key)",
			contextSetup: func(ctx context.Context) context.Context {
				return context.WithValue(ctx, apiKeyRequiredForFreshKey, true)
			},
			expected: "cache",
		},
		{
			name: "no auth context - no header",
			contextSetup: func(ctx context.Context) context.Context {
				return ctx
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/test", nil)
			r = r.WithContext(tt.contextSetup(r.Context()))

			Respond(w, r).SetCacheStatus("HIT").JSON(map[string]string{"test": "data"})

			got := w.Header().Get("X-Auth-Mode")
			if got != tt.expected {
				t.Errorf("X-Auth-Mode = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestAPIResponse_RateLimitTypeFromContext(t *testing.T) {
	tests := []struct {
		name     string
		rateType string
		expected string
	}{
		{"normal rate limit", "normal", "normal"},
		{"cached rate limit", "cached", "cached"},
		{"bypass rate limit", "bypass", "bypass"},
		{"no rate limit type", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/test", nil)
			if tt.rateType != "" {
				r = r.WithContext(context.WithValue(r.Context(), rateLimitTypeKey, tt.rateType))
			}

			Respond(w, r).SetCacheStatus("HIT").JSON(map[string]string{"test": "data"})

			got := w.Header().Get("X-RateLimit-Type")
			if got != tt.expected {
				t.Errorf("X-RateLimit-Type = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestAPIResponse_SetProvider(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	Respond(w, r).SetProvider("ttml").SetCacheStatus("HIT").JSON(map[string]string{"test": "data"})

	if got := w.Header().Get("X-Provider"); got != "ttml" {
		t.Errorf("X-Provider = %q, want %q", got, "ttml")
	}
}

func TestAPIResponse_ContentType(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	Respond(w, r).JSON(map[string]string{"test": "data"})

	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
}

func TestAPIResponse_Error(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	Respond(w, r).SetCacheStatus("MISS").Error(http.StatusNotFound, map[string]string{"error": "not found"})

	if w.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusNotFound)
	}

	if got := w.Header().Get("X-Cache-Status"); got != "MISS" {
		t.Errorf("X-Cache-Status = %q, want %q", got, "MISS")
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "not found" {
		t.Errorf("error = %q, want %q", resp["error"], "not found")
	}
}

func TestAPIResponse_JSONBody(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	data := map[string]interface{}{
		"ttml":  "<xml>lyrics</xml>",
		"score": 0.95,
	}
	Respond(w, r).SetCacheStatus("MISS").JSON(data)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["ttml"] != "<xml>lyrics</xml>" {
		t.Errorf("ttml = %v, want %v", resp["ttml"], "<xml>lyrics</xml>")
	}
	if resp["score"] != 0.95 {
		t.Errorf("score = %v, want %v", resp["score"], 0.95)
	}
}

func TestAPIResponse_AuthModePriority(t *testing.T) {
	// Test that authenticated takes priority over invalid and required
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(r.Context(), apiKeyAuthenticatedKey, true)
	ctx = context.WithValue(ctx, apiKeyInvalidKey, true)
	ctx = context.WithValue(ctx, apiKeyRequiredForFreshKey, true)
	r = r.WithContext(ctx)

	Respond(w, r).JSON(map[string]string{})

	if got := w.Header().Get("X-Auth-Mode"); got != "authenticated" {
		t.Errorf("X-Auth-Mode = %q, want %q (authenticated should have priority)", got, "authenticated")
	}
}

func TestAPIResponse_InvalidOverRequired(t *testing.T) {
	// Test that invalid takes priority over required
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(r.Context(), apiKeyInvalidKey, true)
	ctx = context.WithValue(ctx, apiKeyRequiredForFreshKey, true)
	r = r.WithContext(ctx)

	Respond(w, r).JSON(map[string]string{})

	if got := w.Header().Get("X-Auth-Mode"); got != "invalid" {
		t.Errorf("X-Auth-Mode = %q, want %q (invalid should have priority over cache)", got, "invalid")
	}
}

func TestAPIResponse_FluentAPI(t *testing.T) {
	// Test that methods can be chained in any order
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r = r.WithContext(context.WithValue(r.Context(), apiKeyAuthenticatedKey, true))

	// Chain in different order
	Respond(w, r).
		SetProvider("kugou").
		SetCacheStatus("HIT").
		JSON(map[string]string{"lyrics": "test"})

	if got := w.Header().Get("X-Provider"); got != "kugou" {
		t.Errorf("X-Provider = %q, want %q", got, "kugou")
	}
	if got := w.Header().Get("X-Cache-Status"); got != "HIT" {
		t.Errorf("X-Cache-Status = %q, want %q", got, "HIT")
	}
	if got := w.Header().Get("X-Auth-Mode"); got != "authenticated" {
		t.Errorf("X-Auth-Mode = %q, want %q", got, "authenticated")
	}
}

func TestAPIResponse_ErrorWithHeaders(t *testing.T) {
	// Test that Error() also sets all context-based headers
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(r.Context(), apiKeyAuthenticatedKey, true)
	ctx = context.WithValue(ctx, rateLimitTypeKey, "normal")
	r = r.WithContext(ctx)

	Respond(w, r).
		SetCacheStatus("MISS").
		Error(http.StatusInternalServerError, map[string]string{"error": "server error"})

	if got := w.Header().Get("X-Auth-Mode"); got != "authenticated" {
		t.Errorf("X-Auth-Mode = %q, want %q", got, "authenticated")
	}
	if got := w.Header().Get("X-RateLimit-Type"); got != "normal" {
		t.Errorf("X-RateLimit-Type = %q, want %q", got, "normal")
	}
	if got := w.Header().Get("X-Cache-Status"); got != "MISS" {
		t.Errorf("X-Cache-Status = %q, want %q", got, "MISS")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
