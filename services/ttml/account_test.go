package ttml

import (
	"sync"
	"testing"
	"time"
)

func TestAccountManager_GetNextAccount_RoundRobin(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
		{NameID: "Account3", BearerToken: "token3"},
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
		{NameID: "OnlyAccount", BearerToken: "token"},
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
		BearerToken:    "test_bearer_token_123",
		MediaUserToken: "test_media_token_456",
		Storefront:     "us",
	}

	if account.NameID != "TestAccount" {
		t.Errorf("Expected NameID 'TestAccount', got %q", account.NameID)
	}
	if account.BearerToken != "test_bearer_token_123" {
		t.Errorf("Expected BearerToken 'test_bearer_token_123', got %q", account.BearerToken)
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
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
		{NameID: "Account3", BearerToken: "token3"},
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
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
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
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
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
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
		{NameID: "Account3", BearerToken: "token3"},
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
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
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
	} else if remaining <= 0 || remaining > 60 {
		t.Errorf("Expected remaining time between 0-60 seconds, got %d", remaining)
	}
}

func TestAccountManager_IsQuarantined(t *testing.T) {
	manager := &AccountManager{
		accounts: []MusicAccount{
			{NameID: "Account1", BearerToken: "token1"},
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
