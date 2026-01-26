package ttml

import (
	"strings"
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

// CheckMUTHealth tests a single account's MUT against the canary song.
// Only 404 errors are considered "unhealthy" (stale MUT) - the canary song definitely
// has lyrics, so 404 means the MUT can't access them (stale/expired).
// 429 is handled by quarantine, 401 is a bearer token issue (separate system).
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
		status.LastError = err.Error()

		// 404 on canary song = stale MUT (song definitely has lyrics, MUT can't access them)
		if strings.Contains(err.Error(), "404") {
			status.Healthy = false
			log.Warnf("%s Account %s: STALE MUT (404 on canary) - %v", logcolors.LogHealthCheck, logcolors.Account(account.NameID), err)

			// Permanently disable this account
			accountManager.DisableAccount(account)
		} else {
			// 429, 401, network errors don't mean the MUT is stale
			status.Healthy = true
			log.Debugf("%s Account %s: transient error (not stale) - %v", logcolors.LogHealthCheck, logcolors.Account(account.NameID), err)
		}
	}

	healthMu.Lock()
	healthStatuses[account.NameID] = status
	healthMu.Unlock()

	return status
}

// CheckAllMUTHealth runs health checks on all ACTIVE accounts.
// Skips out-of-service accounts (empty MUT), quarantined accounts (rate limited),
// and already disabled accounts (stale MUT detected previously).
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

		// Skip quarantined accounts (rate limited, not stale)
		if accountManager.IsAccountQuarantinedByName(account.NameID) {
			log.Debugf("%s Skipping quarantined account: %s", logcolors.LogHealthCheck, account.NameID)
			continue
		}

		// Skip already disabled accounts (stale MUT detected previously)
		if accountManager.IsAccountDisabled(account.NameID) {
			log.Debugf("%s Skipping disabled account: %s", logcolors.LogHealthCheck, account.NameID)
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
	result := make(map[string]*MUTHealthStatus, len(healthStatuses))
	for k, v := range healthStatuses {
		statusCopy := *v
		result[k] = &statusCopy
	}
	return result
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

	var healthy int
	var staleMUTs []*MUTHealthStatus

	for _, status := range results {
		if status.Healthy {
			healthy++
		} else {
			// Only 404s (stale MUT) are marked unhealthy by CheckMUTHealth
			staleMUTs = append(staleMUTs, status)
		}
	}

	log.Infof("%s Health check complete: %d healthy, %d stale MUTs (404)",
		logcolors.LogHealthCheck, healthy, len(staleMUTs))

	if len(staleMUTs) == 0 {
		return
	}

	// Convert to simplified format for notifier
	unhealthyData := make([]map[string]string, 0, len(staleMUTs))
	for _, status := range staleMUTs {
		unhealthyData = append(unhealthyData, map[string]string{
			"name":  status.AccountName,
			"error": status.LastError,
		})
	}
	notifier.PublishMUTHealthCheckFailed(unhealthyData)
}

// getAllAccounts returns all accounts from the manager (for health checks)
func (m *AccountManager) getAllAccounts() []MusicAccount {
	if m == nil {
		return nil
	}
	return m.accounts
}
