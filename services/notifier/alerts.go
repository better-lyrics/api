package notifier

import (
	"fmt"
	"lyrics-api-go/logcolors"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// Default cooldown between alerts of the same type
	DefaultAlertCooldown = 15 * time.Minute
)

// AlertHandler handles events and sends notifications
type AlertHandler struct {
	notifiers    []Notifier
	cooldowns    map[EventType]time.Time // last alert time per event type
	cooldownDuration time.Duration
	mu           sync.RWMutex
}

// AlertConfig holds configuration for the alert handler
type AlertConfig struct {
	Notifiers        []Notifier
	CooldownDuration time.Duration
}

// NewAlertHandler creates a new alert handler
func NewAlertHandler(config AlertConfig) *AlertHandler {
	cooldown := config.CooldownDuration
	if cooldown == 0 {
		cooldown = DefaultAlertCooldown
	}

	handler := &AlertHandler{
		notifiers:        config.Notifiers,
		cooldowns:        make(map[EventType]time.Time),
		cooldownDuration: cooldown,
	}

	return handler
}

// Start subscribes the handler to the event bus
func (h *AlertHandler) Start() {
	bus := GetEventBus()
	bus.SubscribeAll(h.handleEvent)
	log.Infof("%s Alert handler started (cooldown: %v, notifiers: %d)",
		logcolors.LogNotifier, h.cooldownDuration, len(h.notifiers))
}

// handleEvent processes incoming events
func (h *AlertHandler) handleEvent(event *Event) {
	// Check cooldown
	if !h.shouldAlert(event.Type) {
		log.Debugf("%s Skipping alert for %s (cooldown active)", logcolors.LogNotifier, event.Type)
		return
	}

	// Format and send the alert
	subject, message := h.formatAlert(event)
	if subject == "" {
		return // Unknown event type
	}

	h.sendAlert(subject, message, event)
}

// shouldAlert checks if we should send an alert based on cooldown
func (h *AlertHandler) shouldAlert(eventType EventType) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	lastAlert, exists := h.cooldowns[eventType]
	if !exists || time.Since(lastAlert) >= h.cooldownDuration {
		h.cooldowns[eventType] = time.Now()
		return true
	}
	return false
}

