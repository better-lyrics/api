package ttml

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
)

const (
	// QuarantineDuration is how long an account is quarantined after a 429
	QuarantineDuration = 5 * time.Minute

	// StorefrontCacheFile is the filename for persistent storefront cache
	StorefrontCacheFile = "storefront_cache.json"
)

var (
	accountManager   *AccountManager
	quarantineMutex  sync.RWMutex            // Protects quarantineTime map
	disabledAccounts = make(map[string]bool) // Permanently disabled accounts (stale MUT)
	disabledMutex    sync.RWMutex            // Protects disabledAccounts map

	// Storefront cache: maps MUT hash -> storefront code
	storefrontCache     = make(map[string]string)
	storefrontCachePath string
	storefrontMutex     sync.RWMutex
)

func initAccountManager() {
	conf := config.Get()
	configAccounts, err := conf.GetTTMLAccounts()
	if err != nil {
		log.Fatalf("Failed to initialize TTML accounts: %v", err)
	}

	if len(configAccounts) == 0 {
		log.Warn("No TTML accounts configured")
		accountManager = &AccountManager{
			accounts:       []MusicAccount{},
			currentIndex:   0,
			quarantineTime: make(map[int]int64),
		}
		return
	}

	storefront := conf.Configuration.TTMLStorefront
	if storefront == "" {
		storefront = "us"
	}

	accounts := make([]MusicAccount, len(configAccounts))
	for i, acc := range configAccounts {
		accounts[i] = MusicAccount{
			NameID:         acc.Name,
			MediaUserToken: acc.MediaUserToken,
			Storefront:     storefront,
		}
	}

	accountManager = &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	log.Infof("Initialized %d TTML account(s) with round-robin load balancing", len(accounts))
}

// getNextAccount returns the next non-quarantined, non-disabled account in round-robin fashion (thread-safe)
// If all accounts are quarantined or disabled, returns the one with the shortest remaining quarantine
func (m *AccountManager) getNextAccount() MusicAccount {
	if len(m.accounts) == 0 {
		return MusicAccount{}
	}

	now := time.Now().Unix()
	numAccounts := len(m.accounts)

	// Try to find a non-quarantined, non-disabled account
	for i := 0; i < numAccounts; i++ {
		idx := atomic.AddUint64(&m.currentIndex, 1) - 1
		accountIdx := int(idx % uint64(numAccounts))

		// Skip disabled accounts (stale MUT - permanent)
		if m.IsAccountDisabled(m.accounts[accountIdx].NameID) {
			log.Debugf("%s Skipping %s (disabled - stale MUT)", logcolors.LogQuarantine, logcolors.Account(m.accounts[accountIdx].NameID))
			continue
		}

		// Skip quarantined accounts (rate limited - temporary)
		if !m.isQuarantined(accountIdx, now) {
			return m.accounts[accountIdx]
		}
		log.Debugf("%s Skipping %s (quarantined)", logcolors.LogQuarantine, logcolors.Account(m.accounts[accountIdx].NameID))
	}

	// All accounts quarantined or disabled - find the one with shortest remaining time
	// (only consider non-disabled accounts)
	shortestIdx := -1
	shortestTime := int64(^uint64(0) >> 1) // Max int64

	quarantineMutex.RLock()
	for i := 0; i < numAccounts; i++ {
		// Skip disabled accounts entirely
		if m.IsAccountDisabled(m.accounts[i].NameID) {
			continue
		}

		if endTime, exists := m.quarantineTime[i]; exists {
			remaining := endTime - now
			if remaining < shortestTime {
				shortestTime = remaining
				shortestIdx = i
			}
		} else {
			// Not quarantined, use this one
			shortestIdx = i
			shortestTime = 0
			break
		}
	}
	quarantineMutex.RUnlock()

	// If all accounts are disabled, return empty
	if shortestIdx == -1 {
		log.Errorf("%s All accounts are disabled! No accounts available.", logcolors.LogQuarantine)
		return MusicAccount{}
	}

	if shortestTime > 0 {
		log.Warnf("%s All available accounts quarantined! Using %s (shortest wait: %ds)",
			logcolors.LogQuarantine, logcolors.Account(m.accounts[shortestIdx].NameID), shortestTime)
	}

	return m.accounts[shortestIdx]
}

