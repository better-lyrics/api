package notifier

import (
	"encoding/json"
	"fmt"
	"lyrics-api-go/logcolors"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

// TokenInfo holds information about a single token for monitoring
// Now tracks Media User Tokens (MUTs) since bearer is auto-scraped
type TokenInfo struct {
	Name           string // Account name (e.g., "Account-1")
	MediaUserToken string // MUT to monitor for expiration
}

// MonitorConfig holds the configuration for the token monitor
type MonitorConfig struct {
	Tokens           []TokenInfo // Multiple tokens to monitor
	WarningThreshold int         // Days before expiration to start warning
	ReminderInterval int         // Hours between reminders (to avoid spam)
	StateFile        string
	Notifiers        []Notifier
}

// MonitorState tracks when we last sent notifications
type MonitorState struct {
	LastNotificationSent time.Time `json:"last_notification_sent"`
	LastDaysRemaining    int       `json:"last_days_remaining"`
}

// TokenMonitor monitors token expiration and sends notifications
type TokenMonitor struct {
	config MonitorConfig
	state  MonitorState
}

// NewTokenMonitor creates a new token monitor
func NewTokenMonitor(config MonitorConfig) *TokenMonitor {
	monitor := &TokenMonitor{
		config: config,
		state:  MonitorState{},
	}

	// Load previous state if exists
	monitor.loadState()

	return monitor
}

// loadState loads the last notification state from disk
func (m *TokenMonitor) loadState() {
	if m.config.StateFile == "" {
		return
	}

	data, err := os.ReadFile(m.config.StateFile)
	if err != nil {
		// File doesn't exist yet, that's okay
		return
	}

	if err := json.Unmarshal(data, &m.state); err != nil {
		log.Warnf("%s Failed to load state file: %v", logcolors.LogTokenMonitor, err)
	}
}

// saveState saves the current notification state to disk
func (m *TokenMonitor) saveState() {
	if m.config.StateFile == "" {
		return
	}

	data, err := json.Marshal(m.state)
	if err != nil {
		log.Errorf("%s Failed to marshal state: %v", logcolors.LogTokenMonitor, err)
		return
	}

	if err := os.WriteFile(m.config.StateFile, data, 0644); err != nil {
		log.Errorf("%s Failed to write state file: %v", logcolors.LogTokenMonitor, err)
	}
}

// shouldSendNotification determines if we should send a notification based on:
// 1. Days remaining has changed
// 2. Enough time has passed since last notification (to avoid spam)
func (m *TokenMonitor) shouldSendNotification(daysRemaining int) bool {
	now := time.Now()

	// If days remaining changed, we should notify
	if daysRemaining != m.state.LastDaysRemaining {
		return true
	}

	// Check if enough time has passed since last notification
	hoursSinceLastNotification := now.Sub(m.state.LastNotificationSent).Hours()
	if hoursSinceLastNotification >= float64(m.config.ReminderInterval) {
		return true
	}

	return false
}

// TokenStatus holds the status of a single token check
type TokenStatus struct {
	Name          string
	ExpiringSoon  bool
	DaysRemaining int
	Error         error
}

// Check performs a single check of all token expirations
func (m *TokenMonitor) Check() error {
	if len(m.config.Tokens) == 0 {
		return fmt.Errorf("no tokens configured for monitoring")
	}

	var expiringTokens []TokenStatus
	minDaysRemaining := 999999

	// Check all MUT tokens
	for _, token := range m.config.Tokens {
		expiringSoon, daysRemaining, err := IsExpiringSoon(token.MediaUserToken, m.config.WarningThreshold)
		status := TokenStatus{
			Name:          token.Name,
			ExpiringSoon:  expiringSoon,
			DaysRemaining: daysRemaining,
			Error:         err,
		}

		if err != nil {
			log.Warnf("%s Failed to check %s: %v", logcolors.LogTokenMonitor, token.Name, err)
			continue
		}

		log.Debugf("%s %s: expiring_soon=%v, days_remaining=%d", logcolors.LogTokenMonitor, token.Name, expiringSoon, daysRemaining)

		if expiringSoon {
			expiringTokens = append(expiringTokens, status)
			if daysRemaining < minDaysRemaining {
				minDaysRemaining = daysRemaining
			}
		}
	}

	// If no tokens are expiring soon, nothing to do
	if len(expiringTokens) == 0 {
		log.Debugf("%s All tokens are healthy", logcolors.LogTokenMonitor)
		return nil
	}

	// Check if we should send notification (based on the most urgent token)
	if !m.shouldSendNotification(minDaysRemaining) {
		log.Debugf("%s Skipping notification (too soon since last notification)", logcolors.LogTokenMonitor)
		return nil
	}

	// Send notifications for all expiring tokens
	if err := m.sendNotifications(expiringTokens); err != nil {
		return fmt.Errorf("failed to send notifications: %v", err)
	}

	// Update state
	m.state.LastNotificationSent = time.Now()
	m.state.LastDaysRemaining = minDaysRemaining
	m.saveState()

	return nil
}

// sendNotifications sends notifications through all configured notifiers
func (m *TokenMonitor) sendNotifications(expiringTokens []TokenStatus) error {
	var subject, message string

	// Build token details
	var tokenDetails string
	for _, t := range expiringTokens {
		if t.DaysRemaining <= 0 {
			tokenDetails += fmt.Sprintf("  • %s: EXPIRED\n", t.Name)
		} else if t.DaysRemaining == 1 {
			tokenDetails += fmt.Sprintf("  • %s: expires tomorrow\n", t.Name)
		} else {
			tokenDetails += fmt.Sprintf("  • %s: %d days remaining\n", t.Name, t.DaysRemaining)
		}
	}

	// Find the most urgent status
	minDays := expiringTokens[0].DaysRemaining
	for _, t := range expiringTokens {
		if t.DaysRemaining < minDays {
			minDays = t.DaysRemaining
		}
	}

	tokenWord := "token"
	if len(expiringTokens) > 1 {
		tokenWord = "tokens"
	}

	if minDays <= 0 {
		subject = fmt.Sprintf("🚨 URGENT: MUT %s EXPIRED", tokenWord)
		message = fmt.Sprintf("🚨 MEDIA USER TOKEN(S) EXPIRED\n\n"+
			"The following %s have EXPIRED:\n\n%s\n"+
			"⚠️ Action Required:\n\n"+
			"The service will stop working until you update the tokens.\n\n"+
			"Update TTML_MEDIA_USER_TOKENS in your environment and restart the service immediately.",
			tokenWord, tokenDetails)
	} else if minDays == 1 {
		subject = fmt.Sprintf("⚠️ Alert: MUT %s Expires Tomorrow", tokenWord)
		message = fmt.Sprintf("⚠️ MEDIA USER TOKEN EXPIRATION WARNING\n\n"+
			"The following %s need attention:\n\n%s\n"+
			"📝 Action Required:\n\n"+
			"Update TTML_MEDIA_USER_TOKENS in your environment soon to avoid service interruption.",
			tokenWord, tokenDetails)
	} else {
		subject = fmt.Sprintf("⏰ Notice: MUT %s Expiring Soon", tokenWord)
		message = fmt.Sprintf(
			"⏰ MEDIA USER TOKEN EXPIRATION NOTICE\n\n"+
				"The following %s need attention:\n\n%s\n"+
				"📝 Action Required:\n\n"+
				"Update TTML_MEDIA_USER_TOKENS in your environment before expiration to maintain service availability.\n\n"+
				"You will receive daily reminders until the tokens are updated.",
			tokenWord, tokenDetails)
	}

	log.Infof("%s Sending notifications: %s", logcolors.LogNotifier, subject)

	var lastErr error
	successCount := 0

	for _, notifier := range m.config.Notifiers {
		if err := notifier.Send(subject, message); err != nil {
			log.Errorf("%s Notifier failed: %v", logcolors.LogNotifier, err)
			lastErr = err
		} else {
			successCount++
		}
	}

	if successCount == 0 && lastErr != nil {
		return fmt.Errorf("all notifiers failed, last error: %v", lastErr)
	}

	log.Infof("%s Successfully sent %d/%d notifications", logcolors.LogNotifier, successCount, len(m.config.Notifiers))
	return nil
}

// Run starts the monitor in a loop, checking at the specified interval
func (m *TokenMonitor) Run(checkInterval time.Duration) {
	log.Infof("%s Starting (tokens: %d, check interval: %v, warning threshold: %d days, reminder interval: %d hours)",
		logcolors.LogTokenMonitor, len(m.config.Tokens), checkInterval, m.config.WarningThreshold, m.config.ReminderInterval)

	// Do an immediate check
	if err := m.Check(); err != nil {
		log.Errorf("%s Initial token check failed: %v", logcolors.LogTokenMonitor, err)
	}

	// Then check periodically
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for range ticker.C {
		if err := m.Check(); err != nil {
			log.Errorf("%s Token check failed: %v", logcolors.LogTokenMonitor, err)
		}
	}
}
