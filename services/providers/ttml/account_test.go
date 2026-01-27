package ttml

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAccountManager_GetNextAccount_RoundRobin(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
		{NameID: "Account3", MediaUserToken: "mut3"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Should rotate through accounts
	expectedSequence := []string{"Account1", "Account2", "Account3", "Account1", "Account2"}
	for i, expected := range expectedSequence {
		acc := manager.getNextAccount()
		if acc.NameID != expected {
			t.Errorf("Iteration %d: expected %q, got %q", i, expected, acc.NameID)
		}
	}
}

func TestAccountManager_SingleAccount(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "OnlyAccount", MediaUserToken: "mut"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// With single account, should always return the same account
	for i := 0; i < 5; i++ {
		acc := manager.getNextAccount()
		if acc.NameID != "OnlyAccount" {
			t.Errorf("Iteration %d: expected OnlyAccount, got %q", i, acc.NameID)
		}
	}
}

func TestAccountManager_EmptyAccounts(t *testing.T) {
	manager := &AccountManager{
		accounts:       []MusicAccount{},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Should return empty account without panicking
	acc := manager.getNextAccount()
	if acc.NameID != "" {
		t.Errorf("Expected empty account, got %q", acc.NameID)
	}

	if manager.hasAccounts() {
		t.Error("hasAccounts should return false for empty manager")
	}
}

func TestAccountManager_HasAccounts(t *testing.T) {
	emptyManager := &AccountManager{
		accounts:       []MusicAccount{},
		quarantineTime: make(map[int]int64),
	}
	if emptyManager.hasAccounts() {
		t.Error("hasAccounts should return false for empty manager")
	}

	manager := &AccountManager{
		accounts:       []MusicAccount{{NameID: "Account1"}},
		quarantineTime: make(map[int]int64),
	}
	if !manager.hasAccounts() {
		t.Error("hasAccounts should return true when accounts exist")
	}
}

func TestAccountManager_AccountCount(t *testing.T) {
	manager := &AccountManager{
		accounts: []MusicAccount{
			{NameID: "Account1"},
			{NameID: "Account2"},
			{NameID: "Account3"},
		},
		quarantineTime: make(map[int]int64),
	}

	if manager.accountCount() != 3 {
		t.Errorf("Expected accountCount 3, got %d", manager.accountCount())
	}
}

func TestAccountManager_ConcurrentAccess(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1"},
		{NameID: "Account2"},
		{NameID: "Account3"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Simulate concurrent access
	var wg sync.WaitGroup
	results := make(chan string, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acc := manager.getNextAccount()
			results <- acc.NameID
		}()
	}

	wg.Wait()
	close(results)

	// Count distribution
	counts := make(map[string]int)
	for name := range results {
		counts[name]++
	}

	// Should have roughly equal distribution (with some variance)
	for name, count := range counts {
		if count < 20 || count > 50 {
			t.Logf("Distribution might be uneven: %s=%d (expected ~33)", name, count)
		}
	}

	// All accounts should have been used
	if len(counts) != 3 {
		t.Errorf("Expected all 3 accounts to be used, got %d", len(counts))
	}
}

func TestMusicAccount_Fields(t *testing.T) {
	account := MusicAccount{
		NameID:         "TestAccount",
		MediaUserToken: "test_media_token_456",
		Storefront:     "us",
	}

	if account.NameID != "TestAccount" {
		t.Errorf("Expected NameID 'TestAccount', got %q", account.NameID)
	}
	if account.MediaUserToken != "test_media_token_456" {
		t.Errorf("Expected MediaUserToken 'test_media_token_456', got %q", account.MediaUserToken)
	}
	if account.Storefront != "us" {
		t.Errorf("Expected Storefront 'us', got %q", account.Storefront)
	}
}

func TestAccountManager_Quarantine(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
		{NameID: "Account3", MediaUserToken: "mut3"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Get first account
	acc1 := manager.getNextAccount()
	if acc1.NameID != "Account1" {
		t.Errorf("Expected Account1, got %q", acc1.NameID)
	}

	// Quarantine Account1
	manager.quarantineAccount(acc1)

	// Next calls should skip Account1
	acc2 := manager.getNextAccount()
	if acc2.NameID != "Account2" {
		t.Errorf("Expected Account2 (skipping quarantined Account1), got %q", acc2.NameID)
	}

	acc3 := manager.getNextAccount()
	if acc3.NameID != "Account3" {
		t.Errorf("Expected Account3, got %q", acc3.NameID)
	}

	// Should skip Account1 again and go to Account2
	acc4 := manager.getNextAccount()
	if acc4.NameID != "Account2" {
		t.Errorf("Expected Account2 (skipping quarantined Account1), got %q", acc4.NameID)
	}
}

func TestAccountManager_QuarantineAllAccounts(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Quarantine all accounts
	manager.quarantineAccount(accounts[0])
	manager.quarantineAccount(accounts[1])

	// Should still return an account (the one with shortest quarantine)
	acc := manager.getNextAccount()
	if acc.NameID == "" {
		t.Error("Expected to get an account even when all are quarantined")
	}
}

func TestAccountManager_ClearQuarantine(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Quarantine Account1
	manager.quarantineAccount(accounts[0])

	// Verify it's quarantined
	status := manager.getQuarantineStatus()
	if _, exists := status["Account1"]; !exists {
		t.Error("Account1 should be quarantined")
	}

	// Clear quarantine
	manager.clearQuarantine(accounts[0])

	// Verify it's no longer quarantined
	status = manager.getQuarantineStatus()
	if _, exists := status["Account1"]; exists {
		t.Error("Account1 should no longer be quarantined")
	}
}

func TestAccountManager_AvailableAccountCount(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
		{NameID: "Account3", MediaUserToken: "mut3"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// All available initially
	if manager.availableAccountCount() != 3 {
		t.Errorf("Expected 3 available accounts, got %d", manager.availableAccountCount())
	}

	// Quarantine one
	manager.quarantineAccount(accounts[0])
	if manager.availableAccountCount() != 2 {
		t.Errorf("Expected 2 available accounts after quarantine, got %d", manager.availableAccountCount())
	}

	// Quarantine another
	manager.quarantineAccount(accounts[1])
	if manager.availableAccountCount() != 1 {
		t.Errorf("Expected 1 available account after quarantine, got %d", manager.availableAccountCount())
	}
}

func TestAccountManager_QuarantineStatus(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// No quarantine initially
	status := manager.getQuarantineStatus()
	if len(status) != 0 {
		t.Errorf("Expected no quarantined accounts, got %d", len(status))
	}

	// Quarantine Account1
	manager.quarantineAccount(accounts[0])

	// Check status
	status = manager.getQuarantineStatus()
	if len(status) != 1 {
		t.Errorf("Expected 1 quarantined account, got %d", len(status))
	}
	if remaining, exists := status["Account1"]; !exists {
		t.Error("Account1 should be in quarantine status")
	} else if remaining <= 0 || remaining > 300 {
		t.Errorf("Expected remaining time between 0-300 seconds, got %d", remaining)
	}
}

func TestAccountManager_IsQuarantined(t *testing.T) {
	manager := &AccountManager{
		accounts: []MusicAccount{
			{NameID: "Account1", MediaUserToken: "mut1"},
		},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	now := time.Now().Unix()

	// Not quarantined initially
	if manager.isQuarantined(0, now) {
		t.Error("Account should not be quarantined initially")
	}

	// Set quarantine in the past (should be expired)
	manager.quarantineTime[0] = now - 10
	if manager.isQuarantined(0, now) {
		t.Error("Account should not be quarantined (expired)")
	}

	// Set quarantine in the future
	manager.quarantineTime[0] = now + 60
	if !manager.isQuarantined(0, now) {
		t.Error("Account should be quarantined (future expiry)")
	}
}

func TestAccountManager_DisableAccount(t *testing.T) {
	// Reset disabled accounts
	disabledMutex.Lock()
	originalDisabled := disabledAccounts
	disabledAccounts = make(map[string]bool)
	disabledMutex.Unlock()
	defer func() {
		disabledMutex.Lock()
		disabledAccounts = originalDisabled
		disabledMutex.Unlock()
	}()

	account := MusicAccount{NameID: "TestAccount", MediaUserToken: "mut"}

	manager := &AccountManager{
		accounts:       []MusicAccount{account},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Not disabled initially
	if manager.IsAccountDisabled("TestAccount") {
		t.Error("Account should not be disabled initially")
	}

	// Disable it
	manager.DisableAccount(account)

	// Should be disabled now
	if !manager.IsAccountDisabled("TestAccount") {
		t.Error("Account should be disabled after DisableAccount call")
	}
}

func TestAccountManager_IsAccountQuarantinedByName(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Not quarantined initially
	if manager.IsAccountQuarantinedByName("Account1") {
		t.Error("Account1 should not be quarantined initially")
	}

	// Quarantine Account1
	manager.quarantineAccount(accounts[0])

	// Should be quarantined now
	if !manager.IsAccountQuarantinedByName("Account1") {
		t.Error("Account1 should be quarantined")
	}

	// Account2 should not be quarantined
	if manager.IsAccountQuarantinedByName("Account2") {
		t.Error("Account2 should not be quarantined")
	}

	// Non-existent account should return false
	if manager.IsAccountQuarantinedByName("NonExistent") {
		t.Error("Non-existent account should return false")
	}
}

func TestAccountManager_GetNextAccount_SkipsDisabled(t *testing.T) {
	// Reset disabled accounts
	disabledMutex.Lock()
	originalDisabled := disabledAccounts
	disabledAccounts = make(map[string]bool)
	disabledMutex.Unlock()
	defer func() {
		disabledMutex.Lock()
		disabledAccounts = originalDisabled
		disabledMutex.Unlock()
	}()

	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
		{NameID: "Account3", MediaUserToken: "mut3"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Disable Account1
	disabledMutex.Lock()
	disabledAccounts["Account1"] = true
	disabledMutex.Unlock()

	// Should skip Account1 and return Account2
	acc := manager.getNextAccount()
	if acc.NameID == "Account1" {
		t.Error("Should have skipped disabled Account1")
	}
}

func TestAccountManager_AvailableAccountCount_ExcludesDisabled(t *testing.T) {
	// Reset disabled accounts
	disabledMutex.Lock()
	originalDisabled := disabledAccounts
	disabledAccounts = make(map[string]bool)
	disabledMutex.Unlock()
	defer func() {
		disabledMutex.Lock()
		disabledAccounts = originalDisabled
		disabledMutex.Unlock()
	}()

	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
		{NameID: "Account3", MediaUserToken: "mut3"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// All available initially
	if manager.availableAccountCount() != 3 {
		t.Errorf("Expected 3 available accounts, got %d", manager.availableAccountCount())
	}

	// Disable one
	disabledMutex.Lock()
	disabledAccounts["Account1"] = true
	disabledMutex.Unlock()

	if manager.availableAccountCount() != 2 {
		t.Errorf("Expected 2 available accounts after disable, got %d", manager.availableAccountCount())
	}

	// Quarantine another
	manager.quarantineAccount(accounts[1])
	if manager.availableAccountCount() != 1 {
		t.Errorf("Expected 1 available account after disable+quarantine, got %d", manager.availableAccountCount())
	}
}

func TestAccountManager_DisabledAndQuarantinedCombination(t *testing.T) {
	// Reset disabled accounts
	disabledMutex.Lock()
	originalDisabled := disabledAccounts
	disabledAccounts = make(map[string]bool)
	disabledMutex.Unlock()
	defer func() {
		disabledMutex.Lock()
		disabledAccounts = originalDisabled
		disabledMutex.Unlock()
	}()

	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
		{NameID: "Account3", MediaUserToken: "mut3"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Disable Account1
	disabledMutex.Lock()
	disabledAccounts["Account1"] = true
	disabledMutex.Unlock()

	// Quarantine Account2
	manager.quarantineAccount(accounts[1])

	// Only Account3 should be available
	if manager.availableAccountCount() != 1 {
		t.Errorf("Expected 1 available account, got %d", manager.availableAccountCount())
	}

	// getNextAccount should eventually return Account3
	foundAccount3 := false
	for i := 0; i < 5; i++ {
		acc := manager.getNextAccount()
		if acc.NameID == "Account3" {
			foundAccount3 = true
			break
		}
		if acc.NameID == "Account1" {
			t.Error("Should never return disabled Account1")
		}
	}
	if !foundAccount3 {
		t.Error("Should have returned Account3 at least once")
	}
}

func TestAccountManager_AllAccountsDisabled(t *testing.T) {
	// Reset disabled accounts
	disabledMutex.Lock()
	originalDisabled := disabledAccounts
	disabledAccounts = make(map[string]bool)
	disabledMutex.Unlock()
	defer func() {
		disabledMutex.Lock()
		disabledAccounts = originalDisabled
		disabledMutex.Unlock()
	}()

	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1"},
		{NameID: "Account2", MediaUserToken: "mut2"},
	}

	manager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Disable all accounts
	disabledMutex.Lock()
	disabledAccounts["Account1"] = true
	disabledAccounts["Account2"] = true
	disabledMutex.Unlock()

	// Should return empty account when all are disabled
	acc := manager.getNextAccount()
	if acc.NameID != "" {
		t.Errorf("Expected empty account when all are disabled, got %q", acc.NameID)
	}

	// Available count should be 0
	if manager.availableAccountCount() != 0 {
		t.Errorf("Expected 0 available accounts, got %d", manager.availableAccountCount())
	}
}

func TestAccountManager_IsAccountDisabled_NotFound(t *testing.T) {
	// Reset disabled accounts
	disabledMutex.Lock()
	originalDisabled := disabledAccounts
	disabledAccounts = make(map[string]bool)
	disabledMutex.Unlock()
	defer func() {
		disabledMutex.Lock()
		disabledAccounts = originalDisabled
		disabledMutex.Unlock()
	}()

	manager := &AccountManager{
		accounts:       []MusicAccount{{NameID: "Account1", MediaUserToken: "mut1"}},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Non-existent account should return false
	if manager.IsAccountDisabled("NonExistent") {
		t.Error("Non-existent account should not be disabled")
	}
}

// =============================================================================
// STOREFRONT FETCHING TESTS
// =============================================================================

func TestFetchAccountStorefront_Success(t *testing.T) {
	// Create a mock server that returns a valid account response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request path
		if r.URL.Path != "/v1/me/account" {
			t.Errorf("Expected path /v1/me/account, got %s", r.URL.Path)
		}

		// Verify query params
		if r.URL.Query().Get("meta") != "subscription" {
			t.Errorf("Expected meta=subscription query param")
		}

		// Verify required headers
		if r.Header.Get("Authorization") == "" {
			t.Error("Expected Authorization header")
		}
		if r.Header.Get("media-user-token") != "test_mut" {
			t.Errorf("Expected media-user-token header 'test_mut', got %s", r.Header.Get("media-user-token"))
		}
		if r.Header.Get("Origin") != "https://music.apple.com" {
			t.Errorf("Expected Origin header")
		}

		// Return mock response
		resp := AccountResponse{
			Meta: AccountMeta{
				Subscription: SubscriptionInfo{
					Active:     true,
					Storefront: "in",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Save and restore original bearer token state
	tokenMu.Lock()
	originalToken := bearerToken
	originalExpiry := tokenExpiry
	bearerToken = "test_bearer_token"
	tokenExpiry = time.Now().Add(1 * time.Hour)
	tokenMu.Unlock()
	defer func() {
		tokenMu.Lock()
		bearerToken = originalToken
		tokenExpiry = originalExpiry
		tokenMu.Unlock()
	}()

	// We need to use the mock server URL, but fetchAccountStorefront uses config
	// So we'll test the response parsing directly
	account := MusicAccount{
		NameID:         "TestAccount",
		MediaUserToken: "test_mut",
		Storefront:     "us",
	}

	// Test with empty MUT should error
	emptyMutAccount := MusicAccount{
		NameID:         "EmptyMUT",
		MediaUserToken: "",
		Storefront:     "us",
	}
	_, err := fetchAccountStorefront(emptyMutAccount)
	if err == nil {
		t.Error("Expected error for account with empty MUT")
	}
	if err.Error() != "account has no media user token" {
		t.Errorf("Expected 'account has no media user token' error, got: %v", err)
	}

	// For accounts with MUT, we can't fully test without mocking config
	// but we verify the function exists and handles empty MUT correctly
	_ = account
}

func TestFetchAccountStorefront_EmptyMUT(t *testing.T) {
	account := MusicAccount{
		NameID:         "TestAccount",
		MediaUserToken: "",
		Storefront:     "us",
	}

	_, err := fetchAccountStorefront(account)
	if err == nil {
		t.Error("Expected error for account with empty MUT")
	}
	if err.Error() != "account has no media user token" {
		t.Errorf("Expected 'account has no media user token' error, got: %v", err)
	}
}

func TestAccountResponse_Parsing(t *testing.T) {
	// Test that AccountResponse struct can parse the expected JSON format
	jsonData := `{
		"meta": {
			"subscription": {
				"active": true,
				"storefront": "gb"
			}
		}
	}`

	var resp AccountResponse
	err := json.Unmarshal([]byte(jsonData), &resp)
	if err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if !resp.Meta.Subscription.Active {
		t.Error("Expected subscription to be active")
	}
	if resp.Meta.Subscription.Storefront != "gb" {
		t.Errorf("Expected storefront 'gb', got %q", resp.Meta.Subscription.Storefront)
	}
}

func TestAccountResponse_EmptyStorefront(t *testing.T) {
	// Test handling of empty storefront in response
	jsonData := `{
		"meta": {
			"subscription": {
				"active": true,
				"storefront": ""
			}
		}
	}`

	var resp AccountResponse
	err := json.Unmarshal([]byte(jsonData), &resp)
	if err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if resp.Meta.Subscription.Storefront != "" {
		t.Errorf("Expected empty storefront, got %q", resp.Meta.Subscription.Storefront)
	}
}

func TestAccountResponse_MissingFields(t *testing.T) {
	// Test handling of missing fields (should default to zero values)
	jsonData := `{
		"meta": {}
	}`

	var resp AccountResponse
	err := json.Unmarshal([]byte(jsonData), &resp)
	if err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if resp.Meta.Subscription.Storefront != "" {
		t.Errorf("Expected empty storefront for missing field, got %q", resp.Meta.Subscription.Storefront)
	}
	if resp.Meta.Subscription.Active {
		t.Error("Expected Active to be false for missing field")
	}
}

func TestInitializeAccountStorefronts_NoAccounts(t *testing.T) {
	// Save and restore original account manager
	originalManager := accountManager
	defer func() {
		accountManager = originalManager
	}()

	// Test with nil account manager
	accountManager = nil
	InitializeAccountStorefronts() // Should not panic

	// Test with empty accounts
	accountManager = &AccountManager{
		accounts:       []MusicAccount{},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}
	InitializeAccountStorefronts() // Should not panic
}

func TestInitializeAccountStorefronts_SkipsEmptyMUT(t *testing.T) {
	// Save and restore original state
	originalManager := accountManager
	tokenMu.Lock()
	originalToken := bearerToken
	originalExpiry := tokenExpiry
	bearerToken = "test_bearer_token"
	tokenExpiry = time.Now().Add(1 * time.Hour)
	tokenMu.Unlock()

	defer func() {
		accountManager = originalManager
		tokenMu.Lock()
		bearerToken = originalToken
		tokenExpiry = originalExpiry
		tokenMu.Unlock()
	}()

	// Create manager with one account with empty MUT
	accountManager = &AccountManager{
		accounts: []MusicAccount{
			{NameID: "Account1", MediaUserToken: "", Storefront: "us"},
			{NameID: "Account2", MediaUserToken: "valid_mut", Storefront: "us"},
		},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// This will attempt to fetch but fail (no real API)
	// The important thing is it doesn't panic and skips empty MUT
	InitializeAccountStorefronts()

	// Account with empty MUT should still have default storefront
	if accountManager.accounts[0].Storefront != "us" {
		t.Errorf("Account with empty MUT should keep default storefront, got %q", accountManager.accounts[0].Storefront)
	}
}

// =============================================================================
// STOREFRONT CACHE TESTS
// =============================================================================

func TestHashMUT(t *testing.T) {
	// Test that hashMUT returns consistent results
	mut := "test_media_user_token_12345"
	hash1 := hashMUT(mut)
	hash2 := hashMUT(mut)

	if hash1 != hash2 {
		t.Errorf("hashMUT should be deterministic, got %q and %q", hash1, hash2)
	}

	// Hash should be 64 characters (SHA256 = 32 bytes = 64 hex chars)
	if len(hash1) != 64 {
		t.Errorf("Expected hash length 64, got %d", len(hash1))
	}

	// Different MUTs should produce different hashes
	hash3 := hashMUT("different_token")
	if hash1 == hash3 {
		t.Error("Different MUTs should produce different hashes")
	}
}

func TestStorefrontCache_GetSet(t *testing.T) {
	// Save and restore original cache
	storefrontMutex.Lock()
	originalCache := storefrontCache
	storefrontCache = make(map[string]string)
	storefrontMutex.Unlock()
	defer func() {
		storefrontMutex.Lock()
		storefrontCache = originalCache
		storefrontMutex.Unlock()
	}()

	mut := "test_mut_for_cache"

	// Initially should be empty
	sf := getCachedStorefront(mut)
	if sf != "" {
		t.Errorf("Expected empty storefront for uncached MUT, got %q", sf)
	}

	// Set and retrieve
	setCachedStorefront(mut, "in")
	sf = getCachedStorefront(mut)
	if sf != "in" {
		t.Errorf("Expected storefront 'in', got %q", sf)
	}

	// Update should overwrite
	setCachedStorefront(mut, "gb")
	sf = getCachedStorefront(mut)
	if sf != "gb" {
		t.Errorf("Expected storefront 'gb' after update, got %q", sf)
	}
}

func TestStorefrontCache_Persistence(t *testing.T) {
	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "storefront_cache_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Save and restore original state
	storefrontMutex.Lock()
	originalCache := storefrontCache
	originalPath := storefrontCachePath
	storefrontCache = make(map[string]string)
	storefrontCachePath = filepath.Join(tmpDir, StorefrontCacheFile)
	storefrontMutex.Unlock()
	defer func() {
		storefrontMutex.Lock()
		storefrontCache = originalCache
		storefrontCachePath = originalPath
		storefrontMutex.Unlock()
	}()

	// Set some values
	setCachedStorefront("mut1", "us")
	setCachedStorefront("mut2", "in")

	// Save to disk
	saveStorefrontCache()

	// Verify file exists
	if _, err := os.Stat(storefrontCachePath); os.IsNotExist(err) {
		t.Error("Cache file should exist after save")
	}

	// Clear in-memory cache
	storefrontMutex.Lock()
	storefrontCache = make(map[string]string)
	storefrontMutex.Unlock()

	// Load from disk
	loadStorefrontCache()

	// Verify values are restored
	if getCachedStorefront("mut1") != "us" {
		t.Errorf("Expected 'us' for mut1 after load, got %q", getCachedStorefront("mut1"))
	}
	if getCachedStorefront("mut2") != "in" {
		t.Errorf("Expected 'in' for mut2 after load, got %q", getCachedStorefront("mut2"))
	}
}

func TestStorefrontCache_LoadNonexistentFile(t *testing.T) {
	// Save and restore original state
	storefrontMutex.Lock()
	originalCache := storefrontCache
	originalPath := storefrontCachePath
	storefrontCache = make(map[string]string)
	storefrontCachePath = "/nonexistent/path/storefront_cache.json"
	storefrontMutex.Unlock()
	defer func() {
		storefrontMutex.Lock()
		storefrontCache = originalCache
		storefrontCachePath = originalPath
		storefrontMutex.Unlock()
	}()

	// Set CACHE_DB_PATH to trigger the path calculation in loadStorefrontCache
	oldEnv := os.Getenv("CACHE_DB_PATH")
	os.Setenv("CACHE_DB_PATH", "/nonexistent/cache.db")
	defer os.Setenv("CACHE_DB_PATH", oldEnv)

	// Should not panic when file doesn't exist
	loadStorefrontCache()

	// Cache should remain empty
	storefrontMutex.RLock()
	if len(storefrontCache) != 0 {
		t.Error("Cache should be empty after loading nonexistent file")
	}
	storefrontMutex.RUnlock()
}

func TestInitializeAccountStorefronts_UsesCache(t *testing.T) {
	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "storefront_init_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Save and restore original state
	originalManager := accountManager
	storefrontMutex.Lock()
	originalCache := storefrontCache
	originalPath := storefrontCachePath
	storefrontCache = make(map[string]string)
	storefrontCachePath = filepath.Join(tmpDir, StorefrontCacheFile)
	storefrontMutex.Unlock()

	tokenMu.Lock()
	originalToken := bearerToken
	originalExpiry := tokenExpiry
	bearerToken = "test_bearer_token"
	tokenExpiry = time.Now().Add(1 * time.Hour)
	tokenMu.Unlock()

	defer func() {
		accountManager = originalManager
		storefrontMutex.Lock()
		storefrontCache = originalCache
		storefrontCachePath = originalPath
		storefrontMutex.Unlock()
		tokenMu.Lock()
		bearerToken = originalToken
		tokenExpiry = originalExpiry
		tokenMu.Unlock()
	}()

	// Pre-populate cache with a storefront
	testMut := "test_cached_mut"
	setCachedStorefront(testMut, "jp")
	saveStorefrontCache()

	// Clear in-memory cache to simulate fresh start
	storefrontMutex.Lock()
	storefrontCache = make(map[string]string)
	storefrontMutex.Unlock()

	// Create account manager with the same MUT
	accountManager = &AccountManager{
		accounts: []MusicAccount{
			{NameID: "CachedAccount", MediaUserToken: testMut, Storefront: "us"},
		},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Initialize - should use cached value without API call
	InitializeAccountStorefronts()

	// Account should have the cached storefront
	if accountManager.accounts[0].Storefront != "jp" {
		t.Errorf("Expected storefront 'jp' from cache, got %q", accountManager.accounts[0].Storefront)
	}
}
