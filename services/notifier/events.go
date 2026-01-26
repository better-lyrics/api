package notifier

import (
	"sync"
	"time"
)

// EventType represents the type of event
type EventType string

const (
	// Critical events
	EventCircuitBreakerOpen    EventType = "circuit_breaker_open"
	EventAllAccountsQuarantine EventType = "all_accounts_quarantined"
	EventAccountAuthFailure    EventType = "account_auth_failure"
	EventServerStartupFailed   EventType = "server_startup_failed"
	EventMUTHealthCheckFailed  EventType = "mut_health_check_failed"

	// Warning events
	EventHighFailureRate        EventType = "high_failure_rate"
	EventHalfAccountsQuarantine EventType = "half_accounts_quarantined"
	EventOneAwayFromQuarantine  EventType = "one_away_from_quarantine"
	EventCacheBackupFailed      EventType = "cache_backup_failed"

	// Info events
	EventCircuitBreakerRecovered EventType = "circuit_breaker_recovered"
	EventServerStarted           EventType = "server_started"
	EventCacheCleared            EventType = "cache_cleared"
)

// Severity represents the severity level of an event
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Event represents a system event
type Event struct {
	Type      EventType
	Severity  Severity
	Message   string
	Data      map[string]interface{}
	Timestamp time.Time
}

// NewEvent creates a new event with the current timestamp
func NewEvent(eventType EventType, severity Severity, message string) *Event {
	return &Event{
		Type:      eventType,
		Severity:  severity,
		Message:   message,
		Data:      make(map[string]interface{}),
		Timestamp: time.Now(),
	}
}

// WithData adds data to the event (chainable)
func (e *Event) WithData(key string, value interface{}) *Event {
	e.Data[key] = value
	return e
}

// EventHandler is a function that handles events
type EventHandler func(event *Event)

// EventBus manages event publishing and subscription
type EventBus struct {
	handlers    map[EventType][]EventHandler
	allHandlers []EventHandler // handlers that receive all events
	mu          sync.RWMutex
}

// Global event bus instance
var globalBus *EventBus
var busOnce sync.Once

// GetEventBus returns the global event bus instance
func GetEventBus() *EventBus {
	busOnce.Do(func() {
		globalBus = &EventBus{
			handlers:    make(map[EventType][]EventHandler),
			allHandlers: make([]EventHandler, 0),
		}
	})
	return globalBus
}

// Subscribe adds a handler for a specific event type
func (b *EventBus) Subscribe(eventType EventType, handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], handler)
}

// SubscribeAll adds a handler that receives all events
func (b *EventBus) SubscribeAll(handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.allHandlers = append(b.allHandlers, handler)
}

// Publish sends an event to all subscribed handlers
func (b *EventBus) Publish(event *Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Call specific handlers
	if handlers, ok := b.handlers[event.Type]; ok {
		for _, handler := range handlers {
			go handler(event)
		}
	}

	// Call handlers subscribed to all events
	for _, handler := range b.allHandlers {
		go handler(event)
	}
}

// Helper functions for publishing common events

// PublishCircuitBreakerOpen publishes a circuit breaker open event
func PublishCircuitBreakerOpen(name string, failures int, cooldown time.Duration) {
	event := NewEvent(EventCircuitBreakerOpen, SeverityCritical,
		"Circuit breaker has opened due to consecutive failures").
		WithData("name", name).
		WithData("failures", failures).
		WithData("cooldown", cooldown.String())
	GetEventBus().Publish(event)
}

// PublishCircuitBreakerRecovered publishes a circuit breaker recovery event
func PublishCircuitBreakerRecovered(name string) {
	event := NewEvent(EventCircuitBreakerRecovered, SeverityInfo,
		"Circuit breaker has recovered and is operational").
		WithData("name", name)
	GetEventBus().Publish(event)
}

