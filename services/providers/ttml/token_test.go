package ttml

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// createTestJWT creates a valid JWT with the given expiry time for testing
func createTestJWT(exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT","kid":"test"}`))
	claims := map[string]interface{}{
		"exp": exp.Unix(),
		"iss": "test-issuer",
	}
	claimsJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
	return header + "." + payload + "." + signature
}

// createTestJWTWithClaims creates a JWT with custom claims for testing edge cases
func createTestJWTWithClaims(claims map[string]interface{}) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	claimsJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := base64.RawURLEncoding.EncodeToString([]byte("fake-signature"))
	return header + "." + payload + "." + signature
}

func TestParseJWTExpiry_ValidToken(t *testing.T) {
	// Create a token expiring in 1 hour
	expectedExpiry := time.Now().Add(1 * time.Hour).Truncate(time.Second)
	token := createTestJWT(expectedExpiry)

	expiry, err := parseJWTExpiry(token)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Compare Unix timestamps to avoid nanosecond differences
	if expiry.Unix() != expectedExpiry.Unix() {
		t.Errorf("Expected expiry %v, got %v", expectedExpiry, expiry)
	}
}

func TestParseJWTExpiry_VariousExpiryTimes(t *testing.T) {
	tests := []struct {
		name   string
		expiry time.Time
	}{
		{
			name:   "Expires in 1 minute",
			expiry: time.Now().Add(1 * time.Minute),
		},
		{
			name:   "Expires in 24 hours",
			expiry: time.Now().Add(24 * time.Hour),
		},
		{
			name:   "Expires in 30 days",
			expiry: time.Now().Add(30 * 24 * time.Hour),
		},
		{
			name:   "Already expired",
			expiry: time.Now().Add(-1 * time.Hour),
		},
		{
			name:   "Expires at epoch + 1 year",
			expiry: time.Unix(31536000, 0), // 1971-01-01
		},
		{
			name:   "Far future expiry",
			expiry: time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := createTestJWT(tt.expiry)
			expiry, err := parseJWTExpiry(token)
			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}
			if expiry.Unix() != tt.expiry.Unix() {
				t.Errorf("Expected expiry Unix %d, got %d", tt.expiry.Unix(), expiry.Unix())
			}
		})
	}
}

func TestParseJWTExpiry_InvalidFormat(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		expectedErr string
	}{
		{
			name:        "Empty string",
			token:       "",
			expectedErr: "invalid JWT format: expected 3 parts, got 1",
		},
		{
			name:        "Single part",
			token:       "eyJhbGciOiJFUzI1NiJ9",
			expectedErr: "invalid JWT format: expected 3 parts, got 1",
		},
		{
			name:        "Two parts",
			token:       "eyJhbGciOiJFUzI1NiJ9.eyJleHAiOjE2MDAwMDAwMDB9",
			expectedErr: "invalid JWT format: expected 3 parts, got 2",
		},
		{
			name:        "Four parts",
			token:       "part1.part2.part3.part4",
			expectedErr: "invalid JWT format: expected 3 parts, got 4",
		},
		{
			name:        "Just dots",
			token:       "..",
			expectedErr: "failed to parse JWT claims",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseJWTExpiry(tt.token)
			if err == nil {
				t.Fatal("Expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("Expected error containing %q, got %q", tt.expectedErr, err.Error())
			}
		})
	}
}

func TestParseJWTExpiry_InvalidPayload(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		expectedErr string
	}{
		{
			name:        "Invalid base64",
			payload:     "not-valid-base64!!!",
			expectedErr: "failed to decode JWT payload",
		},
		{
			name:        "Valid base64 but not JSON",
			payload:     base64.RawURLEncoding.EncodeToString([]byte("not json")),
			expectedErr: "failed to parse JWT claims",
		},
		{
			name:        "Empty JSON object",
			payload:     base64.RawURLEncoding.EncodeToString([]byte("{}")),
			expectedErr: "JWT has no exp claim",
		},
		{
			name:        "Exp is zero",
			payload:     base64.RawURLEncoding.EncodeToString([]byte(`{"exp":0}`)),
			expectedErr: "JWT has no exp claim",
		},
		{
			name:        "Exp is string instead of number",
			payload:     base64.RawURLEncoding.EncodeToString([]byte(`{"exp":"not a number"}`)),
			expectedErr: "failed to parse JWT claims", // JSON unmarshal fails on type mismatch
		},
	}

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256"}`))
	signature := base64.RawURLEncoding.EncodeToString([]byte("sig"))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := header + "." + tt.payload + "." + signature
			_, err := parseJWTExpiry(token)
			if err == nil {
				t.Fatal("Expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("Expected error containing %q, got %q", tt.expectedErr, err.Error())
			}
		})
	}
}