// isQuarantined checks if an account is currently quarantined
func (m *AccountManager) isQuarantined(accountIdx int, now int64) bool {
	quarantineMutex.RLock()
	defer quarantineMutex.RUnlock()

	endTime, exists := m.quarantineTime[accountIdx]
	if !exists {
		return false
	}
	return now < endTime
}

// quarantineAccount puts an account in quarantine for QuarantineDuration
func (m *AccountManager) quarantineAccount(account MusicAccount) {
	// Find the account index
	accountIdx := -1
	for i, acc := range m.accounts {
		if acc.NameID == account.NameID {
			accountIdx = i
			break
		}
	}

	if accountIdx == -1 {
		log.Warnf("%s Could not find account %s to quarantine", logcolors.LogQuarantine, logcolors.Account(account.NameID))
		return
	}

	quarantineMutex.Lock()
	m.quarantineTime[accountIdx] = time.Now().Add(QuarantineDuration).Unix()
	quarantineMutex.Unlock()

	log.Warnf("%s Account %s quarantined for %v due to rate limit", logcolors.LogQuarantine, logcolors.Account(account.NameID), QuarantineDuration)

	// Check quarantine thresholds and emit events
	m.checkQuarantineThresholds()
}

// checkQuarantineThresholds checks if we've hit quarantine thresholds and emits events
func (m *AccountManager) checkQuarantineThresholds() {
	total := len(m.accounts)
	if total == 0 {
		return
	}

	// Get out-of-service account names for notifications
	outOfServiceNames := getOutOfServiceAccountNames()

	quarantined := total - m.availableAccountCount()
	status := m.getQuarantineStatus()

	// All accounts quarantined
	if quarantined == total {
		notifier.PublishAllAccountsQuarantined(status, outOfServiceNames)
		// Trip circuit breaker since we have no healthy accounts
		TripCircuitBreakerOnFullQuarantine()
		return
	}

	// One account away from all quarantined
	if quarantined == total-1 {
		// Find the remaining healthy account
		now := time.Now().Unix()
		for i, acc := range m.accounts {
			if !m.isQuarantined(i, now) {
				notifier.PublishOneAwayFromQuarantine(acc.NameID, status, outOfServiceNames)
				return
			}
		}
		return
	}

	// Half or more accounts quarantined
	if quarantined >= total/2 && quarantined > 0 {
		notifier.PublishHalfAccountsQuarantined(quarantined, total, status, outOfServiceNames)
	}
}

// getOutOfServiceAccountNames returns names of accounts with empty credentials
func getOutOfServiceAccountNames() []string {
	conf := config.Get()
	allAccounts, err := conf.GetAllTTMLAccounts()
	if err != nil {
		return nil
	}
	var names []string
	for _, acc := range allAccounts {
		if acc.OutOfService {
			names = append(names, acc.Name)
		}
	}
	return names
}

// clearQuarantine removes quarantine from an account (called on successful request)
func (m *AccountManager) clearQuarantine(account MusicAccount) {
	// Find the account index
	accountIdx := -1
	for i, acc := range m.accounts {
		if acc.NameID == account.NameID {
			accountIdx = i
			break
		}
	}

	if accountIdx == -1 {
		return
	}

	quarantineMutex.Lock()
	if _, exists := m.quarantineTime[accountIdx]; exists {
		delete(m.quarantineTime, accountIdx)
		log.Infof("%s Account %s quarantine cleared (successful request)", logcolors.LogQuarantine, logcolors.Account(account.NameID))
	}
	quarantineMutex.Unlock()
}

// getQuarantineStatus returns a map of account names to remaining quarantine seconds
func (m *AccountManager) getQuarantineStatus() map[string]int64 {
	now := time.Now().Unix()
	status := make(map[string]int64)

	quarantineMutex.RLock()
	defer quarantineMutex.RUnlock()

	for idx, endTime := range m.quarantineTime {
		remaining := endTime - now
		if remaining > 0 && idx < len(m.accounts) {
			status[m.accounts[idx].NameID] = remaining
		}
	}

	return status
}

