package notifier

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// Helper function to create a test JWT token
func createTestJWT(claims JWTClaims) string {
	// Create header
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	// Create payload
	payloadJSON, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Create a dummy signature
	signature := base64.RawURLEncoding.EncodeToString([]byte("dummy_signature"))

	return headerB64 + "." + payloadB64 + "." + signature
}

func TestDecodeJWT(t *testing.T) {
	now := time.Now()
	futureTime := now.Add(7 * 24 * time.Hour)

	claims := JWTClaims{
		Issuer:         "test-issuer",
		IssuedAt:       now.Unix(),
		ExpiresAt:      futureTime.Unix(),
		RootHTTPOrigin: []string{"https://example.com"},
	}

	token := createTestJWT(claims)

	decoded, err := DecodeJWT(token)
	if err != nil {
		t.Fatalf("Failed to decode JWT: %v", err)
	}

	if decoded.Issuer != claims.Issuer {
		t.Errorf("Expected issuer %q, got %q", claims.Issuer, decoded.Issuer)
	}
	if decoded.IssuedAt != claims.IssuedAt {
		t.Errorf("Expected issued at %d, got %d", claims.IssuedAt, decoded.IssuedAt)
	}
	if decoded.ExpiresAt != claims.ExpiresAt {
		t.Errorf("Expected expires at %d, got %d", claims.ExpiresAt, decoded.ExpiresAt)
	}
	if len(decoded.RootHTTPOrigin) != len(claims.RootHTTPOrigin) {
		t.Errorf("Expected %d origins, got %d", len(claims.RootHTTPOrigin), len(decoded.RootHTTPOrigin))
	}
}

func TestDecodeJWT_InvalidFormat(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "Empty token",
			token: "",
		},
		{
			name:  "Single part",
			token: "invalid",
		},
		{
			name:  "Two parts",
			token: "part1.part2",
		},
		{
			name:  "Four parts",
			token: "part1.part2.part3.part4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeJWT(tt.token)
			if err == nil {
				t.Error("Expected error for invalid JWT format, got nil")
			}
		})
	}
}

func TestDecodeJWT_InvalidBase64(t *testing.T) {
	// Create a JWT with invalid base64 in the payload
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.invalid_base64!!!.signature"

	_, err := DecodeJWT(token)
	if err == nil {
		t.Error("Expected error for invalid base64, got nil")
	}
}

func TestDecodeJWT_InvalidJSON(t *testing.T) {
	// Create a JWT with invalid JSON in the payload
	invalidJSON := base64.RawURLEncoding.EncodeToString([]byte("{invalid json"))
	token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." + invalidJSON + ".signature"

	_, err := DecodeJWT(token)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

func TestGetExpirationDate(t *testing.T) {
	futureTime := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)

	claims := JWTClaims{
		Issuer:    "test",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: futureTime.Unix(),
	}

	token := createTestJWT(claims)

	expiration, err := GetExpirationDate(token)
	if err != nil {
		t.Fatalf("Failed to get expiration date: %v", err)
	}

	if expiration.Unix() != futureTime.Unix() {
		t.Errorf("Expected expiration %v, got %v", futureTime, expiration)
	}
}

func TestGetExpirationDate_InvalidToken(t *testing.T) {
	_, err := GetExpirationDate("invalid.token")
	if err == nil {
		t.Error("Expected error for invalid token, got nil")
	}
}

func TestDaysUntilExpiration(t *testing.T) {
	tests := []struct {
		name         string
		daysInFuture int
		expectedDays int
	}{
		{
			name:         "Expires in 7 days",
			daysInFuture: 7,
			expectedDays: 7,
		},
		{
			name:         "Expires in 30 days",
			daysInFuture: 30,
			expectedDays: 30,
		},
		{
			name:         "Expires in 1 day",
			daysInFuture: 1,
			expectedDays: 1,
		},
		{
			name:         "Expires today (within 24 hours)",
			daysInFuture: 0,
			expectedDays: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			futureTime := time.Now().Add(time.Duration(tt.daysInFuture) * 24 * time.Hour)

			claims := JWTClaims{
				Issuer:    "test",
				IssuedAt:  time.Now().Unix(),
				ExpiresAt: futureTime.Unix(),
			}

			token := createTestJWT(claims)

			days, err := DaysUntilExpiration(token)
			if err != nil {
				t.Fatalf("Failed to get days until expiration: %v", err)
			}

			// Allow for a small margin of error (±1 day) due to timing
			if days < tt.expectedDays-1 || days > tt.expectedDays+1 {
				t.Errorf("Expected approximately %d days, got %d", tt.expectedDays, days)
			}
		})
	}
}

