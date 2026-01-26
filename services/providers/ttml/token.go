package ttml

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
)

var (
	bearerToken  string
	tokenExpiry  time.Time
	tokenMu      sync.RWMutex

	// Refresh token when it has less than this time remaining
	refreshThreshold = 5 * time.Minute
)

// JWTClaims represents the relevant claims from the bearer token
type JWTClaims struct {
	Exp int64 `json:"exp"` // Expiration time (Unix timestamp)
	Iat int64 `json:"iat"` // Issued at time
}

// GetBearerToken returns the current bearer token, scraping a fresh one if expired or near expiry
func GetBearerToken() (string, error) {
	tokenMu.RLock()
	if bearerToken != "" && !isTokenExpiringSoon() {
		defer tokenMu.RUnlock()
		return bearerToken, nil
	}
	tokenMu.RUnlock()

	return refreshBearerToken()
}

// isTokenExpiringSoon checks if the token will expire within the refresh threshold
// Must be called with at least a read lock held
func isTokenExpiringSoon() bool {
	if tokenExpiry.IsZero() {
		return true
	}
	return time.Now().Add(refreshThreshold).After(tokenExpiry)
}

// GetTokenStatus returns the current token's expiry status for monitoring
func GetTokenStatus() (expiry time.Time, remaining time.Duration, needsRefresh bool) {
	tokenMu.RLock()
	defer tokenMu.RUnlock()

	if tokenExpiry.IsZero() {
		return time.Time{}, 0, true
	}

	remaining = time.Until(tokenExpiry)
	needsRefresh = isTokenExpiringSoon()
	return tokenExpiry, remaining, needsRefresh
}

func refreshBearerToken() (string, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()

	// Double-check after acquiring write lock
	if bearerToken != "" && !isTokenExpiringSoonUnsafe() {
		return bearerToken, nil
	}

	log.Infof("%s Refreshing bearer token...", logcolors.LogBearerToken)

	token, err := scrapeToken()
	if err != nil {
		return "", err
	}

	// Parse JWT to get actual expiry time
	expiry, err := parseJWTExpiry(token)
	if err != nil {
		// If we can't parse expiry, use a conservative default (1 hour)
		log.Warnf("%s Could not parse JWT expiry, using 1h default: %v", logcolors.LogBearerToken, err)
		expiry = time.Now().Add(1 * time.Hour)
	}

	bearerToken = token
	tokenExpiry = expiry

	remaining := time.Until(expiry)
	log.Infof("%s Bearer token refreshed, expires in %v (at %s)",
		logcolors.LogBearerToken, remaining.Round(time.Minute), expiry.Format(time.RFC3339))

	return token, nil
}

// isTokenExpiringSoonUnsafe is like isTokenExpiringSoon but doesn't check locks
// Only call when write lock is already held
func isTokenExpiringSoonUnsafe() bool {
	if tokenExpiry.IsZero() {
		return true
	}
	return time.Now().Add(refreshThreshold).After(tokenExpiry)
}

// parseJWTExpiry extracts the expiration time from a JWT token
func parseJWTExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	// Decode payload (second part)
	payload := parts[1]

	// Add padding if needed (JWT uses unpadded base64url)
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try standard encoding as fallback
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return time.Time{}, fmt.Errorf("failed to decode JWT payload: %w", err)
		}
	}

	var claims JWTClaims
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return time.Time{}, fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("JWT has no exp claim")
	}

	return time.Unix(claims.Exp, 0), nil
}

func scrapeToken() (string, error) {
	conf := config.Get()
	baseURL := conf.Configuration.TTMLTokenSourceURL
	if baseURL == "" {
		return "", fmt.Errorf("TTML_TOKEN_SOURCE_URL not configured")
	}

	storefront := conf.Configuration.TTMLStorefront
	if storefront == "" {
		storefront = "us"
	}
	browsePath := "/" + storefront + "/browse"

	// 1. Fetch upstream provider's browse page
	client := &http.Client{Timeout: 15 * time.Second}

	req, _ := http.NewRequest("GET", baseURL+browsePath, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch token source: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token source returned status %d", resp.StatusCode)
	}

	html, _ := io.ReadAll(resp.Body)

	// 2. Extract JS bundle path
	jsPathRe := regexp.MustCompile(`/assets/index[~\-][a-zA-Z0-9]+\.js`)
	jsPath := jsPathRe.FindString(string(html))
	if jsPath == "" {
		return "", fmt.Errorf("could not find JS bundle path in HTML")
	}

	log.Debugf("%s Found JS bundle: %s", logcolors.LogBearerToken, jsPath)

	// 3. Fetch JS bundle
	jsReq, _ := http.NewRequest("GET", baseURL+jsPath, nil)
	jsReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	jsResp, err := client.Do(jsReq)
	if err != nil {
		return "", fmt.Errorf("failed to fetch JS bundle: %w", err)
	}
	defer jsResp.Body.Close()

	jsContent, _ := io.ReadAll(jsResp.Body)

	// 4. Extract JWT token - look for ES256 signed developer token
	tokenRe := regexp.MustCompile(`"(eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCIsImtpZCI6[^"]+)"`)
	match := tokenRe.FindStringSubmatch(string(jsContent))
	if len(match) > 1 {
		log.Debugf("%s Extracted ES256 JWT from JS bundle", logcolors.LogBearerToken)
		return match[1], nil
	}

	// Fallback: any JWT with three parts
	jwtRe := regexp.MustCompile(`"(eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,})"`)
	match = jwtRe.FindStringSubmatch(string(jsContent))
	if len(match) > 1 {
		log.Debugf("%s Extracted fallback JWT from JS bundle", logcolors.LogBearerToken)
		return match[1], nil
	}

	return "", fmt.Errorf("could not extract JWT from JS bundle")
}

// StartBearerTokenMonitor starts a background goroutine that proactively refreshes
// the bearer token before it expires
func StartBearerTokenMonitor() {
	go func() {
		// Initial fetch
		_, err := GetBearerToken()
		if err != nil {
			log.Errorf("%s Initial token fetch failed: %v", logcolors.LogBearerToken, err)
		}

		// Check every minute if refresh is needed
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			tokenMu.RLock()
			needsRefresh := isTokenExpiringSoon()
			tokenMu.RUnlock()

			if needsRefresh {
				_, err := GetBearerToken()
				if err != nil {
					log.Errorf("%s Proactive token refresh failed: %v", logcolors.LogBearerToken, err)
				}
			}
		}
	}()
}
