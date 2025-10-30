package ttml

import "testing"

func TestAccountManager_GetCurrentAccount(t *testing.T) {
	accounts := []MusicAccount{
		{
			NameID:         "Account1",
			BearerToken:    "token1",
			MediaUserToken: "media1",
			Storefront:     "us",
		},
		{
			NameID:         "Account2",
			BearerToken:    "token2",
			MediaUserToken: "media2",
			Storefront:     "jp",
		},
	}

	manager := &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}

	current := manager.getCurrentAccount()
	if current.NameID != "Account1" {
		t.Errorf("Expected current account 'Account1', got %q", current.NameID)
	}
	if current.BearerToken != "token1" {
		t.Errorf("Expected bearer token 'token1', got %q", current.BearerToken)
	}
}

func TestAccountManager_SwitchToNextAccount(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", BearerToken: "token1"},
		{NameID: "Account2", BearerToken: "token2"},
		{NameID: "Account3", BearerToken: "token3"},
	}

	manager := &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}

	// Initially on Account1
	if manager.getCurrentAccount().NameID != "Account1" {
		t.Errorf("Expected Account1 initially")
	}

	// Switch to Account2
	manager.switchToNextAccount()
	if manager.getCurrentAccount().NameID != "Account2" {
		t.Errorf("Expected Account2 after first switch")
	}

	// Switch to Account3
	manager.switchToNextAccount()
	if manager.getCurrentAccount().NameID != "Account3" {
		t.Errorf("Expected Account3 after second switch")
	}

	// Should wrap around to Account1
	manager.switchToNextAccount()
	if manager.getCurrentAccount().NameID != "Account1" {
		t.Errorf("Expected Account1 after wrapping around")
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

	// Should stay on the same account
	initial := manager.getCurrentAccount()
	manager.switchToNextAccount()
	afterSwitch := manager.getCurrentAccount()

	if initial.NameID != afterSwitch.NameID {
		t.Errorf("Expected to stay on same account with single account configuration")
	}
}

func TestAccountManager_MultipleAccountRotation(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1"},
		{NameID: "Account2"},
	}

	manager := &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}

	// Test multiple rotations
	expectedSequence := []string{
		"Account1", // Initial
		"Account2", // After 1st switch
		"Account1", // After 2nd switch (wrapped)
		"Account2", // After 3rd switch
		"Account1", // After 4th switch (wrapped again)
	}

	for i, expected := range expectedSequence {
		current := manager.getCurrentAccount()
		if current.NameID != expected {
			t.Errorf("Iteration %d: expected %q, got %q", i, expected, current.NameID)
		}
		if i < len(expectedSequence)-1 {
			manager.switchToNextAccount()
		}
	}
}

func TestAccountManager_CurrentIndexBounds(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1"},
		{NameID: "Account2"},
		{NameID: "Account3"},
	}

	manager := &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}

	// Perform many switches to ensure index stays in bounds
	for i := 0; i < 100; i++ {
		manager.switchToNextAccount()
		if manager.currentIndex < 0 || manager.currentIndex >= len(accounts) {
			t.Fatalf("Index out of bounds after %d switches: %d", i+1, manager.currentIndex)
		}
	}

	// Verify index is still valid
	current := manager.getCurrentAccount()
	if current.NameID == "" {
		t.Error("Got empty account after many switches")
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
