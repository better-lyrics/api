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
	bearerToken string
	tokenExpiry time.Time
	tokenMu     sync.RWMutex

	// Refresh token when it has less than this time remaining
	refreshThreshold = 5 * time.Minute
)

// JWTClaims represents the relevant claims from the bearer token
type JWTClaims struct {
	Exp int64 `json:"exp"` // Expiration time (Unix timestamp)
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

// isTokenExpiringSoon checks if the token will expire within the refresh threshold.
// Note: This function does not acquire locks - caller must hold at least a read lock.
func isTokenExpiringSoon() bool {
	return tokenExpiry.IsZero() || time.Now().Add(refreshThreshold).After(tokenExpiry)
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
	if bearerToken != "" && !isTokenExpiringSoon() {
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

	req, err := http.NewRequest("GET", baseURL+browsePath, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create browse request: %w", err)
	}
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

	html, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token source response: %w", err)
	}

	// 2. Extract JS bundle path
	jsPathRe := regexp.MustCompile(`/assets/index[~\-][a-zA-Z0-9]+\.js`)
	jsPath := jsPathRe.FindString(string(html))
	if jsPath == "" {
		return "", fmt.Errorf("could not find JS bundle path in HTML")
	}

	log.Debugf("%s Found JS bundle: %s", logcolors.LogBearerToken, jsPath)

	// 3. Fetch JS bundle
	jsReq, err := http.NewRequest("GET", baseURL+jsPath, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create JS bundle request: %w", err)
	}
	jsReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	jsResp, err := client.Do(jsReq)
	if err != nil {
		return "", fmt.Errorf("failed to fetch JS bundle: %w", err)
	}
	defer jsResp.Body.Close()

	jsContent, err := io.ReadAll(jsResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read JS bundle: %w", err)
	}

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

// =============================================================================
// MINTED BEARER LANE (search only, no media-user-token)
// =============================================================================

// mintTokenURL is the minter that returns a short-lived Apple developer bearer,
// from TTML_MINT_URL (empty disables the minted lane). A package var so tests
// can point it at a stub.
var mintTokenURL = config.Get().Configuration.TTMLMintURL

// mintUserAgent is the am-mint UA; that endpoint is not UA-whitelisted, so a plain
// identifier is fine (the unguessable UA is reserved for lrc.red, LRC_RED_USER_AGENT).
const mintUserAgent = "better-lyrics-api (+https://betterlyrics.org)"

const (
	mintMinTTL        = 60 * time.Second
	mintMaxTTL        = 3600 * time.Second
	mintRefreshBuffer = 15 * time.Second // re-mint this long before expiry
)

type mintedBearer struct {
	token        string
	storefront   string
	storefrontID string
}

var (
	minted       mintedBearer
	mintedExpiry time.Time
	mintedMu     sync.RWMutex
)

var appleStorefrontCodes = map[string]string{
	"143441": "us",
	"143442": "fr",
	"143443": "de",
	"143444": "gb",
	"143445": "at",
	"143446": "be",
	"143447": "fi",
	"143448": "gr",
	"143449": "ie",
	"143450": "it",
	"143451": "lu",
	"143452": "nl",
	"143453": "pt",
	"143454": "es",
	"143455": "ca",
	"143456": "se",
	"143457": "no",
	"143458": "dk",
	"143459": "ch",
	"143460": "au",
	"143461": "nz",
	"143462": "jp",
	"143463": "hk",
	"143464": "sg",
	"143465": "cn",
	"143466": "kr",
	"143467": "in",
	"143468": "mx",
	"143469": "ru",
	"143470": "tw",
	"143471": "vn",
	"143472": "za",
	"143473": "my",
	"143474": "ph",
	"143475": "th",
	"143476": "id",
	"143477": "pk",
	"143478": "pl",
	"143479": "sa",
	"143480": "tr",
	"143481": "ae",
	"143482": "hu",
	"143483": "cl",
	"143484": "np",
	"143485": "pa",
	"143486": "lk",
	"143487": "ro",
	"143488": "mv",
	"143489": "cz",
	"143490": "bd",
	"143491": "il",
	"143492": "ua",
	"143493": "kw",
	"143494": "hr",
	"143495": "cr",
	"143496": "sk",
	"143497": "lb",
	"143498": "qa",
	"143499": "si",
	"143500": "rs",
	"143501": "co",
	"143502": "ve",
	"143503": "br",
	"143504": "gt",
	"143505": "ar",
	"143506": "sv",
	"143507": "pe",
	"143508": "do",
	"143509": "ec",
	"143510": "hn",
	"143511": "jm",
	"143512": "ni",
	"143513": "py",
	"143514": "uy",
	"143515": "mo",
	"143516": "eg",
	"143517": "kz",
	"143518": "ee",
	"143519": "lv",
	"143520": "lt",
	"143521": "mt",
	"143522": "li",
	"143523": "md",
	"143524": "am",
	"143525": "bw",
	"143526": "bg",
	"143527": "ci",
	"143528": "jo",
	"143529": "ke",
	"143530": "mk",
	"143531": "mg",
	"143532": "ml",
	"143533": "mu",
	"143534": "ne",
	"143535": "sn",
	"143536": "tn",
	"143537": "ug",
	"143538": "ai",
	"143539": "bs",
	"143540": "ag",
	"143541": "bb",
	"143542": "bm",
	"143543": "vg",
	"143544": "ky",
	"143545": "dm",
	"143546": "gd",
	"143547": "ms",
	"143548": "kn",
	"143549": "lc",
	"143550": "vc",
	"143551": "tt",
	"143552": "tc",
	"143553": "gy",
	"143554": "sr",
	"143555": "bz",
	"143556": "bo",
	"143557": "cy",
	"143558": "is",
	"143559": "bh",
	"143560": "bn",
	"143561": "ng",
	"143562": "om",
	"143563": "dz",
	"143564": "ao",
	"143565": "by",
	"143566": "uz",
	"143568": "az",
	"143572": "tz",
	"143573": "gh",
}

func mintedStorefrontCode(storefrontID string) string {
	numeric := storefrontID
	if i := strings.IndexAny(numeric, "-,"); i >= 0 {
		numeric = numeric[:i]
	}
	return appleStorefrontCodes[numeric]
}

type mintResponse struct {
	StorefrontID    string `json:"storefront_id"`
	Token           string `json:"token"`
	TokenType       string `json:"token_type"`
	CacheTTLSeconds int    `json:"cache_ttl_seconds"`
}

// clampMintTTL bounds the minter's advertised TTL. The minter returns ~120s,
// far below the scraped token's hour-plus lifetime, so the scraped refresh
// threshold (5 min) does not apply to this lane.
func clampMintTTL(seconds int) time.Duration {
	d := time.Duration(seconds) * time.Second
	if d < mintMinTTL {
		return mintMinTTL
	}
	if d > mintMaxTTL {
		return mintMaxTTL
	}
	return d
}

func getMintedBearer() (mintedBearer, error) {
	mintedMu.RLock()
	if minted.token != "" && time.Now().Add(mintRefreshBuffer).Before(mintedExpiry) {
		cached := minted
		mintedMu.RUnlock()
		return cached, nil
	}
	mintedMu.RUnlock()

	if mintTokenURL == "" {
		return mintedBearer{}, fmt.Errorf("mint url not configured")
	}

	mintedMu.Lock()
	defer mintedMu.Unlock()

	if minted.token != "" && time.Now().Add(mintRefreshBuffer).Before(mintedExpiry) {
		return minted, nil
	}

	req, err := http.NewRequest("GET", mintTokenURL, nil)
	if err != nil {
		return mintedBearer{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", mintUserAgent)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return mintedBearer{}, fmt.Errorf("mint request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return mintedBearer{}, fmt.Errorf("mint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return mintedBearer{}, fmt.Errorf("failed to read mint response: %w", err)
	}

	var mr mintResponse
	if err := json.Unmarshal(body, &mr); err != nil {
		return mintedBearer{}, fmt.Errorf("failed to parse mint response: %w", err)
	}
	if mr.Token == "" {
		return mintedBearer{}, fmt.Errorf("mint response missing token")
	}

	storefront := mintedStorefrontCode(mr.StorefrontID)
	if storefront == "" {
		return mintedBearer{}, fmt.Errorf("mint returned unmappable storefront %q", mr.StorefrontID)
	}

	minted = mintedBearer{token: mr.Token, storefront: storefront, storefrontID: mr.StorefrontID}
	mintedExpiry = time.Now().Add(clampMintTTL(mr.CacheTTLSeconds))
	log.Infof("%s Minted search bearer for storefront %s (ttl %v)", logcolors.LogBearerToken, storefront, clampMintTTL(mr.CacheTTLSeconds))
	return minted, nil
}

// StartBearerTokenMonitor fetches the initial bearer token and storefronts synchronously,
// then starts a background goroutine that proactively refreshes the token before it expires.
func StartBearerTokenMonitor() {
	// Initial fetch - synchronous to ensure storefronts are available before server starts
	_, err := GetBearerToken()
	if err != nil {
		log.Errorf("%s Initial token fetch failed: %v", logcolors.LogBearerToken, err)
	} else {
		// Bearer token available - fetch per-account storefronts
		InitializeAccountStorefronts()
	}

	// Background monitor for proactive refresh
	go func() {
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
