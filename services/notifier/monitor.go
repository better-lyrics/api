package notifier

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

// MonitorConfig holds the configuration for the token monitor
type MonitorConfig struct {
	BearerToken       string
	WarningThreshold  int // Days before expiration to start warning
	ReminderInterval  int // Hours between reminders (to avoid spam)
	StateFile         string
	Notifiers         []Notifier
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
		log.Warnf("Failed to load state file: %v", err)
	}
}

// saveState saves the current notification state to disk
func (m *TokenMonitor) saveState() {
	if m.config.StateFile == "" {
		return
	}

	data, err := json.Marshal(m.state)
	if err != nil {
		log.Errorf("Failed to marshal state: %v", err)
		return
	}

	if err := os.WriteFile(m.config.StateFile, data, 0644); err != nil {
		log.Errorf("Failed to write state file: %v", err)
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

// Check performs a single check of the token expiration
func (m *TokenMonitor) Check() error {
	expiringSoon, daysRemaining, err := IsExpiringSoon(m.config.BearerToken, m.config.WarningThreshold)
	if err != nil {
		return fmt.Errorf("failed to check token expiration: %v", err)
	}

	log.Debugf("Token check: expiring_soon=%v, days_remaining=%d", expiringSoon, daysRemaining)

	// If token is not expiring soon, nothing to do
	if !expiringSoon {
		log.Debugf("Token is healthy, %d days until expiration", daysRemaining)
		return nil
	}

	// Check if we should send notification
	if !m.shouldSendNotification(daysRemaining) {
		log.Debugf("Skipping notification (too soon since last notification)")
		return nil
	}

	// Send notifications
	if err := m.sendNotifications(daysRemaining); err != nil {
		return fmt.Errorf("failed to send notifications: %v", err)
	}

	// Update state
	m.state.LastNotificationSent = time.Now()
	m.state.LastDaysRemaining = daysRemaining
	m.saveState()

	return nil
}

// sendNotifications sends notifications through all configured notifiers
func (m *TokenMonitor) sendNotifications(daysRemaining int) error {
	var subject, message string

	if daysRemaining <= 0 {
		subject = "🚨 URGENT: TTML Token EXPIRED"
		message = "🚨 TTML TOKEN EXPIRED\n\n" +
			"Your TTML bearer token has EXPIRED.\n\n" +
			"⚠️ Action Required:\n\n" +
			"The service will stop working until you update the token.\n\n" +
			"Update TTML_BEARER_TOKEN in your .env file and restart the service immediately."
	} else if daysRemaining == 1 {
		subject = "⚠️ Alert: TTML Token Expires Tomorrow"
		message = "⚠️ TTML TOKEN EXPIRATION WARNING\n\n" +
			"Your TTML bearer token will expire in 1 day.\n\n" +
			"📝 Action Required:\n\n" +
			"Update TTML_BEARER_TOKEN in your .env file soon to avoid service interruption."
	} else {
		subject = fmt.Sprintf("⏰ Notice: TTML Token Expires in %d Days", daysRemaining)
		message = fmt.Sprintf(
			"⏰ TTML TOKEN EXPIRATION NOTICE\n\n"+
			"Your TTML bearer token will expire in %d days.\n\n"+
			"📝 Action Required:\n\n"+
			"Update TTML_BEARER_TOKEN in your .env file before expiration to maintain service availability.\n\n"+
			"You will receive daily reminders until the token is updated.",
			daysRemaining,
		)
	}

	log.Infof("Sending notifications: %s", subject)

	var lastErr error
	successCount := 0

	for _, notifier := range m.config.Notifiers {
		if err := notifier.Send(subject, message); err != nil {
			log.Errorf("Notifier failed: %v", err)
			lastErr = err
		} else {
			successCount++
		}
	}

	if successCount == 0 && lastErr != nil {
		return fmt.Errorf("all notifiers failed, last error: %v", lastErr)
	}

	log.Infof("Successfully sent %d/%d notifications", successCount, len(m.config.Notifiers))
	return nil
}

// Run starts the monitor in a loop, checking at the specified interval
func (m *TokenMonitor) Run(checkInterval time.Duration) {
	log.Infof("Starting TTML token monitor (check interval: %v, warning threshold: %d days, reminder interval: %d hours)",
		checkInterval, m.config.WarningThreshold, m.config.ReminderInterval)

	// Do an immediate check
	if err := m.Check(); err != nil {
		log.Errorf("Initial token check failed: %v", err)
	}

	// Then check periodically
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for range ticker.C {
		if err := m.Check(); err != nil {
			log.Errorf("Token check failed: %v", err)
		}
	}
}