func TestDaysUntilExpiration_ExpiredToken(t *testing.T) {
	// Create a token that expired 5 days ago
	pastTime := time.Now().Add(-5 * 24 * time.Hour)

	claims := JWTClaims{
		Issuer:    "test",
		IssuedAt:  time.Now().Add(-10 * 24 * time.Hour).Unix(),
		ExpiresAt: pastTime.Unix(),
	}

	token := createTestJWT(claims)

	days, err := DaysUntilExpiration(token)
	if err != nil {
		t.Fatalf("Failed to get days until expiration: %v", err)
	}

	// Should return a negative number for expired tokens
	if days >= 0 {
		t.Errorf("Expected negative days for expired token, got %d", days)
	}
}

func TestIsExpiringSoon(t *testing.T) {
	tests := []struct {
		name          string
		daysInFuture  int
		threshold     int
		expectingSoon bool
	}{
		{
			name:          "Expires in 3 days, threshold 7 days",
			daysInFuture:  3,
			threshold:     7,
			expectingSoon: true,
		},
		{
			name:          "Expires in 10 days, threshold 7 days",
			daysInFuture:  10,
			threshold:     7,
			expectingSoon: false,
		},
		{
			name:          "Expires in 7 days, threshold 7 days",
			daysInFuture:  7,
			threshold:     7,
			expectingSoon: true,
		},
		{
			name:          "Expires in 1 day, threshold 30 days",
			daysInFuture:  1,
			threshold:     30,
			expectingSoon: true,
		},
		{
			name:          "Expires today, threshold 1 day",
			daysInFuture:  0,
			threshold:     1,
			expectingSoon: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			futureTime := time.Now().Add(time.Duration(tt.daysInFuture) * 24 * time.Hour)

			claims := JWTClaims{
				Issuer:    "test",
				IssuedAt:  time.Now().Unix(),
				ExpiresAt: futureTime.Unix(),
			}

			token := createTestJWT(claims)

			expiringSoon, days, err := IsExpiringSoon(token, tt.threshold)
			if err != nil {
				t.Fatalf("Failed to check if expiring soon: %v", err)
			}

			if expiringSoon != tt.expectingSoon {
				t.Errorf("Expected expiring soon to be %v, got %v (days: %d)", tt.expectingSoon, expiringSoon, days)
			}

			// Verify the days returned is approximately correct
			if days < tt.daysInFuture-1 || days > tt.daysInFuture+1 {
				t.Errorf("Expected approximately %d days, got %d", tt.daysInFuture, days)
			}
		})
	}
}

func TestIsExpiringSoon_ExpiredToken(t *testing.T) {
	// Create an expired token
	pastTime := time.Now().Add(-5 * 24 * time.Hour)

	claims := JWTClaims{
		Issuer:    "test",
		IssuedAt:  time.Now().Add(-10 * 24 * time.Hour).Unix(),
		ExpiresAt: pastTime.Unix(),
	}

	token := createTestJWT(claims)

	expiringSoon, days, err := IsExpiringSoon(token, 7)
	if err != nil {
		t.Fatalf("Failed to check if expiring soon: %v", err)
	}

	// Expired tokens should not be "expiring soon" (they're already expired)
	if expiringSoon {
		t.Errorf("Expected expired token to not be 'expiring soon', but it was (days: %d)", days)
	}

	if days >= 0 {
		t.Errorf("Expected negative days for expired token, got %d", days)
	}
}

func TestIsExpiringSoon_InvalidToken(t *testing.T) {
	_, _, err := IsExpiringSoon("invalid.token.format", 7)
	if err == nil {
		t.Error("Expected error for invalid token, got nil")
	}
}

func TestJWTClaims_MultipleOrigins(t *testing.T) {
	claims := JWTClaims{
		Issuer:    "test",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour).Unix(),
		RootHTTPOrigin: []string{
			"https://example.com",
			"https://api.example.com",
			"https://cdn.example.com",
		},
	}

	token := createTestJWT(claims)

	decoded, err := DecodeJWT(token)
	if err != nil {
		t.Fatalf("Failed to decode JWT: %v", err)
	}

	if len(decoded.RootHTTPOrigin) != 3 {
		t.Errorf("Expected 3 origins, got %d", len(decoded.RootHTTPOrigin))
	}

	expectedOrigins := map[string]bool{
		"https://example.com":     true,
		"https://api.example.com": true,
		"https://cdn.example.com": true,
	}

	for _, origin := range decoded.RootHTTPOrigin {
		if !expectedOrigins[origin] {
			t.Errorf("Unexpected origin: %s", origin)
		}
	}
}
