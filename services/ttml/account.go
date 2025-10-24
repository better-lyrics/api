package ttml

import (
	"lyrics-api-go/config"

	log "github.com/sirupsen/logrus"
)

var accountManager *AccountManager

func initAccountManager() {
	conf := config.Get()
	accounts := []MusicAccount{
		{
			NameID:           "Primary",
			AuthType:         conf.Configuration.TTMLAuthType,
			AndroidAuthToken: conf.Configuration.TTMLAndroidToken,
			AndroidDSID:      conf.Configuration.TTMLAndroidDSID,
			AndroidUserAgent: conf.Configuration.TTMLAndroidUserAgent,
			AndroidCookie:    conf.Configuration.TTMLAndroidCookie,
			Storefront:       conf.Configuration.TTMLStorefront,
			MusicAuthToken:   conf.Configuration.TTMLWebToken,
		},
	}

	accountManager = &AccountManager{
		accounts:     accounts,
		currentIndex: 0,
	}
}

func (m *AccountManager) getCurrentAccount() MusicAccount {
	return m.accounts[m.currentIndex]
}

func (m *AccountManager) switchToNextAccount() {
	m.currentIndex = (m.currentIndex + 1) % len(m.accounts)
	log.Warnf("Switched to TTML API account: %s", m.accounts[m.currentIndex].NameID)
}
