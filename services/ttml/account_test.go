package ttml

import (
	"sync"
	"testing"
)

func TestAccountManager_GetNextAccount_RoundRobin(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
		{NameID: "Account3", BearerToken: "token3"},
	}

	manager := &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
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

func TestAccountManager_GetCurrentAccount(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
	}

	manager := &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}

	// Before any getNextAccount, getCurrentAccount should return first account
	current := manager.getCurrentAccount()
	if current.NameID != "Account1" {
		t.Errorf("Expected Account1 initially, got %q", current.NameID)
	}

	// After getNextAccount, getCurrentAccount should return the same account
	next := manager.getNextAccount()
	current = manager.getCurrentAccount()
	if current.NameID != next.NameID {
		t.Errorf("getCurrentAccount should return same as last getNextAccount, got %q vs %q", current.NameID, next.NameID)
	}
}

func TestAccountManager_SkipCurrentAccount(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
		{NameID: "Account3", BearerToken: "token3"},
	}

	manager := &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}

	// Get first account
	first := manager.getNextAccount()
	if first.NameID != "Account1" {
		t.Errorf("Expected Account1, got %q", first.NameID)
	}

	// Skip to next (simulating a 429 error)
	manager.skipCurrentAccount()

	// Next getNextAccount should give Account3 (skipped Account2)
	next := manager.getNextAccount()
	if next.NameID != "Account3" {
		t.Errorf("Expected Account3 after skip, got %q", next.NameID)
	}
}

func TestAccountManager_SingleAccount(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "OnlyAccount", BearerToken: "token"},
	}

	manager := &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}

	// With single account, should always return the same account
	for i := 0; i < 5; i++ {
		acc := manager.getNextAccount()
		if acc.NameID != "OnlyAccount" {
			t.Errorf("Iteration %d: expected OnlyAccount, got %q", i, acc.NameID)
		}
	}

	// Skip should be no-op with single account
	manager.skipCurrentAccount()
	acc := manager.getNextAccount()
	if acc.NameID != "OnlyAccount" {
		t.Errorf("Expected OnlyAccount after skip, got %q", acc.NameID)
	}
}

func TestAccountManager_EmptyAccounts(t *testing.T) {
	manager := &AccountManager{
		accounts:     []MusicAccount{},
		currentIndex: 0,
	}

	// Should return empty account without panicking
	acc := manager.getNextAccount()
	if acc.NameID != "" {
		t.Errorf("Expected empty account, got %q", acc.NameID)
	}

	if manager.hasAccounts() {
		t.Error("hasAccounts should return false for empty manager")
	}

	// Skip should not panic
	manager.skipCurrentAccount()
}

func TestAccountManager_HasAccounts(t *testing.T) {
	emptyManager := &AccountManager{accounts: []MusicAccount{}}
	if emptyManager.hasAccounts() {
		t.Error("hasAccounts should return false for empty manager")
	}

	manager := &AccountManager{
		accounts: []MusicAccount{{NameID: "Account1"}},
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
		accounts:     accounts,
		currentIndex: 0,
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