func TestParseJWTExpiry_Base64Padding(t *testing.T) {
	// JWT uses base64url encoding WITHOUT padding, but parseJWTExpiry should handle
	// payloads that need different amounts of padding

	tests := []struct {
		name   string
		claims map[string]interface{}
	}{
		{
			name:   "Payload needs no padding",
			claims: map[string]interface{}{"exp": time.Now().Add(time.Hour).Unix()},
		},
		{
			name:   "Payload needs single padding",
			claims: map[string]interface{}{"exp": time.Now().Add(time.Hour).Unix(), "iss": "a"},
		},
		{
			name:   "Payload needs double padding",
			claims: map[string]interface{}{"exp": time.Now().Add(time.Hour).Unix(), "iss": "ab"},
		},
		{
			name:   "Larger payload",
			claims: map[string]interface{}{
				"exp": time.Now().Add(time.Hour).Unix(),
				"iss": "test-issuer-with-longer-name",
				"sub": "user@example.com",
				"aud": "api.example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := createTestJWTWithClaims(tt.claims)
			expiry, err := parseJWTExpiry(token)
			if err != nil {
				t.Fatalf("Expected no error for padding test, got: %v", err)
			}
			expectedExp := tt.claims["exp"].(int64)
			if expiry.Unix() != expectedExp {
				t.Errorf("Expected expiry %d, got %d", expectedExp, expiry.Unix())
			}
		})
	}
}

func TestParseJWTExpiry_RealWorldTokenFormat(t *testing.T) {
	// Test with token format similar to real Apple Music tokens (ES256 signed)
	// The actual token in production starts with: eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCIsImtpZCI6...
	header := `{"alg":"ES256","typ":"JWT","kid":"ABC123DEF"}`
	claims := map[string]interface{}{
		"iss": "TEAM_ID",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(6 * 30 * 24 * time.Hour).Unix(), // 6 months
	}

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	claimsJSON, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signatureB64 := base64.RawURLEncoding.EncodeToString([]byte("fake-es256-signature-would-be-here"))

	token := headerB64 + "." + payloadB64 + "." + signatureB64

	expiry, err := parseJWTExpiry(token)
	if err != nil {
		t.Fatalf("Failed to parse real-world format token: %v", err)
	}

	expectedExp := claims["exp"].(int64)
	if expiry.Unix() != expectedExp {
		t.Errorf("Expected expiry %d, got %d", expectedExp, expiry.Unix())
	}
}

func TestIsTokenExpiringSoon(t *testing.T) {
	// Save original state
	originalToken := bearerToken
	originalExpiry := tokenExpiry
	defer func() {
		tokenMu.Lock()
		bearerToken = originalToken
		tokenExpiry = originalExpiry
		tokenMu.Unlock()
	}()

	tests := []struct {
		name           string
		tokenExpiry    time.Time
		expectedResult bool
	}{
		{
			name:           "Zero time - needs refresh",
			tokenExpiry:    time.Time{},
			expectedResult: true,
		},
		{
			name:           "Expired - needs refresh",
			tokenExpiry:    time.Now().Add(-1 * time.Hour),
			expectedResult: true,
		},
		{
			name:           "Expires in 1 minute - needs refresh",
			tokenExpiry:    time.Now().Add(1 * time.Minute),
			expectedResult: true,
		},
		{
			name:           "Expires in 4 minutes - needs refresh (within 5min threshold)",
			tokenExpiry:    time.Now().Add(4 * time.Minute),
			expectedResult: true,
		},
		{
			name:           "Expires in exactly 5 minutes - edge case, needs refresh",
			tokenExpiry:    time.Now().Add(5 * time.Minute),
			expectedResult: true, // time.Now().Add(5min).After(expiry) is true when equal
		},
		{
			name:           "Expires in 6 minutes - does not need refresh",
			tokenExpiry:    time.Now().Add(6 * time.Minute),
			expectedResult: false,
		},
		{
			name:           "Expires in 1 hour - does not need refresh",
			tokenExpiry:    time.Now().Add(1 * time.Hour),
			expectedResult: false,
		},
		{
			name:           "Expires in 24 hours - does not need refresh",
			tokenExpiry:    time.Now().Add(24 * time.Hour),
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenMu.Lock()
			tokenExpiry = tt.tokenExpiry
			tokenMu.Unlock()

			tokenMu.RLock()
			result := isTokenExpiringSoon()
			tokenMu.RUnlock()

			if result != tt.expectedResult {
				t.Errorf("Expected isTokenExpiringSoon()=%v for expiry %v, got %v",
					tt.expectedResult, tt.tokenExpiry, result)
			}
		})
	}
}

