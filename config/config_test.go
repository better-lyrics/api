package config

import (
	"os"
	"testing"
)

func TestConfigDefaultValues(t *testing.T) {
	// Clear any existing env vars that might interfere
	envVars := []string{
		"RATE_LIMIT_PER_SECOND",
		"RATE_LIMIT_BURST_LIMIT",
		"CACHED_RATE_LIMIT_PER_SECOND",
		"CACHED_RATE_LIMIT_BURST_LIMIT",
		"CACHE_INVALIDATION_INTERVAL_IN_SECONDS",
		"LYRICS_CACHE_TTL_IN_SECONDS",
		"FF_CACHE_COMPRESSION",
		"TTML_STOREFRONT",
	}

	// Store original values
	originalValues := make(map[string]string)
	for _, key := range envVars {
		originalValues[key] = os.Getenv(key)
		os.Unsetenv(key)
	}
	defer func() {
		// Restore original values
		for key, value := range originalValues {
			if value != "" {
				os.Setenv(key, value)
			}
		}
	}()

	// Load config
	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{
			name:     "RateLimitPerSecond default",
			got:      cfg.Configuration.RateLimitPerSecond,
			expected: 2,
		},
		{
			name:     "RateLimitBurstLimit default",
			got:      cfg.Configuration.RateLimitBurstLimit,
			expected: 5,
		},
		{
			name:     "CachedRateLimitPerSecond default",
			got:      cfg.Configuration.CachedRateLimitPerSecond,
			expected: 10,
		},
		{
			name:     "CachedRateLimitBurstLimit default",
			got:      cfg.Configuration.CachedRateLimitBurstLimit,
			expected: 20,
		},
		{
			name:     "CacheInvalidationIntervalInSeconds default",
			got:      cfg.Configuration.CacheInvalidationIntervalInSeconds,
			expected: 3600,
		},
		{
			name:     "LyricsCacheTTLInSeconds default",
			got:      cfg.Configuration.LyricsCacheTTLInSeconds,
			expected: 86400,
		},
		{
			name:     "TTMLStorefront default",
			got:      cfg.Configuration.TTMLStorefront,
			expected: "us",
		},
		{
			name:     "CacheCompression default",
			got:      cfg.FeatureFlags.CacheCompression,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, tt.got)
			}
		})
	}
}

func TestConfigEnvironmentOverrides(t *testing.T) {
	// Set custom environment variables
	os.Setenv("RATE_LIMIT_PER_SECOND", "5")
	os.Setenv("RATE_LIMIT_BURST_LIMIT", "15")
	os.Setenv("CACHED_RATE_LIMIT_PER_SECOND", "25")
	os.Setenv("CACHED_RATE_LIMIT_BURST_LIMIT", "50")
	os.Setenv("CACHE_INVALIDATION_INTERVAL_IN_SECONDS", "7200")
	os.Setenv("LYRICS_CACHE_TTL_IN_SECONDS", "172800")
	os.Setenv("CACHE_ACCESS_TOKEN", "test_token_123")
	os.Setenv("TTML_STOREFRONT", "jp")
	os.Setenv("FF_CACHE_COMPRESSION", "false")

	defer func() {
		// Clean up
		os.Unsetenv("RATE_LIMIT_PER_SECOND")
		os.Unsetenv("RATE_LIMIT_BURST_LIMIT")
		os.Unsetenv("CACHED_RATE_LIMIT_PER_SECOND")
		os.Unsetenv("CACHED_RATE_LIMIT_BURST_LIMIT")
		os.Unsetenv("CACHE_INVALIDATION_INTERVAL_IN_SECONDS")
		os.Unsetenv("LYRICS_CACHE_TTL_IN_SECONDS")
		os.Unsetenv("CACHE_ACCESS_TOKEN")
		os.Unsetenv("TTML_STOREFRONT")
		os.Unsetenv("FF_CACHE_COMPRESSION")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{
			name:     "RateLimitPerSecond override",
			got:      cfg.Configuration.RateLimitPerSecond,
			expected: 5,
		},
		{
			name:     "RateLimitBurstLimit override",
			got:      cfg.Configuration.RateLimitBurstLimit,
			expected: 15,
		},
		{
			name:     "CachedRateLimitPerSecond override",
			got:      cfg.Configuration.CachedRateLimitPerSecond,
			expected: 25,
		},
		{
			name:     "CachedRateLimitBurstLimit override",
			got:      cfg.Configuration.CachedRateLimitBurstLimit,
			expected: 50,
		},
		{
			name:     "CacheInvalidationIntervalInSeconds override",
			got:      cfg.Configuration.CacheInvalidationIntervalInSeconds,
			expected: 7200,
		},
		{
			name:     "LyricsCacheTTLInSeconds override",
			got:      cfg.Configuration.LyricsCacheTTLInSeconds,
			expected: 172800,
		},
		{
			name:     "CacheAccessToken override",
			got:      cfg.Configuration.CacheAccessToken,
			expected: "test_token_123",
		},
		{
			name:     "TTMLStorefront override",
			got:      cfg.Configuration.TTMLStorefront,
			expected: "jp",
		},
		{
			name:     "CacheCompression override",
			got:      cfg.FeatureFlags.CacheCompression,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, tt.got)
			}
		})
	}
}

