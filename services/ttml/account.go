package ttml

import (
	"lyrics-api-go/config"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
)

var accountManager *AccountManager

func initAccountManager() {
	conf := config.Get()
	configAccounts, err := conf.GetTTMLAccounts()
	if err != nil {
		log.Fatalf("Failed to initialize TTML accounts: %v", err)
	}

	if len(configAccounts) == 0 {
		log.Warn("No TTML accounts configured")
		accountManager = &AccountManager{
			accounts:     []MusicAccount{},
			currentIndex: 0,
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
		accounts:     accounts,
		currentIndex: 0,
	}

	log.Infof("Initialized %d TTML account(s) with round-robin load balancing", len(accounts))
}

// getNextAccount returns the next account in round-robin fashion (thread-safe)
// This distributes requests evenly across all accounts
func (m *AccountManager) getNextAccount() MusicAccount {
	if len(m.accounts) == 0 {
		return MusicAccount{}
	}
	// Atomically increment and get the index
	idx := atomic.AddUint64(&m.currentIndex, 1) - 1
	return m.accounts[idx%uint64(len(m.accounts))]
}

// getCurrentAccount returns the current account without rotating (for retries on same account)
func (m *AccountManager) getCurrentAccount() MusicAccount {
	if len(m.accounts) == 0 {
		return MusicAccount{}
	}
	idx := atomic.LoadUint64(&m.currentIndex)
	// Subtract 1 because getNextAccount increments before returning
	if idx == 0 {
		return m.accounts[0]
	}
	return m.accounts[(idx-1)%uint64(len(m.accounts))]
}

func (m *AccountManager) hasAccounts() bool {
	return len(m.accounts) > 0
}

func (m *AccountManager) accountCount() int {
	return len(m.accounts)
}

// skipCurrentAccount advances to the next account (used when current account fails)
// This is called on 401/429 errors to try a different account
func (m *AccountManager) skipCurrentAccount() {
	if len(m.accounts) <= 1 {
		return // No other accounts to try
	}
	atomic.AddUint64(&m.currentIndex, 1)
	idx := atomic.LoadUint64(&m.currentIndex)
	log.Warnf("Skipping to next TTML API account: %s", m.accounts[(idx-1)%uint64(len(m.accounts))].NameID)
}
