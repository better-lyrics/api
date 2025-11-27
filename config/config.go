package config

import (
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
		TTMLBearerToken    string `envconfig:"TTML_BEARER_TOKEN" default:""`
		TTMLMediaUserToken string `envconfig:"TTML_MEDIA_USER_TOKEN" default:""`
		TTMLStorefront     string `envconfig:"TTML_STOREFRONT" default:"us"`
		TTMLBaseURL        string `envconfig:"TTML_BASE_URL" default:""`
		TTMLSearchPath     string `envconfig:"TTML_SEARCH_PATH" default:""`
		TTMLLyricsPath       string  `envconfig:"TTML_LYRICS_PATH" default:""`
		MinSimilarityScore          float64 `envconfig:"MIN_SIMILARITY_SCORE" default:"0.6"`
		DurationMatchDeltaMs        int     `envconfig:"DURATION_MATCH_DELTA_MS" default:"2000"`  // Strict duration filter: reject tracks outside this delta (in ms)
		NegativeCacheTTLInDays      int     `envconfig:"NEGATIVE_CACHE_TTL_DAYS" default:"7"`     // TTL for caching "no lyrics found" responses
		CircuitBreakerThreshold     int     `envconfig:"CIRCUIT_BREAKER_THRESHOLD" default:"5"`   // Consecutive failures before circuit opens
		CircuitBreakerCooldownSecs  int     `envconfig:"CIRCUIT_BREAKER_COOLDOWN_SECS" default:"300"` // Seconds to wait before retrying (default: 5 minutes)
	}

	FeatureFlags struct {
		CacheCompression bool `envconfig:"FF_CACHE_COMPRESSION" default:"true"`
	}
}

// load loads the configuration from the environment.
func load() (Config, error) {
	err := godotenv.Load()
	if err != nil {
		log.Warnf("Error loading env config: %v", err)
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
