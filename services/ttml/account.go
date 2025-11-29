package ttml

import (
	"lyrics-api-go/config"
	"lyrics-api-go/logcolors"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// QuarantineDuration is how long an account is quarantined after a 429
	QuarantineDuration = 5 * time.Minute
)

var (
	accountManager  *AccountManager
	quarantineMutex sync.RWMutex // Protects quarantineTime map
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
			BearerToken:    acc.BearerToken,
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

// getNextAccount returns the next non-quarantined account in round-robin fashion (thread-safe)
// If all accounts are quarantined, returns the one with the shortest remaining quarantine
func (m *AccountManager) getNextAccount() MusicAccount {
	if len(m.accounts) == 0 {
		return MusicAccount{}
	}

	now := time.Now().Unix()
	numAccounts := len(m.accounts)

	// Try to find a non-quarantined account
	for i := 0; i < numAccounts; i++ {
		idx := atomic.AddUint64(&m.currentIndex, 1) - 1
		accountIdx := int(idx % uint64(numAccounts))

		if !m.isQuarantined(accountIdx, now) {
			return m.accounts[accountIdx]
		}
		log.Debugf("%s Skipping %s (quarantined)", logcolors.LogQuarantine, m.accounts[accountIdx].NameID)
	}

	// All accounts quarantined - find the one with shortest remaining time
	shortestIdx := 0
	shortestTime := int64(^uint64(0) >> 1) // Max int64

	quarantineMutex.RLock()
	for i := 0; i < numAccounts; i++ {
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

	if shortestTime > 0 {
		log.Warnf("%s All accounts quarantined! Using %s (shortest wait: %ds)",
			logcolors.LogQuarantine, m.accounts[shortestIdx].NameID, shortestTime)
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
		log.Warnf("%s Could not find account %s to quarantine", logcolors.LogQuarantine, account.NameID)
		return
	}

	quarantineMutex.Lock()
	m.quarantineTime[accountIdx] = time.Now().Add(QuarantineDuration).Unix()
	quarantineMutex.Unlock()

	log.Warnf("%s Account %s quarantined for %v due to rate limit", logcolors.LogQuarantine, account.NameID, QuarantineDuration)
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
		log.Infof("%s Account %s quarantine cleared (successful request)", logcolors.LogQuarantine, account.NameID)
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

// availableAccountCount returns the number of non-quarantined accounts
func (m *AccountManager) availableAccountCount() int {
	now := time.Now().Unix()
	count := 0
	for i := range m.accounts {
		if !m.isQuarantined(i, now) {
			count++
		}
	}
	return count
}
