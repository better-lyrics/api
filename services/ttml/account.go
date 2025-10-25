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
			NameID:         "Primary",
			BearerToken:    conf.Configuration.TTMLBearerToken,
			MediaUserToken: conf.Configuration.TTMLMediaUserToken,
			Storefront:     conf.Configuration.TTMLStorefront,
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