func (m *AccountManager) hasAccounts() bool {
	return len(m.accounts) > 0
}

func (m *AccountManager) accountCount() int {
	return len(m.accounts)
}

// availableAccountCount returns the number of non-quarantined, non-disabled accounts
func (m *AccountManager) availableAccountCount() int {
	now := time.Now().Unix()
	count := 0
	for i, acc := range m.accounts {
		// Skip disabled accounts (stale MUT)
		if m.IsAccountDisabled(acc.NameID) {
			continue
		}
		if !m.isQuarantined(i, now) {
			count++
		}
	}
	return count
}

// IsAccountQuarantinedByName checks if an account is quarantined by its name ID
func (m *AccountManager) IsAccountQuarantinedByName(nameID string) bool {
	now := time.Now().Unix()
	for i, acc := range m.accounts {
		if acc.NameID == nameID {
			return m.isQuarantined(i, now)
		}
	}
	return false
}

// IsAccountDisabled checks if an account is permanently disabled (stale MUT)
func (m *AccountManager) IsAccountDisabled(nameID string) bool {
	disabledMutex.RLock()
	defer disabledMutex.RUnlock()
	return disabledAccounts[nameID]
}

// DisableAccount permanently disables an account (called when MUT is detected as stale via 404 on canary)
func (m *AccountManager) DisableAccount(account MusicAccount) {
	disabledMutex.Lock()
	disabledAccounts[account.NameID] = true
	disabledMutex.Unlock()

	log.Errorf("%s Account %s PERMANENTLY DISABLED (stale MUT - 404 on canary)",
		logcolors.LogQuarantine, logcolors.Account(account.NameID))

	// Check if this triggers circuit breaker (all accounts unavailable)
	m.checkQuarantineThresholds()
}

// =============================================================================
// STOREFRONT CACHE
// =============================================================================

// hashMUT returns a SHA256 hash of the media user token
func hashMUT(mut string) string {
	h := sha256.Sum256([]byte(mut))
	return hex.EncodeToString(h[:])
}

// loadStorefrontCache loads the storefront cache from disk
func loadStorefrontCache() {
	storefrontMutex.Lock()
	defer storefrontMutex.Unlock()

	// Determine cache path (same directory as cache.db) if not already set
	if storefrontCachePath == "" {
		cacheDir := os.Getenv("CACHE_DB_PATH")
		if cacheDir == "" {
			cacheDir = "./cache.db"
		}
		storefrontCachePath = filepath.Join(filepath.Dir(cacheDir), StorefrontCacheFile)
	}

	data, err := os.ReadFile(storefrontCachePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warnf("%s Failed to read storefront cache: %v", logcolors.LogAccountInit, err)
		}
		return
	}

	if err := json.Unmarshal(data, &storefrontCache); err != nil {
		log.Warnf("%s Failed to parse storefront cache: %v", logcolors.LogAccountInit, err)
		return
	}

	log.Debugf("%s Loaded %d cached storefronts", logcolors.LogAccountInit, len(storefrontCache))
}

