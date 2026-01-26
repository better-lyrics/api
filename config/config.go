package config

import (
	"fmt"
	"lyrics-api-go/logcolors"
	"strings"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
)

var conf = mustLoad()

type Config struct {
	Configuration struct {
		// Provider Settings
		DefaultProvider string `envconfig:"DEFAULT_PROVIDER" default:"ttml"` // Default lyrics provider (ttml, kugou, legacy)

		// Rate Limiting
		RateLimitPerSecond                 int    `envconfig:"RATE_LIMIT_PER_SECOND" default:"2"`
		RateLimitBurstLimit                int    `envconfig:"RATE_LIMIT_BURST_LIMIT" default:"5"`
		CachedRateLimitPerSecond           int    `envconfig:"CACHED_RATE_LIMIT_PER_SECOND" default:"10"`
		CachedRateLimitBurstLimit          int    `envconfig:"CACHED_RATE_LIMIT_BURST_LIMIT" default:"20"`
		CacheInvalidationIntervalInSeconds int    `envconfig:"CACHE_INVALIDATION_INTERVAL_IN_SECONDS" default:"3600"`
		LyricsCacheTTLInSeconds            int    `envconfig:"LYRICS_CACHE_TTL_IN_SECONDS" default:"86400"`
		CacheAccessToken                   string `envconfig:"CACHE_ACCESS_TOKEN" default:""`
		APIKey                             string `envconfig:"API_KEY" default:""`
		APIKeyRequired                     bool   `envconfig:"API_KEY_REQUIRED" default:"false"`

		// TTML API Configuration
		// Token source for auto-scraping bearer tokens (web frontend URL)
		TTMLTokenSourceURL string `envconfig:"TTML_TOKEN_SOURCE_URL" default:""`
		// Single account (backwards compatible) - only MUT needed, bearer is auto-scraped
		TTMLMediaUserToken string `envconfig:"TTML_MEDIA_USER_TOKEN" default:""`
		// Multi-account support (comma-separated media user tokens)
		TTMLMediaUserTokens        string  `envconfig:"TTML_MEDIA_USER_TOKENS" default:""`
		TTMLStorefront             string  `envconfig:"TTML_STOREFRONT" default:"us"`
		TTMLBaseURL                string  `envconfig:"TTML_BASE_URL" default:""`
		TTMLSearchPath             string  `envconfig:"TTML_SEARCH_PATH" default:""`
		TTMLLyricsPath             string  `envconfig:"TTML_LYRICS_PATH" default:""`
		MinSimilarityScore         float64 `envconfig:"MIN_SIMILARITY_SCORE" default:"0.6"`
		DurationMatchDeltaMs       int     `envconfig:"DURATION_MATCH_DELTA_MS" default:"2000"`      // Strict duration filter: reject tracks outside this delta (in ms)
		NegativeCacheTTLInDays     int     `envconfig:"NEGATIVE_CACHE_TTL_DAYS" default:"7"`         // TTL for caching "no lyrics found" responses
		CircuitBreakerThreshold    int     `envconfig:"CIRCUIT_BREAKER_THRESHOLD" default:"5"`       // Consecutive failures before circuit opens
		CircuitBreakerCooldownSecs int     `envconfig:"CIRCUIT_BREAKER_COOLDOWN_SECS" default:"300"` // Seconds to wait before retrying (default: 5 minutes)

		// Legacy Provider Configuration (Spotify-based)
		LyricsUrl              string `envconfig:"LYRICS_URL" default:""`
		TrackUrl               string `envconfig:"TRACK_URL" default:""`
		TokenUrl               string `envconfig:"TOKEN_URL" default:""`
		TokenKey               string `envconfig:"TOKEN_KEY" default:"sp_dc_token"`
		AppPlatform            string `envconfig:"APP_PLATFORM" default:"WebPlayer"`
		UserAgent              string `envconfig:"USER_AGENT" default:"Mozilla/5.0"`
		CookieStringFormat     string `envconfig:"COOKIE_STRING_FORMAT" default:"sp_dc=%s"`
		CookieValue            string `envconfig:"COOKIE_VALUE" default:""`
		ClientID               string `envconfig:"CLIENT_ID" default:""`
		ClientSecret           string `envconfig:"CLIENT_SECRET" default:""`
		OauthTokenUrl          string `envconfig:"OAUTH_TOKEN_URL" default:"https://accounts.spotify.com/api/token"`
		OauthTokenKey          string `envconfig:"OAUTH_TOKEN_KEY" default:"oauth_token"`
		TrackCacheTTLInSeconds int    `envconfig:"TRACK_CACHE_TTL_IN_SECONDS" default:"86400"`
	}

	FeatureFlags struct {
		CacheCompression bool `envconfig:"FF_CACHE_COMPRESSION" default:"true"`
		CacheOnlyMode    bool `envconfig:"FF_CACHE_ONLY_MODE" default:"false"`
	}
}

// load loads the configuration from the environment.
func load() (Config, error) {
	err := godotenv.Load()
	if err != nil {
		log.Warnf("%s Error loading env config: %v", logcolors.LogConfig, err)
	}

	cfg := Config{}
	err = envconfig.Process("", &cfg)
	return cfg, err
}