// PublishHighFailureRate publishes a high failure rate warning
func PublishHighFailureRate(name string, failures, threshold int) {
	event := NewEvent(EventHighFailureRate, SeverityWarning,
		"High failure rate detected, circuit breaker may trip soon").
		WithData("name", name).
		WithData("failures", failures).
		WithData("threshold", threshold)
	GetEventBus().Publish(event)
}

// PublishAllAccountsQuarantined publishes when all accounts are quarantined
func PublishAllAccountsQuarantined(accountStatus map[string]int64, outOfServiceAccounts []string) {
	event := NewEvent(EventAllAccountsQuarantine, SeverityCritical,
		"All active API accounts are currently rate-limited").
		WithData("accounts", accountStatus).
		WithData("accounts_out_of_service", outOfServiceAccounts)
	GetEventBus().Publish(event)
}

// PublishHalfAccountsQuarantined publishes when half or more accounts are quarantined
func PublishHalfAccountsQuarantined(quarantined, totalActive int, accountStatus map[string]int64, outOfServiceAccounts []string) {
	event := NewEvent(EventHalfAccountsQuarantine, SeverityWarning,
		"Half or more active API accounts are rate-limited").
		WithData("quarantined", quarantined).
		WithData("total_active", totalActive).
		WithData("accounts", accountStatus).
		WithData("accounts_out_of_service", outOfServiceAccounts)
	GetEventBus().Publish(event)
}

// PublishOneAwayFromQuarantine publishes when only one account remains healthy
func PublishOneAwayFromQuarantine(remainingAccount string, quarantinedStatus map[string]int64, outOfServiceAccounts []string) {
	event := NewEvent(EventOneAwayFromQuarantine, SeverityWarning,
		"Only one active API account remains healthy").
		WithData("remaining_account", remainingAccount).
		WithData("quarantined", quarantinedStatus).
		WithData("accounts_out_of_service", outOfServiceAccounts)
	GetEventBus().Publish(event)
}

// PublishAccountAuthFailure publishes when an account receives 401
func PublishAccountAuthFailure(accountName string, statusCode int) {
	event := NewEvent(EventAccountAuthFailure, SeverityCritical,
		"API account authentication failed").
		WithData("account", accountName).
		WithData("status_code", statusCode)
	GetEventBus().Publish(event)
}

// PublishCacheBackupFailed publishes when cache backup fails
func PublishCacheBackupFailed(err error) {
	event := NewEvent(EventCacheBackupFailed, SeverityWarning,
		"Cache backup operation failed").
		WithData("error", err.Error())
	GetEventBus().Publish(event)
}

// PublishCacheCleared publishes when cache is cleared
func PublishCacheCleared(backupPath string) {
	event := NewEvent(EventCacheCleared, SeverityInfo,
		"Cache has been cleared").
		WithData("backup_path", backupPath)
	GetEventBus().Publish(event)
}

// PublishServerStarted publishes when server starts successfully
func PublishServerStarted(port string, activeCount int, outOfServiceAccounts []string) {
	event := NewEvent(EventServerStarted, SeverityInfo,
		"Server started successfully").
		WithData("port", port).
		WithData("accounts_active", activeCount).
		WithData("accounts_out_of_service", outOfServiceAccounts)
	GetEventBus().Publish(event)
}

// PublishServerStartupFailed publishes when server fails to start
func PublishServerStartupFailed(component string, err error) {
	event := NewEvent(EventServerStartupFailed, SeverityCritical,
		"Server failed to start").
		WithData("component", component).
		WithData("error", err.Error())
	GetEventBus().Publish(event)
}

// PublishMUTHealthCheckFailed publishes when MUT health check detects unhealthy accounts
func PublishMUTHealthCheckFailed(unhealthyAccounts interface{}) {
	event := NewEvent(EventMUTHealthCheckFailed, SeverityCritical,
		"MUT health check detected unhealthy accounts").
		WithData("unhealthy_accounts", unhealthyAccounts)
	GetEventBus().Publish(event)
}
