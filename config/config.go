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
		RateLimitPerSecond                 int    `envconfig:"RATE_LIMIT_PER_SECOND" default:"2"`
		RateLimitBurstLimit                int    `envconfig:"RATE_LIMIT_BURST_LIMIT" default:"5"`
		CachedRateLimitPerSecond           int    `envconfig:"CACHED_RATE_LIMIT_PER_SECOND" default:"10"`
		CachedRateLimitBurstLimit          int    `envconfig:"CACHED_RATE_LIMIT_BURST_LIMIT" default:"20"`
		CacheInvalidationIntervalInSeconds int    `envconfig:"CACHE_INVALIDATION_INTERVAL_IN_SECONDS" default:"3600"`
		LyricsCacheTTLInSeconds            int    `envconfig:"LYRICS_CACHE_TTL_IN_SECONDS" default:"86400"`
		CacheAccessToken                   string `envconfig:"CACHE_ACCESS_TOKEN" default:""`
		// TTML API Configuration
		// Single account (backwards compatible)
		TTMLBearerToken    string `envconfig:"TTML_BEARER_TOKEN" default:""`
		TTMLMediaUserToken string `envconfig:"TTML_MEDIA_USER_TOKEN" default:""`
		// Multi-account support (comma-separated)
		TTMLBearerTokens           string  `envconfig:"TTML_BEARER_TOKENS" default:""`
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
	}

	FeatureFlags struct {
		CacheCompression bool `envconfig:"FF_CACHE_COMPRESSION" default:"true"`
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

// TTMLAccount represents a single TTML API account
type TTMLAccount struct {
	Name           string
	BearerToken    string
	MediaUserToken string
}

// GetTTMLAccounts parses the comma-separated tokens and returns a slice of accounts.
// Returns an error if the number of bearer tokens doesn't match media user tokens.
// Falls back to single token env vars if multi-account vars are not set.
func (c *Config) GetTTMLAccounts() ([]TTMLAccount, error) {
	bearerTokens := c.Configuration.TTMLBearerTokens
	mediaUserTokens := c.Configuration.TTMLMediaUserTokens

	// If multi-account vars are empty, fall back to single account
	if bearerTokens == "" {
		if c.Configuration.TTMLBearerToken == "" {
			return nil, nil // No accounts configured
		}
		return []TTMLAccount{
			{
				Name:           "Billie",
				BearerToken:    c.Configuration.TTMLBearerToken,
				MediaUserToken: c.Configuration.TTMLMediaUserToken,
			},
		}, nil
	}

	// Parse comma-separated values
	bearerList := splitAndTrim(bearerTokens)
	mediaUserList := splitAndTrim(mediaUserTokens)

	// Validate: must have same number of tokens
	if len(bearerList) != len(mediaUserList) {
		return nil, fmt.Errorf(
			"TTML account mismatch: %d bearer tokens but %d media user tokens. Each account needs both tokens",
			len(bearerList), len(mediaUserList),
		)
	}

	// Artist names for account logging
	funNames := []string{
		"Billie", "Toliver", "Taylor", "Dua", "Olivia",
		"Charli", "Khalid", "Tyler", "Gunna", "Future",
		"Offset", "Metro", "Burna", "Phoebe", "Mitski",
		"Finneas", "Clairo", "Raye", "Hozier", "Gracie",
		"Adele", "Ye", "Abel", "Keem", "Yeat",
		"Cannons", "Roosevelt", "Kygo", "Uchis", "Laufey",
		"Impala", "Denzel", "Garrix", "Illenium", "June",
		"Winona", "Carti", "Sivan", "Larsson", "Midnight",
		"Marias", "Lanez", "Odesza", "Flume", "Mura",
		"Gryffin", "Rüfüs", "Jai", "Disclosure", "Kaytranada",
	}

	accounts := make([]TTMLAccount, len(bearerList))
	for i := range bearerList {
		name := fmt.Sprintf("Account-%d", i+1)
		if i < len(funNames) {
			name = funNames[i]
		}
		accounts[i] = TTMLAccount{
			Name:           name,
			BearerToken:    bearerList[i],
			MediaUserToken: mediaUserList[i],
		}
	}

	return accounts, nil
}

// GetAllBearerTokens returns all configured bearer tokens (for monitoring purposes)
func (c *Config) GetAllBearerTokens() []string {
	accounts, err := c.GetTTMLAccounts()
	if err != nil || len(accounts) == 0 {
		return nil
	}

	tokens := make([]string, len(accounts))
	for i, acc := range accounts {
		tokens[i] = acc.BearerToken
	}
	return tokens
}

// splitAndTrim splits a comma-separated string and trims whitespace from each element
func splitAndTrim(s string) []string {
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
