package ttml

import (
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
)

const (
	// HealthCheckSongID is a known song ID that has lyrics - used as health check canary
	// "Viva la Vida" by Coldplay - a popular song that should always have lyrics
	HealthCheckSongID = "1065973704"

	// HealthCheckInterval is the time between health checks
	HealthCheckInterval = 24 * time.Hour
)

// MUTHealthStatus holds the health status of a single account's MUT
type MUTHealthStatus struct {
	AccountName string    `json:"account_name"`
	Healthy     bool      `json:"healthy"`
	LastChecked time.Time `json:"last_checked"`
	LastError   string    `json:"last_error,omitempty"`
}

var (
	healthStatuses = make(map[string]*MUTHealthStatus)
	healthMu       sync.RWMutex
)

// CheckMUTHealth tests a single account's MUT against the canary song
func CheckMUTHealth(account MusicAccount) *MUTHealthStatus {
	status := &MUTHealthStatus{
		AccountName: account.NameID,
		LastChecked: time.Now(),
	}

	// Attempt to fetch lyrics for canary song
	_, err := fetchLyricsTTML(HealthCheckSongID, account.Storefront, account)

	if err == nil {
		status.Healthy = true
		log.Debugf("%s Account %s: healthy", logcolors.LogHealthCheck, logcolors.Account(account.NameID))
	} else {
		status.Healthy = false
		status.LastError = err.Error()
		log.Warnf("%s Account %s: unhealthy - %v", logcolors.LogHealthCheck, logcolors.Account(account.NameID), err)
	}

	healthMu.Lock()
	healthStatuses[account.NameID] = status
	healthMu.Unlock()

	return status
}

// CheckAllMUTHealth runs health checks on all ACTIVE accounts.
// Skips out-of-service accounts (empty MUT).
func CheckAllMUTHealth() []*MUTHealthStatus {
	if accountManager == nil {
		initAccountManager()
	}

	accounts := accountManager.getAllAccounts()
	results := make([]*MUTHealthStatus, 0, len(accounts))

	for _, account := range accounts {
		// Skip out-of-service accounts (empty MUT)
		if account.MediaUserToken == "" {
			log.Debugf("%s Skipping out-of-service account: %s", logcolors.LogHealthCheck, account.NameID)
			continue
		}
		status := CheckMUTHealth(account)
		results = append(results, status)
	}

	return results
}

// GetHealthStatuses returns current health status of all MUTs
func GetHealthStatuses() map[string]*MUTHealthStatus {
	healthMu.RLock()
	defer healthMu.RUnlock()

	// Return a copy to avoid race conditions
	copy := make(map[string]*MUTHealthStatus)
	for k, v := range healthStatuses {
		statusCopy := *v
		copy[k] = &statusCopy
	}
	return copy
}

// StartHealthCheckScheduler runs health checks daily
func StartHealthCheckScheduler() {
	// Run immediately on startup
	go runHealthCheck()

	// Schedule daily checks
	ticker := time.NewTicker(HealthCheckInterval)
	go func() {
		for range ticker.C {
			runHealthCheck()
		}
	}()
}

func runHealthCheck() {
	log.Infof("%s Starting MUT health check...", logcolors.LogHealthCheck)

	results := CheckAllMUTHealth()

	healthy := 0
	var unhealthy []*MUTHealthStatus

	for _, status := range results {
		if status.Healthy {
			healthy++
		} else {
			unhealthy = append(unhealthy, status)
		}
	}

	log.Infof("%s Health check complete: %d healthy, %d unhealthy",
		logcolors.LogHealthCheck, healthy, len(unhealthy))

	// Emit notification if any unhealthy MUTs detected
	if len(unhealthy) > 0 {
		notifier.PublishMUTHealthCheckFailed(unhealthy)
	}
}

// getAllAccounts returns all accounts from the manager (for health checks)
func (m *AccountManager) getAllAccounts() []MusicAccount {
	if m == nil {
		return nil
	}
	return m.accounts
}