func TestConfigTTMLSettings(t *testing.T) {
	// Set TTML-specific environment variables
	os.Setenv("TTML_BEARER_TOKEN", "test_bearer_token")
	os.Setenv("TTML_MEDIA_USER_TOKEN", "test_media_user_token")
	os.Setenv("TTML_BASE_URL", "https://api.example.com")
	os.Setenv("TTML_SEARCH_PATH", "/search")
	os.Setenv("TTML_LYRICS_PATH", "/lyrics")

	defer func() {
		os.Unsetenv("TTML_BEARER_TOKEN")
		os.Unsetenv("TTML_MEDIA_USER_TOKEN")
		os.Unsetenv("TTML_BASE_URL")
		os.Unsetenv("TTML_SEARCH_PATH")
		os.Unsetenv("TTML_LYRICS_PATH")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Configuration.TTMLBearerToken != "test_bearer_token" {
		t.Errorf("Expected TTMLBearerToken 'test_bearer_token', got %q", cfg.Configuration.TTMLBearerToken)
	}
	if cfg.Configuration.TTMLMediaUserToken != "test_media_user_token" {
		t.Errorf("Expected TTMLMediaUserToken 'test_media_user_token', got %q", cfg.Configuration.TTMLMediaUserToken)
	}
	if cfg.Configuration.TTMLBaseURL != "https://api.example.com" {
		t.Errorf("Expected TTMLBaseURL 'https://api.example.com', got %q", cfg.Configuration.TTMLBaseURL)
	}
	if cfg.Configuration.TTMLSearchPath != "/search" {
		t.Errorf("Expected TTMLSearchPath '/search', got %q", cfg.Configuration.TTMLSearchPath)
	}
	if cfg.Configuration.TTMLLyricsPath != "/lyrics" {
		t.Errorf("Expected TTMLLyricsPath '/lyrics', got %q", cfg.Configuration.TTMLLyricsPath)
	}
}

func TestGet(t *testing.T) {
	// Test that Get() returns the global config
	cfg := Get()

	// Should return a valid config struct
	if cfg.Configuration.RateLimitPerSecond == 0 && cfg.Configuration.RateLimitBurstLimit == 0 {
		t.Error("Expected Get() to return initialized config, got zero values")
	}
}

func TestMustLoad(t *testing.T) {
	// mustLoad should not panic with valid config
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("mustLoad() panicked: %v", r)
		}
	}()

	cfg := mustLoad()

	// Verify it returns a config with defaults
	if cfg.Configuration.RateLimitPerSecond <= 0 {
		t.Error("Expected mustLoad to return valid config with positive RateLimitPerSecond")
	}
}

func TestFeatureFlagCacheCompression(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{
			name:     "Cache compression enabled (true)",
			envValue: "true",
			expected: true,
		},
		{
			name:     "Cache compression disabled (false)",
			envValue: "false",
			expected: false,
		},
		{
			name:     "Cache compression enabled (1)",
			envValue: "1",
			expected: true,
		},
		{
			name:     "Cache compression disabled (0)",
			envValue: "0",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("FF_CACHE_COMPRESSION", tt.envValue)
			defer os.Unsetenv("FF_CACHE_COMPRESSION")

			cfg, err := load()
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			if cfg.FeatureFlags.CacheCompression != tt.expected {
				t.Errorf("Expected CacheCompression %v, got %v", tt.expected, cfg.FeatureFlags.CacheCompression)
			}
		})
	}
}

func TestConfigStringFields(t *testing.T) {
	// Test that string fields handle empty values correctly
	os.Setenv("CACHE_ACCESS_TOKEN", "")
	os.Setenv("TTML_BEARER_TOKEN", "")
	defer func() {
		os.Unsetenv("CACHE_ACCESS_TOKEN")
		os.Unsetenv("TTML_BEARER_TOKEN")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Configuration.CacheAccessToken != "" {
		t.Errorf("Expected empty CacheAccessToken, got %q", cfg.Configuration.CacheAccessToken)
	}
	if cfg.Configuration.TTMLBearerToken != "" {
		t.Errorf("Expected empty TTMLBearerToken, got %q", cfg.Configuration.TTMLBearerToken)
	}
}