// formatAlert formats an event into a notification message
func (h *AlertHandler) formatAlert(event *Event) (subject, message string) {
	switch event.Type {
	// Critical events
	case EventCircuitBreakerOpen:
		name := event.Data["name"].(string)
		failures := event.Data["failures"].(int)
		cooldown := event.Data["cooldown"].(string)
		subject = "Circuit Breaker OPEN"
		message = fmt.Sprintf(
			"The %s circuit breaker has tripped after %d consecutive failures.\n\n"+
				"All requests will be blocked for %s.\n\n"+
				"Action: Check TTML API status and account health.",
			name, failures, cooldown)

	case EventAllAccountsQuarantine:
		accounts := event.Data["accounts"].(map[string]int64)
		outOfService := getStringSlice(event.Data, "accounts_out_of_service")
		subject = "All Active Accounts Quarantined"
		message = "All active TTML accounts are currently rate-limited:\n\n"
		for name, remaining := range accounts {
			message += fmt.Sprintf("  • %s: %s remaining\n", name, formatDuration(remaining))
		}
		if len(outOfService) > 0 {
			message += "\n📌 Out of service (empty credentials):\n"
			for _, name := range outOfService {
				message += fmt.Sprintf("  • %s\n", name)
			}
		}
		message += "\nThe API is degraded and may return errors until accounts recover."

	case EventAccountAuthFailure:
		account := event.Data["account"].(string)
		statusCode := event.Data["status_code"].(int)
		subject = "Account Auth Failure"
		message = fmt.Sprintf(
			"Account '%s' received HTTP %d (authentication failed).\n\n"+
				"This account's token may be expired or invalid.\n\n"+
				"Action: Check and refresh the TTML bearer token for this account.",
			account, statusCode)

	case EventMUTHealthCheckFailed:
		subject = "MUT Health Check Failed"
		message = "MUT health check detected unhealthy accounts:\n\n"
		if accounts, ok := event.Data["unhealthy_accounts"].([]map[string]string); ok {
			for _, acc := range accounts {
				message += fmt.Sprintf("  • %s: %s\n", acc["name"], acc["error"])
			}
		}
		message += "\nAction: Check and refresh the Media User Token for these accounts."

	case EventServerStartupFailed:
		component := event.Data["component"].(string)
		errMsg := event.Data["error"].(string)
		subject = "Server Startup FAILED"
		message = fmt.Sprintf(
			"The server failed to start.\n\n"+
				"Component: %s\n"+
				"Error: %s\n\n"+
				"Action: Check logs and fix the issue immediately.",
			component, errMsg)

	// Warning events
	case EventHighFailureRate:
		name := event.Data["name"].(string)
		failures := event.Data["failures"].(int)
		threshold := event.Data["threshold"].(int)
		subject = "High Failure Rate Warning"
		message = fmt.Sprintf(
			"The %s circuit breaker has recorded %d/%d failures.\n\n"+
				"If failures continue, the circuit will open and block all requests.\n\n"+
				"Action: Monitor the situation closely.",
			name, failures, threshold)

	case EventHalfAccountsQuarantine:
		quarantined := event.Data["quarantined"].(int)
		totalActive := event.Data["total_active"].(int)
		accounts := event.Data["accounts"].(map[string]int64)
		outOfService := getStringSlice(event.Data, "accounts_out_of_service")
		subject = "Half Active Accounts Quarantined"
		message = fmt.Sprintf("%d of %d active accounts are rate-limited:\n\n", quarantined, totalActive)
		for name, remaining := range accounts {
			message += fmt.Sprintf("  • %s: %s remaining\n", name, formatDuration(remaining))
		}
		if len(outOfService) > 0 {
			message += "\n📌 Out of service (empty credentials):\n"
			for _, name := range outOfService {
				message += fmt.Sprintf("  • %s\n", name)
			}
		}
		message += "\nAPI capacity is reduced. Monitor for further degradation."

	case EventOneAwayFromQuarantine:
		remaining := event.Data["remaining_account"].(string)
		quarantined := event.Data["quarantined"].(map[string]int64)
		outOfService := getStringSlice(event.Data, "accounts_out_of_service")
		subject = "One Active Account Away from Full Quarantine"
		message = fmt.Sprintf("Only '%s' remains healthy among active accounts.\n\nQuarantined:\n", remaining)
		for name, secs := range quarantined {
			message += fmt.Sprintf("  • %s: %s remaining\n", name, formatDuration(secs))
		}
		if len(outOfService) > 0 {
			message += "\n📌 Out of service (empty credentials):\n"
			for _, name := range outOfService {
				message += fmt.Sprintf("  • %s\n", name)
			}
		}
		message += "\nIf this account gets rate-limited, all active accounts will be quarantined."

	case EventCacheBackupFailed:
		errMsg := event.Data["error"].(string)
		subject = "Cache Backup Failed"
		message = fmt.Sprintf(
			"Failed to create cache backup.\n\n"+
				"Error: %s\n\n"+
				"Action: Check disk space and permissions.",
			errMsg)

	// Info events
	case EventCircuitBreakerRecovered:
		name := event.Data["name"].(string)
		subject = "Circuit Breaker Recovered"
		message = fmt.Sprintf("The %s circuit breaker has recovered and is now operational.", name)

	case EventServerStarted:
		port := event.Data["port"].(string)
		activeCount := event.Data["accounts_active"].(int)
		outOfService := getStringSlice(event.Data, "accounts_out_of_service")
		subject = "Server Started"
		if len(outOfService) > 0 {
			message = fmt.Sprintf(
				"Server started successfully on port %s.\n\n"+
					"Accounts:\n"+
					"  • Active: %d\n"+
					"  • Out of service: %s (empty credentials)",
				port, activeCount, strings.Join(outOfService, ", "))
		} else {
			message = fmt.Sprintf("Server started successfully on port %s with %d account(s).", port, activeCount)
		}

	case EventCacheCleared:
		backupPath := event.Data["backup_path"].(string)
		subject = "Cache Cleared"
		message = fmt.Sprintf("Cache has been cleared.\n\nBackup saved to: %s", backupPath)

	default:
		return "", ""
	}

	// Add severity emoji prefix
	switch event.Severity {
	case SeverityCritical:
		subject = "🚨 " + subject
	case SeverityWarning:
		subject = "⚠️ " + subject
	case SeverityInfo:
		subject = "ℹ️ " + subject
	}

	return subject, message
}

// sendAlert sends the alert through all configured notifiers
func (h *AlertHandler) sendAlert(subject, message string, event *Event) {
	if len(h.notifiers) == 0 {
		log.Warnf("%s No notifiers configured, skipping alert: %s", logcolors.LogNotifier, subject)
		return
	}

	log.Infof("%s Sending alert: %s", logcolors.LogNotifier, subject)

	successCount := 0
	for _, n := range h.notifiers {
		if err := n.Send(subject, message); err != nil {
			log.Errorf("%s Failed to send alert via notifier: %v", logcolors.LogNotifier, err)
		} else {
			successCount++
		}
	}

	if successCount > 0 {
		log.Infof("%s Alert sent successfully via %d/%d notifiers", logcolors.LogNotifier, successCount, len(h.notifiers))
	}
}

// formatDuration formats seconds into a human-readable duration
func formatDuration(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	d := time.Duration(seconds) * time.Second
	if d >= time.Hour {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// ResetCooldown manually resets the cooldown for a specific event type
// Useful for testing or when you want to force an alert
func (h *AlertHandler) ResetCooldown(eventType EventType) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.cooldowns, eventType)
}

// ResetAllCooldowns resets all cooldowns
func (h *AlertHandler) ResetAllCooldowns() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cooldowns = make(map[EventType]time.Time)
}

// getStringSlice safely gets a string slice from event data, returning empty slice if missing
func getStringSlice(data map[string]interface{}, key string) []string {
	if val, ok := data[key].([]string); ok {
		return val
	}
	return nil
}