// saveStorefrontCache persists the storefront cache to disk
func saveStorefrontCache() {
	storefrontMutex.RLock()
	data, err := json.MarshalIndent(storefrontCache, "", "  ")
	storefrontMutex.RUnlock()

	if err != nil {
		log.Warnf("%s Failed to marshal storefront cache: %v", logcolors.LogAccountInit, err)
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(storefrontCachePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Warnf("%s Failed to create cache directory: %v", logcolors.LogAccountInit, err)
		return
	}

	if err := os.WriteFile(storefrontCachePath, data, 0644); err != nil {
		log.Warnf("%s Failed to write storefront cache: %v", logcolors.LogAccountInit, err)
		return
	}

	log.Debugf("%s Saved %d storefronts to cache", logcolors.LogAccountInit, len(storefrontCache))
}

// getCachedStorefront returns the cached storefront for a MUT, or empty string if not cached
func getCachedStorefront(mut string) string {
	storefrontMutex.RLock()
	defer storefrontMutex.RUnlock()
	return storefrontCache[hashMUT(mut)]
}

// setCachedStorefront stores a storefront for a MUT in the cache
func setCachedStorefront(mut, storefront string) {
	storefrontMutex.Lock()
	defer storefrontMutex.Unlock()
	storefrontCache[hashMUT(mut)] = storefront
}

// =============================================================================
// STOREFRONT FETCHING
// =============================================================================

// fetchAccountStorefront fetches the storefront for a specific account from Apple Music's account API.
// Returns the storefront code (e.g., "us", "in", "gb") or an error.
func fetchAccountStorefront(account MusicAccount) (string, error) {
	if account.MediaUserToken == "" {
		return "", fmt.Errorf("account has no media user token")
	}

	// Get bearer token for auth
	bearerToken, err := GetBearerToken()
	if err != nil {
		return "", fmt.Errorf("failed to get bearer token: %w", err)
	}

	conf := config.Get()
	accountURL := conf.Configuration.TTMLBaseURL + "/me/account?meta=subscription"

	req, err := http.NewRequest("GET", accountURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers (same as lyrics API)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("media-user-token", account.MediaUserToken)
	req.Header.Set("Origin", "https://music.apple.com")
	req.Header.Set("Referer", "https://music.apple.com/")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var accountResp AccountResponse
	if err := json.Unmarshal(body, &accountResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	storefront := accountResp.Meta.Subscription.Storefront
	if storefront == "" {
		return "", fmt.Errorf("empty storefront in response")
	}

	return storefront, nil
}

// InitializeAccountStorefronts fetches and sets the storefront for each account.
// Uses persistent cache to avoid refetching storefronts when MUT hasn't changed.
// This should be called after the bearer token is available.
// On failure, accounts retain their default storefront from config.
func InitializeAccountStorefronts() {
	// Ensure account manager is initialized
	if accountManager == nil {
		initAccountManager()
	}

	if accountManager == nil || len(accountManager.accounts) == 0 {
		log.Warnf("%s No accounts to initialize storefronts for", logcolors.LogAccountInit)
		return
	}

	// Load cached storefronts from disk
	loadStorefrontCache()

	log.Infof("%s Initializing storefronts for %d account(s)...", logcolors.LogAccountInit, len(accountManager.accounts))

	cacheUpdated := false
	for i := range accountManager.accounts {
		account := &accountManager.accounts[i]

		// Skip accounts with empty MUT (out-of-service)
		if account.MediaUserToken == "" {
			log.Debugf("%s Skipping %s (no MUT)", logcolors.LogAccountInit, logcolors.Account(account.NameID))
			continue
		}

		// Check cache first
		cachedStorefront := getCachedStorefront(account.MediaUserToken)
		if cachedStorefront != "" {
			if cachedStorefront != account.Storefront {
				log.Infof("%s %s storefront: %s → %s (from cache)",
					logcolors.LogAccountInit, logcolors.Account(account.NameID), account.Storefront, cachedStorefront)
				account.Storefront = cachedStorefront
			} else {
				log.Infof("%s %s storefront: %s (cached)",
					logcolors.LogAccountInit, logcolors.Account(account.NameID), cachedStorefront)
			}
			continue
		}

		// Not in cache - fetch from API
		storefront, err := fetchAccountStorefront(*account)
		if err != nil {
			log.Warnf("%s Failed to fetch storefront for %s, keeping default %q: %v",
				logcolors.LogAccountInit, logcolors.Account(account.NameID), account.Storefront, err)
			continue
		}

		// Update account and cache
		if storefront != account.Storefront {
			log.Infof("%s %s storefront: %s → %s (fetched)",
				logcolors.LogAccountInit, logcolors.Account(account.NameID), account.Storefront, storefront)
			account.Storefront = storefront
		} else {
			log.Infof("%s %s storefront: %s (fetched)",
				logcolors.LogAccountInit, logcolors.Account(account.NameID), storefront)
		}

		// Cache the fetched storefront
		setCachedStorefront(account.MediaUserToken, storefront)
		cacheUpdated = true
	}

	// Save cache if updated
	if cacheUpdated {
		saveStorefrontCache()
	}

	log.Infof("%s Storefront initialization complete", logcolors.LogAccountInit)
}
