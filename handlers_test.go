package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetCacheDump_Returns410(t *testing.T) {
	t.Run("no auth header", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/cache", nil)

		getCacheDump(w, r)

		if w.Code != http.StatusGone {
			t.Errorf("status = %d, want %d", w.Code, http.StatusGone)
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})

	t.Run("with auth header still returns 410", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/cache", nil)
		r.Header.Set("Authorization", "any-token")

		getCacheDump(w, r)

		if w.Code != http.StatusGone {
			t.Errorf("status = %d, want %d", w.Code, http.StatusGone)
		}
	})

	t.Run("response body contains expected fields", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/cache", nil)

		getCacheDump(w, r)

		var body map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if _, ok := body["error"]; !ok {
			t.Error("response missing 'error' field")
		}
		if _, ok := body["message"]; !ok {
			t.Error("response missing 'message' field")
		}

		alternatives, ok := body["alternatives"].(map[string]interface{})
		if !ok {
			t.Fatal("response missing 'alternatives' map")
		}

		// Must reference the primary replacements
		expected := []string{"/stats", "/cache/keys", "/cache/debug?key=...", "/cache/dump"}
		for _, key := range expected {
			if _, ok := alternatives[key]; !ok {
				t.Errorf("alternatives missing %q", key)
			}
		}
	})
}
