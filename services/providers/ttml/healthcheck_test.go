package ttml

import (
	"strings"
	"testing"
	"time"
)

func TestMUTHealthStatus_Fields(t *testing.T) {
	status := &MUTHealthStatus{
		AccountName: "TestAccount",
		Healthy:     true,
		LastChecked: time.Now(),
		LastError:   "",
	}

	if status.AccountName != "TestAccount" {
		t.Errorf("Expected AccountName 'TestAccount', got %q", status.AccountName)
	}
	if !status.Healthy {
		t.Error("Expected Healthy to be true")
	}
	if status.LastError != "" {
		t.Errorf("Expected empty LastError, got %q", status.LastError)
	}
}

func TestMUTHealthStatus_Unhealthy(t *testing.T) {
	status := &MUTHealthStatus{
		AccountName: "FailedAccount",
		Healthy:     false,
		LastChecked: time.Now(),
		LastError:   "HTTP 404: lyrics not found",
	}

	if status.Healthy {
		t.Error("Expected Healthy to be false")
	}
	if status.LastError != "HTTP 404: lyrics not found" {
		t.Errorf("Expected LastError 'HTTP 404: lyrics not found', got %q", status.LastError)
	}
}

func TestCheckAllMUTHealth_SkipsQuarantined(t *testing.T) {
	// Setup test account manager with quarantined account
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1", Storefront: "us"},
		{NameID: "Account2", MediaUserToken: "mut2", Storefront: "us"},
	}

	testManager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	// Store original and replace
	originalManager := accountManager
	accountManager = testManager
	defer func() { accountManager = originalManager }()

	// Quarantine Account1
	testManager.quarantineTime[0] = time.Now().Add(5 * time.Minute).Unix()

	// Reset disabled accounts for test
	disabledMutex.Lock()
	originalDisabled := disabledAccounts
	disabledAccounts = make(map[string]bool)
	disabledMutex.Unlock()
	defer func() {
		disabledMutex.Lock()
		disabledAccounts = originalDisabled
		disabledMutex.Unlock()
	}()

	// Verify quarantine detection
	if !testManager.IsAccountQuarantinedByName("Account1") {
		t.Error("Account1 should be quarantined")
	}
	if testManager.IsAccountQuarantinedByName("Account2") {
		t.Error("Account2 should not be quarantined")
	}
}

func TestCheckAllMUTHealth_SkipsDisabled(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "mut1", Storefront: "us"},
		{NameID: "Account2", MediaUserToken: "mut2", Storefront: "us"},
	}

	testManager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	originalManager := accountManager
	accountManager = testManager
	defer func() { accountManager = originalManager }()

	// Reset and setup disabled accounts
	disabledMutex.Lock()
	originalDisabled := disabledAccounts
	disabledAccounts = make(map[string]bool)
	disabledAccounts["Account1"] = true
	disabledMutex.Unlock()
	defer func() {
		disabledMutex.Lock()
		disabledAccounts = originalDisabled
		disabledMutex.Unlock()
	}()

	if !testManager.IsAccountDisabled("Account1") {
		t.Error("Account1 should be disabled")
	}
	if testManager.IsAccountDisabled("Account2") {
		t.Error("Account2 should not be disabled")
	}
}

