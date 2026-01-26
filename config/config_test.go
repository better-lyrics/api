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
		"FF_CACHE_ONLY_MODE",
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
		{
			name:     "CacheOnlyMode default",
			got:      cfg.FeatureFlags.CacheOnlyMode,
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
	os.Setenv("FF_CACHE_ONLY_MODE", "true")

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
		os.Unsetenv("FF_CACHE_ONLY_MODE")
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
		{
			name:     "CacheOnlyMode override",
			got:      cfg.FeatureFlags.CacheOnlyMode,
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

func TestFeatureFlagCacheOnlyMode(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{
			name:     "Cache-only mode enabled (true)",
			envValue: "true",
			expected: true,
		},
		{
			name:     "Cache-only mode disabled (false)",
			envValue: "false",
			expected: false,
		},
		{
			name:     "Cache-only mode enabled (1)",
			envValue: "1",
			expected: true,
		},
		{
			name:     "Cache-only mode disabled (0)",
			envValue: "0",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("FF_CACHE_ONLY_MODE", tt.envValue)
			defer os.Unsetenv("FF_CACHE_ONLY_MODE")

			cfg, err := load()
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			if cfg.FeatureFlags.CacheOnlyMode != tt.expected {
				t.Errorf("Expected CacheOnlyMode %v, got %v", tt.expected, cfg.FeatureFlags.CacheOnlyMode)
			}
		})
	}
}