func mustLoad() Config {
	c, err := load()
	if err != nil {
		log.WithError(err).Warnf("Unable to load configuration")
	}

	return c
}

func Get() Config {
	return conf
}

// AccountNameMigrations maps old account names to new names.
// When stats are loaded, any usage recorded under old names will be
// merged into the new name. This allows renaming accounts in funNames
// without losing historical stats data.
//
// Format: oldName -> newName
var AccountNameMigrations = map[string]string{
	"Halsey": "Khalid",
}

// APIKeyProtectedPaths defines paths that require API key for cache misses (fresh fetches)
// Cache hits on these paths are still served without API key
var APIKeyProtectedPaths = []string{
	"/getLyrics",
	"/ttml/getLyrics",
	"/revalidate",
}

// TTMLAccount represents a single TTML API account
// Bearer token is now auto-scraped, only MUT is needed per account
type TTMLAccount struct {
	Name           string
	MediaUserToken string
	OutOfService   bool // true if account has empty MUT (excluded from rotation)
}

// funNames contains artist names for account logging
var funNames = []string{
	"Billie", "Toliver", "Taylor", "Dua", "Olivia",
	"Charli", "Khalid", "Tyler", "Crywank", "Future",
	"Offset", "Metro", "Burna", "Phoebe", "Mitski",
	"Finneas", "Clairo", "Raye", "Hozier", "Gracie",
	"Adele", "Ye", "Abel", "Keem", "Yeat",
	"Cannons", "Roosevelt", "Kygo", "Uchis", "Laufey",
	"Impala", "Denzel", "Garrix", "Illenium", "June",
	"Winona", "Carti", "Sivan", "Larsson", "Midnight",
	"Marias", "Lanez", "Odesza", "Flume", "Mura",
	"Gryffin", "Rüfüs", "Jai", "Disclosure", "Kaytranada",
}

// GetTTMLAccounts parses the comma-separated media user tokens and returns only ACTIVE accounts.
// Accounts with empty media user token are excluded from rotation.
// Bearer token is now auto-scraped - only MUTs needed per account.
// Falls back to single token env var if multi-account var is not set.
func (c *Config) GetTTMLAccounts() ([]TTMLAccount, error) {
	mediaUserTokens := c.Configuration.TTMLMediaUserTokens

	// If multi-account var is empty, fall back to single account
	if mediaUserTokens == "" {
		// Check if single account has valid MUT
		if c.Configuration.TTMLMediaUserToken == "" {
			return nil, nil // No accounts configured
		}
		return []TTMLAccount{
			{
				Name:           "Billie",
				MediaUserToken: c.Configuration.TTMLMediaUserToken,
				OutOfService:   false,
			},
		}, nil
	}

	// Parse comma-separated values (preserve empty strings to maintain index alignment)
	mediaUserList := splitAndTrimPreserveEmpty(mediaUserTokens)

	// Build list of active accounts only (those with valid MUT)
	accounts := make([]TTMLAccount, 0, len(mediaUserList))
	for i, mut := range mediaUserList {
		name := fmt.Sprintf("Account-%d", i+1)
		if i < len(funNames) {
			name = funNames[i]
		}

		// Skip accounts with empty MUT - they're out of service
		if mut == "" {
			log.Warnf("%s Account '%s' has empty MUT, excluding from rotation", logcolors.LogConfig, name)
			continue
		}

		accounts = append(accounts, TTMLAccount{
			Name:           name,
			MediaUserToken: mut,
			OutOfService:   false,
		})
	}

	return accounts, nil
}

// GetAllTTMLAccounts returns ALL accounts including out-of-service ones (for monitoring/display).
// Use GetTTMLAccounts() for active accounts only.
// Bearer token is now auto-scraped - only MUTs are configured per account.
func (c *Config) GetAllTTMLAccounts() ([]TTMLAccount, error) {
	mediaUserTokens := c.Configuration.TTMLMediaUserTokens

	// If multi-account var is empty, fall back to single account
	if mediaUserTokens == "" {
		// Check if single account is configured (empty MUT = out of service)
		if c.Configuration.TTMLMediaUserToken == "" {
			return nil, nil // No accounts configured
		}
		return []TTMLAccount{
			{
				Name:           "Billie",
				MediaUserToken: c.Configuration.TTMLMediaUserToken,
				OutOfService:   false, // MUT is present
			},
		}, nil
	}

	// Parse comma-separated values (preserve empty strings to maintain index alignment)
	mediaUserList := splitAndTrimPreserveEmpty(mediaUserTokens)

	// Build list of ALL accounts (including out-of-service)
	accounts := make([]TTMLAccount, len(mediaUserList))
	for i, mut := range mediaUserList {
		name := fmt.Sprintf("Account-%d", i+1)
		if i < len(funNames) {
			name = funNames[i]
		}

		accounts[i] = TTMLAccount{
			Name:           name,
			MediaUserToken: mut,
			OutOfService:   mut == "", // Out of service if empty MUT
		}
	}

	return accounts, nil
}

// SplitAndTrim splits a comma-separated string and trims whitespace from each element
func SplitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// splitAndTrimPreserveEmpty splits a comma-separated string and trims whitespace from each part.
// Empty strings are preserved to maintain index alignment (for multi-account token parsing).
func splitAndTrimPreserveEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = strings.TrimSpace(p)
	}
	return result
}