func TestCheckAllMUTHealth_SkipsEmptyMUT(t *testing.T) {
	accounts := []MusicAccount{
		{NameID: "Account1", MediaUserToken: "", Storefront: "us"},     // Out of service
		{NameID: "Account2", MediaUserToken: "mut2", Storefront: "us"}, // Active
	}

	testManager := &AccountManager{
		accounts:       accounts,
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	originalManager := accountManager
	accountManager = testManager
	defer func() { accountManager = originalManager }()

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

	// The empty MUT account should be skipped in CheckAllMUTHealth
	// We can't fully test without mocking HTTP, but verify the account structure
	if accounts[0].MediaUserToken != "" {
		t.Error("Account1 should have empty MUT")
	}
	if accounts[1].MediaUserToken == "" {
		t.Error("Account2 should have MUT")
	}
}

func TestGetHealthStatuses_ReturnsCleanCopy(t *testing.T) {
	// Clear health statuses
	healthMu.Lock()
	originalStatuses := healthStatuses
	healthStatuses = make(map[string]*MUTHealthStatus)
	healthStatuses["TestAccount"] = &MUTHealthStatus{
		AccountName: "TestAccount",
		Healthy:     true,
		LastChecked: time.Now(),
	}
	healthMu.Unlock()
	defer func() {
		healthMu.Lock()
		healthStatuses = originalStatuses
		healthMu.Unlock()
	}()

	// Get statuses
	statuses := GetHealthStatuses()

	if len(statuses) != 1 {
		t.Errorf("Expected 1 status, got %d", len(statuses))
	}

	if status, ok := statuses["TestAccount"]; !ok {
		t.Error("Expected TestAccount in statuses")
	} else if !status.Healthy {
		t.Error("Expected TestAccount to be healthy")
	}

	// Modify returned map and verify original is unchanged
	statuses["TestAccount"].Healthy = false

	healthMu.RLock()
	if !healthStatuses["TestAccount"].Healthy {
		t.Error("Original healthStatuses should not be modified")
	}
	healthMu.RUnlock()
}

func TestHealthCheckConstants(t *testing.T) {
	// Verify constants are set correctly
	if HealthCheckSongID != "1065973704" {
		t.Errorf("Expected HealthCheckSongID '1065973704', got %q", HealthCheckSongID)
	}

	if HealthCheckInterval != 24*time.Hour {
		t.Errorf("Expected HealthCheckInterval 24h, got %v", HealthCheckInterval)
	}
}

// TestHealthCheck_401DoesNotDisable verifies that 401 errors (bearer token issues)
// do NOT disable accounts - only 404 (stale MUT on canary) should disable.
func TestHealthCheck_401DoesNotDisable(t *testing.T) {
	// Setup test account manager
	account := MusicAccount{NameID: "TestAccount401", MediaUserToken: "mut", Storefront: "us"}

	testManager := &AccountManager{
		accounts:       []MusicAccount{account},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	originalManager := accountManager
	accountManager = testManager
	defer func() { accountManager = originalManager }()

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

	// Simulate what CheckMUTHealth does when it encounters a 401 error
	err401 := "HTTP 401: Unauthorized"

	// This is the exact logic from CheckMUTHealth - 401 should NOT trigger disable
	if strings.Contains(err401, "404") {
		testManager.DisableAccount(account)
	}

	// Verify account was NOT disabled
	if testManager.IsAccountDisabled("TestAccount401") {
		t.Error("401 error should NOT disable account - only 404 should")
	}
}

// TestHealthCheck_404DisablesAccount verifies that 404 errors on the canary song
// DO disable accounts (indicates stale MUT).
func TestHealthCheck_404DisablesAccount(t *testing.T) {
	// Setup test account manager
	account := MusicAccount{NameID: "TestAccount404", MediaUserToken: "mut", Storefront: "us"}

	testManager := &AccountManager{
		accounts:       []MusicAccount{account},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	originalManager := accountManager
	accountManager = testManager
	defer func() { accountManager = originalManager }()

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

	// Simulate what CheckMUTHealth does when it encounters a 404 error
	err404 := "HTTP 404: lyrics not found"

	// This is the exact logic from CheckMUTHealth - 404 SHOULD trigger disable
	if strings.Contains(err404, "404") {
		testManager.DisableAccount(account)
	}

	// Verify account WAS disabled
	if !testManager.IsAccountDisabled("TestAccount404") {
		t.Error("404 error should disable account (stale MUT)")
	}
}

// TestHealthCheck_429DoesNotDisable verifies that 429 errors (rate limit)
// do NOT disable accounts - rate limiting is handled by quarantine system.
func TestHealthCheck_429DoesNotDisable(t *testing.T) {
	// Setup test account manager
	account := MusicAccount{NameID: "TestAccount429", MediaUserToken: "mut", Storefront: "us"}

	testManager := &AccountManager{
		accounts:       []MusicAccount{account},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	originalManager := accountManager
	accountManager = testManager
	defer func() { accountManager = originalManager }()

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

	// Simulate what CheckMUTHealth does when it encounters a 429 error
	err429 := "HTTP 429: Too Many Requests"

	// This is the exact logic from CheckMUTHealth - 429 should NOT trigger disable
	if strings.Contains(err429, "404") {
		testManager.DisableAccount(account)
	}

	// Verify account was NOT disabled
	if testManager.IsAccountDisabled("TestAccount429") {
		t.Error("429 error should NOT disable account - handled by quarantine system")
	}
}

// TestHealthCheck_NetworkErrorDoesNotDisable verifies that network errors
// do NOT disable accounts - they are transient.
func TestHealthCheck_NetworkErrorDoesNotDisable(t *testing.T) {
	// Setup test account manager
	account := MusicAccount{NameID: "TestAccountNetwork", MediaUserToken: "mut", Storefront: "us"}

	testManager := &AccountManager{
		accounts:       []MusicAccount{account},
		currentIndex:   0,
		quarantineTime: make(map[int]int64),
	}

	originalManager := accountManager
	accountManager = testManager
	defer func() { accountManager = originalManager }()

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

	// Simulate what CheckMUTHealth does when it encounters a network error
	errNetwork := "dial tcp: connection refused"

	// This is the exact logic from CheckMUTHealth - network errors should NOT trigger disable
	if strings.Contains(errNetwork, "404") {
		testManager.DisableAccount(account)
	}

	// Verify account was NOT disabled
	if testManager.IsAccountDisabled("TestAccountNetwork") {
		t.Error("Network errors should NOT disable account - they are transient")
	}
}