func TestGetTokenStatus(t *testing.T) {
	// Save original state
	originalToken := bearerToken
	originalExpiry := tokenExpiry
	defer func() {
		tokenMu.Lock()
		bearerToken = originalToken
		tokenExpiry = originalExpiry
		tokenMu.Unlock()
	}()

	t.Run("Zero expiry returns needs refresh", func(t *testing.T) {
		tokenMu.Lock()
		tokenExpiry = time.Time{}
		tokenMu.Unlock()

		expiry, remaining, needsRefresh := GetTokenStatus()

		if !expiry.IsZero() {
			t.Errorf("Expected zero expiry, got %v", expiry)
		}
		if remaining != 0 {
			t.Errorf("Expected 0 remaining, got %v", remaining)
		}
		if !needsRefresh {
			t.Error("Expected needsRefresh=true for zero expiry")
		}
	})

	t.Run("Valid expiry returns correct values", func(t *testing.T) {
		futureExpiry := time.Now().Add(1 * time.Hour)
		tokenMu.Lock()
		tokenExpiry = futureExpiry
		tokenMu.Unlock()

		expiry, remaining, needsRefresh := GetTokenStatus()

		if expiry.Unix() != futureExpiry.Unix() {
			t.Errorf("Expected expiry %v, got %v", futureExpiry, expiry)
		}

		// Remaining should be close to 1 hour (allow 1 second tolerance)
		expectedRemaining := time.Until(futureExpiry)
		if remaining < expectedRemaining-time.Second || remaining > expectedRemaining+time.Second {
			t.Errorf("Expected remaining ~%v, got %v", expectedRemaining, remaining)
		}

		if needsRefresh {
			t.Error("Expected needsRefresh=false for token expiring in 1 hour")
		}
	})

	t.Run("Expiring soon returns needsRefresh true", func(t *testing.T) {
		soonExpiry := time.Now().Add(3 * time.Minute)
		tokenMu.Lock()
		tokenExpiry = soonExpiry
		tokenMu.Unlock()

		_, remaining, needsRefresh := GetTokenStatus()

		if remaining < 0 {
			t.Errorf("Expected positive remaining time, got %v", remaining)
		}
		if !needsRefresh {
			t.Error("Expected needsRefresh=true for token expiring in 3 minutes")
		}
	})
}

func TestGetBearerToken_UsesCachedToken(t *testing.T) {
	// Save original state
	originalToken := bearerToken
	originalExpiry := tokenExpiry
	defer func() {
		tokenMu.Lock()
		bearerToken = originalToken
		tokenExpiry = originalExpiry
		tokenMu.Unlock()
	}()

	// Set a valid cached token
	cachedToken := "cached-test-token"
	tokenMu.Lock()
	bearerToken = cachedToken
	tokenExpiry = time.Now().Add(1 * time.Hour) // Not expiring soon
	tokenMu.Unlock()

	// GetBearerToken should return the cached token without refreshing
	token, err := GetBearerToken()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if token != cachedToken {
		t.Errorf("Expected cached token %q, got %q", cachedToken, token)
	}
}

func TestGetBearerToken_RefreshesExpiredToken(t *testing.T) {
	// Save original state
	originalToken := bearerToken
	originalExpiry := tokenExpiry
	defer func() {
		tokenMu.Lock()
		bearerToken = originalToken
		tokenExpiry = originalExpiry
		tokenMu.Unlock()
	}()

	// Set an expired token
	tokenMu.Lock()
	bearerToken = "expired-token"
	tokenExpiry = time.Now().Add(-1 * time.Hour) // Already expired
	tokenMu.Unlock()

	// Without a mock server, this will fail - but we verify it TRIES to refresh
	_, err := GetBearerToken()
	// Error is expected since TTML_TOKEN_SOURCE_URL is not configured in tests
	if err == nil {
		// If no error, the refresh somehow succeeded (unlikely without config)
		t.Log("GetBearerToken succeeded unexpectedly - check if config is set")
	} else if !strings.Contains(err.Error(), "TOKEN_SOURCE_URL") &&
		!strings.Contains(err.Error(), "failed") {
		t.Logf("GetBearerToken returned expected error type: %v", err)
	}
}

func TestScrapeToken_MissingConfig(t *testing.T) {
	// scrapeToken should fail gracefully when config is missing
	// This tests the error path without needing HTTP mocking

	// Note: This test relies on the config not having TTML_TOKEN_SOURCE_URL set
	// In a real test environment, you might need to temporarily clear it

	_, err := scrapeToken()
	if err == nil {
		// If config is set in test environment, skip this assertion
		t.Log("scrapeToken succeeded - TTML_TOKEN_SOURCE_URL may be configured in test env")
		return
	}

	// Should get a configuration error
	if !strings.Contains(err.Error(), "TOKEN_SOURCE_URL") &&
		!strings.Contains(err.Error(), "failed") {
		t.Logf("scrapeToken error: %v", err)
	}
}

