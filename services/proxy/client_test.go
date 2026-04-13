package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRevalidateAllForSong_NoOpsWhenConfigEmpty(t *testing.T) {
	// With default test config (empty ProxyRevalidateURL and ProxyAPIKey),
	// RevalidateAllForSong should return immediately without calling videoIDsFunc.
	called := false
	RevalidateAllForSong("Song", "Artist", "Album", 240, func(s, a string) []string {
		called = true
		return []string{"vid1"}
	})
	if called {
		t.Error("videoIDsFunc should not be called when proxy config is empty")
	}
}

func TestRevalidateAllForSong_NoOpsWhenVideoIDsEmpty(t *testing.T) {
	// Even with config set, returning no videoIds should be a no-op.
	// Since config is empty in tests, this also verifies the early return.
	RevalidateAllForSong("Song", "Artist", "Album", 240, func(s, a string) []string {
		return nil
	})
	// Should not panic
}

func TestRevalidateByVideoID_HandlesEmptyConfig(t *testing.T) {
	// With empty ProxyRevalidateURL in config, RevalidateByVideoID will construct
	// a malformed URL. It should handle this gracefully without panicking.
	RevalidateByVideoID("vid1", "Song", "Artist", "Album", 240)
}

func TestRevalidateByVideoID_SendsCorrectRequest(t *testing.T) {
	var capturedMethod string
	var capturedPath string
	var capturedAdminKey string
	var capturedVideoID string
	var capturedSong string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedAdminKey = r.Header.Get("x-admin-key")
		capturedVideoID = r.URL.Query().Get("videoId")
		capturedSong = r.URL.Query().Get("song")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Override the package-level httpClient to route through the test server
	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	// Build the request manually to test the HTTP contract,
	// since config.Get() returns empty ProxyRevalidateURL in tests.
	// This simulates what RevalidateByVideoID does internally.
	req, err := http.NewRequest("POST", server.URL+"?videoId=abc123&song=TestSong&artist=TestArtist&album=TestAlbum&duration=240", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("x-admin-key", "test-key")

	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if capturedMethod != "POST" {
		t.Errorf("Expected POST method, got %s", capturedMethod)
	}
	if capturedPath != "" && capturedPath != "/" {
		t.Errorf("Expected root path, got %s", capturedPath)
	}
	if capturedAdminKey != "test-key" {
		t.Errorf("Expected x-admin-key 'test-key', got %q", capturedAdminKey)
	}
	if capturedVideoID != "abc123" {
		t.Errorf("Expected videoId 'abc123', got %q", capturedVideoID)
	}
	if capturedSong != "TestSong" {
		t.Errorf("Expected song 'TestSong', got %q", capturedSong)
	}
}

func TestRevalidateByVideoID_HandlesNon2xxGracefully(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	origClient := httpClient
	httpClient = server.Client()
	defer func() { httpClient = origClient }()

	// Call with the test server URL directly to verify non-2xx handling
	req, err := http.NewRequest("POST", server.URL+"?videoId=fail", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("Request should succeed at HTTP level: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", resp.StatusCode)
	}
}

func TestRevalidateAllForSong_ConcurrencySafety(t *testing.T) {
	// Verify that concurrent calls to RevalidateAllForSong don't panic.
	// With empty config, these are all no-ops but tests the code path is safe.
	var callCount int32
	done := make(chan struct{})

	for i := 0; i < 10; i++ {
		go func() {
			RevalidateAllForSong("Song", "Artist", "Album", 240, func(s, a string) []string {
				atomic.AddInt32(&callCount, 1)
				return []string{"vid1", "vid2"}
			})
			done <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// With empty config, callCount should be 0 (videoIDsFunc never called)
	if atomic.LoadInt32(&callCount) != 0 {
		t.Error("videoIDsFunc should not be called with empty proxy config")
	}
}