func TestFeatureFlagCacheOnlyModeDefault(t *testing.T) {
	// Ensure the env var is not set
	os.Unsetenv("FF_CACHE_ONLY_MODE")

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Default should be false (upstream requests enabled)
	if cfg.FeatureFlags.CacheOnlyMode != false {
		t.Errorf("Expected CacheOnlyMode default to be false, got %v", cfg.FeatureFlags.CacheOnlyMode)
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

func TestGetTTMLAccounts_FiltersEmptyCredentials(t *testing.T) {
	// Set multi-account tokens with some empty values
	// Account 1: valid, Account 2: empty MUT, Account 3: valid
	os.Setenv("TTML_BEARER_TOKENS", "bearer1,bearer2,bearer3")
	os.Setenv("TTML_MEDIA_USER_TOKENS", "mut1,,mut3") // Account 2 has empty MUT
	defer func() {
		os.Unsetenv("TTML_BEARER_TOKENS")
		os.Unsetenv("TTML_MEDIA_USER_TOKENS")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	accounts, err := cfg.GetTTMLAccounts()
	if err != nil {
		t.Fatalf("GetTTMLAccounts failed: %v", err)
	}

	// Should only return 2 accounts (Account 1 and Account 3)
	if len(accounts) != 2 {
		t.Errorf("Expected 2 active accounts (filtering empty MUT), got %d", len(accounts))
	}

	// Verify the accounts are Billie (index 0) and Taylor (index 2)
	expectedNames := []string{"Billie", "Taylor"}
	for i, acc := range accounts {
		if acc.Name != expectedNames[i] {
			t.Errorf("Expected account %d name %q, got %q", i, expectedNames[i], acc.Name)
		}
		if acc.OutOfService {
			t.Errorf("Active account %s should not be marked as OutOfService", acc.Name)
		}
	}
}

func TestGetTTMLAccounts_FiltersEmptyBearerToken(t *testing.T) {
	// Test with empty bearer token
	os.Setenv("TTML_BEARER_TOKENS", "bearer1,,bearer3")
	os.Setenv("TTML_MEDIA_USER_TOKENS", "mut1,mut2,mut3")
	defer func() {
		os.Unsetenv("TTML_BEARER_TOKENS")
		os.Unsetenv("TTML_MEDIA_USER_TOKENS")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	accounts, err := cfg.GetTTMLAccounts()
	if err != nil {
		t.Fatalf("GetTTMLAccounts failed: %v", err)
	}

	// Should only return 2 accounts
	if len(accounts) != 2 {
		t.Errorf("Expected 2 active accounts (filtering empty bearer), got %d", len(accounts))
	}
}

func TestGetAllTTMLAccounts_IncludesOutOfService(t *testing.T) {
	// Set multi-account tokens with some empty values
	os.Setenv("TTML_BEARER_TOKENS", "bearer1,bearer2,bearer3")
	os.Setenv("TTML_MEDIA_USER_TOKENS", "mut1,,mut3") // Account 2 has empty MUT
	defer func() {
		os.Unsetenv("TTML_BEARER_TOKENS")
		os.Unsetenv("TTML_MEDIA_USER_TOKENS")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	allAccounts, err := cfg.GetAllTTMLAccounts()
	if err != nil {
		t.Fatalf("GetAllTTMLAccounts failed: %v", err)
	}

	// Should return all 3 accounts
	if len(allAccounts) != 3 {
		t.Errorf("Expected 3 total accounts, got %d", len(allAccounts))
	}

	// Verify OutOfService flags
	expectedOutOfService := []bool{false, true, false}
	for i, acc := range allAccounts {
		if acc.OutOfService != expectedOutOfService[i] {
			t.Errorf("Account %d (%s) OutOfService: expected %v, got %v",
				i, acc.Name, expectedOutOfService[i], acc.OutOfService)
		}
	}
}

func TestGetAllTTMLAccounts_AllValid(t *testing.T) {
	// All accounts have valid credentials
	os.Setenv("TTML_BEARER_TOKENS", "bearer1,bearer2,bearer3")
	os.Setenv("TTML_MEDIA_USER_TOKENS", "mut1,mut2,mut3")
	defer func() {
		os.Unsetenv("TTML_BEARER_TOKENS")
		os.Unsetenv("TTML_MEDIA_USER_TOKENS")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	allAccounts, err := cfg.GetAllTTMLAccounts()
	if err != nil {
		t.Fatalf("GetAllTTMLAccounts failed: %v", err)
	}

	activeAccounts, err := cfg.GetTTMLAccounts()
	if err != nil {
		t.Fatalf("GetTTMLAccounts failed: %v", err)
	}

	// Both should return the same count when all are valid
	if len(allAccounts) != len(activeAccounts) {
		t.Errorf("With all valid accounts, GetAllTTMLAccounts (%d) and GetTTMLAccounts (%d) should return same count",
			len(allAccounts), len(activeAccounts))
	}

	// No accounts should be out of service
	for _, acc := range allAccounts {
		if acc.OutOfService {
			t.Errorf("Account %s should not be OutOfService when credentials are valid", acc.Name)
		}
	}
}

func TestGetTTMLAccounts_SingleAccountEmptyMUT(t *testing.T) {
	// Test single account mode with empty MUT
	os.Unsetenv("TTML_BEARER_TOKENS")
	os.Unsetenv("TTML_MEDIA_USER_TOKENS")
	os.Setenv("TTML_BEARER_TOKEN", "single_bearer")
	os.Setenv("TTML_MEDIA_USER_TOKEN", "") // Empty MUT
	defer func() {
		os.Unsetenv("TTML_BEARER_TOKEN")
		os.Unsetenv("TTML_MEDIA_USER_TOKEN")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	accounts, err := cfg.GetTTMLAccounts()
	if err != nil {
		t.Fatalf("GetTTMLAccounts failed: %v", err)
	}

	// Should return empty (no active accounts)
	if len(accounts) != 0 {
		t.Errorf("Expected 0 active accounts (single account with empty MUT), got %d", len(accounts))
	}
}

func TestGetAllTTMLAccounts_SingleAccountEmptyMUT(t *testing.T) {
	// Test single account mode with empty MUT
	os.Unsetenv("TTML_BEARER_TOKENS")
	os.Unsetenv("TTML_MEDIA_USER_TOKENS")
	os.Setenv("TTML_BEARER_TOKEN", "single_bearer")
	os.Setenv("TTML_MEDIA_USER_TOKEN", "") // Empty MUT
	defer func() {
		os.Unsetenv("TTML_BEARER_TOKEN")
		os.Unsetenv("TTML_MEDIA_USER_TOKEN")
	}()

	cfg, err := load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	allAccounts, err := cfg.GetAllTTMLAccounts()
	if err != nil {
		t.Fatalf("GetAllTTMLAccounts failed: %v", err)
	}

	// Should return 1 account (but marked as out of service)
	if len(allAccounts) != 1 {
		t.Errorf("Expected 1 total account, got %d", len(allAccounts))
	}

	if !allAccounts[0].OutOfService {
		t.Error("Account with empty MUT should be marked as OutOfService")
	}
}

func TestTTMLAccount_OutOfServiceField(t *testing.T) {
	// Test that OutOfService field is properly set
	acc := TTMLAccount{
		Name:           "TestAccount",
		BearerToken:    "bearer",
		MediaUserToken: "mut",
		OutOfService:   false,
	}

	if acc.OutOfService {
		t.Error("Expected OutOfService to be false")
	}

	acc.OutOfService = true
	if !acc.OutOfService {
		t.Error("Expected OutOfService to be true")
	}
}