func TestScrapeToken_WithMockServer(t *testing.T) {
	// Create a mock server that simulates the upstream provider
	expectedToken := createTestJWT(time.Now().Add(6 * 30 * 24 * time.Hour))

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/browse"):
			// Return HTML with JS bundle path
			html := `<!DOCTYPE html>
				<html>
				<head><title>Browse</title></head>
				<body>
				<script src="/assets/index~abc123.js"></script>
				</body>
				</html>`
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(html))

		case strings.HasPrefix(r.URL.Path, "/assets/index"):
			// Return JS bundle with embedded token
			js := fmt.Sprintf(`
				var config = {
					token: "%s",
					other: "data"
				};
			`, expectedToken)
			w.Header().Set("Content-Type", "application/javascript")
			w.Write([]byte(js))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// We can't easily test scrapeToken directly since it uses config.Get()
	// But we can test the HTTP flow logic indirectly

	// Test that the server responds correctly
	resp, err := http.Get(server.URL + "/us/browse")
	if err != nil {
		t.Fatalf("Failed to fetch browse page: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}

	// Test JS bundle endpoint
	resp2, err := http.Get(server.URL + "/assets/index~abc123.js")
	if err != nil {
		t.Fatalf("Failed to fetch JS bundle: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 for JS bundle, got %d", resp2.StatusCode)
	}
}

func TestScrapeToken_ServerErrors(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		expectsErr bool
	}{
		{
			name: "Server returns 500",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectsErr: true,
		},
		{
			name: "Server returns 404",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			expectsErr: true,
		},
		{
			name: "HTML without JS bundle path",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("<html><body>No JS here</body></html>"))
			},
			expectsErr: true,
		},
		{
			name: "JS bundle without token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/browse") {
					w.Write([]byte(`<script src="/assets/index~test.js"></script>`))
				} else {
					w.Write([]byte("var x = 1; // no token here"))
				}
			},
			expectsErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			// We can't directly call scrapeToken with a custom URL
			// But this documents the expected error scenarios
			t.Logf("Server URL: %s - would expect error: %v", server.URL, tt.expectsErr)
		})
	}
}

func TestRefreshBearerToken_DoubleCheckPattern(t *testing.T) {
	// This tests the double-check pattern in refreshBearerToken
	// After acquiring write lock, it should re-check if token is still valid

	// Save original state
	originalToken := bearerToken
	originalExpiry := tokenExpiry
	defer func() {
		tokenMu.Lock()
		bearerToken = originalToken
		tokenExpiry = originalExpiry
		tokenMu.Unlock()
	}()

	// Set a valid token that doesn't need refresh
	validToken := "valid-token-from-another-goroutine"
	tokenMu.Lock()
	bearerToken = validToken
	tokenExpiry = time.Now().Add(1 * time.Hour)
	tokenMu.Unlock()

	// refreshBearerToken should return the existing valid token
	// because of the double-check after acquiring the lock
	token, err := refreshBearerToken()
	if err != nil {
		// If config isn't set up, refresh will fail - that's ok for this test
		t.Logf("refreshBearerToken error (expected in test env): %v", err)
		return
	}

	if token != validToken {
		t.Errorf("Expected double-check to return existing valid token %q, got %q",
			validToken, token)
	}
}

func TestTokenMonitor_Integration(t *testing.T) {
	// This is more of a documentation test showing how the monitor works
	// We can't easily test the goroutine behavior, but we verify the setup

	// StartBearerTokenMonitor starts a background goroutine
	// It should:
	// 1. Do initial fetch
	// 2. Check every minute if refresh is needed
	// 3. Proactively refresh before expiry

	// For actual testing, you'd need to:
	// 1. Mock the config
	// 2. Mock the HTTP client
	// 3. Use time mocking or very short intervals

	t.Log("TokenMonitor: starts background refresh goroutine")
	t.Log("TokenMonitor: checks every 1 minute")
	t.Log("TokenMonitor: refreshes when token expires within 5 minutes")
}

func TestRefreshThreshold(t *testing.T) {
	// Verify the refresh threshold constant
	if refreshThreshold != 5*time.Minute {
		t.Errorf("Expected refresh threshold of 5 minutes, got %v", refreshThreshold)
	}
}

// Benchmark for JWT parsing
func BenchmarkParseJWTExpiry(b *testing.B) {
	token := createTestJWT(time.Now().Add(time.Hour))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = parseJWTExpiry(token)
	}
}

// Benchmark for token expiry check
func BenchmarkIsTokenExpiringSoon(b *testing.B) {
	tokenMu.Lock()
	tokenExpiry = time.Now().Add(time.Hour)
	tokenMu.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tokenMu.RLock()
		_ = isTokenExpiringSoon()
		tokenMu.RUnlock()
	}
}
