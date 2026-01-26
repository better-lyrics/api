package circuitbreaker

import (
	"errors"
	"lyrics-api-go/logcolors"
	"lyrics-api-go/services/notifier"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// State represents the circuit breaker state
type State int

const (
	StateClosed   State = iota // Normal operation, requests allowed
	StateOpen                  // Circuit tripped, requests blocked
	StateHalfOpen              // Testing if service recovered
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF-OPEN"
	default:
		return "UNKNOWN"
	}
}

var (
	ErrCircuitOpen = errors.New("circuit breaker is open")
)

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name            string
	state           State
	failures        int           // consecutive failures
	threshold       int           // failures before opening
	cooldown        time.Duration // how long to stay open
	halfOpenTimeout time.Duration // max time to wait in half-open state
	lastFailureTime time.Time     // when circuit opened
	halfOpenStart   time.Time     // when half-open state began
	mu              sync.RWMutex
}

// Config holds circuit breaker configuration
type Config struct {
	Name            string        // Name for logging
	Threshold       int           // Number of consecutive failures before opening
	Cooldown        time.Duration // How long to stay open before testing
	HalfOpenTimeout time.Duration // Max time to wait in half-open state before resetting to open
}

// New creates a new circuit breaker
func New(cfg Config) *CircuitBreaker {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 5 // default: 5 consecutive failures
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 5 * time.Minute // default: 5 minute cooldown
	}
	if cfg.HalfOpenTimeout <= 0 {
		cfg.HalfOpenTimeout = 30 * time.Second // default: 30 second half-open timeout
	}
	if cfg.Name == "" {
		cfg.Name = "default"
	}

	return &CircuitBreaker{
		name:            cfg.Name,
		state:           StateClosed,
		threshold:       cfg.Threshold,
		cooldown:        cfg.Cooldown,
		halfOpenTimeout: cfg.HalfOpenTimeout,
	}
}

// Allow checks if a request should be allowed
// Returns true if the request can proceed, false if blocked
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if cooldown has passed
		if time.Since(cb.lastFailureTime) >= cb.cooldown {
			cb.state = StateHalfOpen
			cb.halfOpenStart = time.Now()
			log.Infof("%s Cooldown passed, transitioning to HALF-OPEN", logcolors.CircuitBreakerPrefix(cb.name))
			return true // Allow one test request
		}
		return false

	case StateHalfOpen:
		// Check if half-open timeout has expired
		if time.Since(cb.halfOpenStart) >= cb.halfOpenTimeout {
			// Test request timed out, reset to OPEN
			cb.state = StateOpen
			cb.lastFailureTime = time.Now()
			log.Warnf("%s Half-open timeout expired, transitioning back to OPEN", logcolors.CircuitBreakerPrefix(cb.name))
			return false
		}
		// Only allow one request at a time in half-open state
		// The first request is already in progress, block others
		return false

	default:
		return true
	}
}

// RecordSuccess records a successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateHalfOpen {
		// Test request succeeded, close the circuit
		cb.state = StateClosed
		cb.failures = 0
		log.Infof("%s Test request succeeded, transitioning to CLOSED", logcolors.CircuitBreakerPrefix(cb.name))
		// Emit recovery event
		notifier.PublishCircuitBreakerRecovered(cb.name)
	} else if cb.state == StateClosed {
		// Reset failure count on success
		cb.failures = 0
	}
}

// RecordFailure records a failed request
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailureTime = time.Now()

	if cb.state == StateHalfOpen {
		// Test request failed, back to open
		cb.state = StateOpen
		log.Warnf("%s Test request failed, transitioning back to OPEN", logcolors.CircuitBreakerPrefix(cb.name))
		// Emit circuit open event
		notifier.PublishCircuitBreakerOpen(cb.name, cb.failures, cb.cooldown)
		return
	}

	if cb.state == StateClosed {
		// Check for high failure rate warning (at 60% of threshold)
		warningThreshold := (cb.threshold * 3) / 5 // 60% of threshold
		if warningThreshold < 2 {
			warningThreshold = 2
		}
		if cb.failures == warningThreshold {
			notifier.PublishHighFailureRate(cb.name, cb.failures, cb.threshold)
		}

		if cb.failures >= cb.threshold {
			cb.state = StateOpen
			log.Warnf("%s Threshold reached (%d failures), transitioning to OPEN (cooldown: %v)",
				logcolors.CircuitBreakerPrefix(cb.name), cb.failures, cb.cooldown)
			// Emit circuit open event
			notifier.PublishCircuitBreakerOpen(cb.name, cb.failures, cb.cooldown)
		}
	}
}

// State returns the current state
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Failures returns the current consecutive failure count
func (cb *CircuitBreaker) Failures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

// Stats returns circuit breaker statistics
func (cb *CircuitBreaker) Stats() (state State, failures int, lastFailure time.Time) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state, cb.failures, cb.lastFailureTime
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failures = 0
	cb.lastFailureTime = time.Time{}
	cb.halfOpenStart = time.Time{}
	log.Infof("%s Manually reset to CLOSED", logcolors.CircuitBreakerPrefix(cb.name))
}

// IsOpen returns true if the circuit is open (blocking requests)
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state == StateOpen
}

// TimeUntilRetry returns how long until the circuit will try again
// For OPEN state: returns remaining cooldown time
// For HALF-OPEN state: returns remaining timeout until reset to OPEN
// Returns 0 if circuit is closed
func (cb *CircuitBreaker) TimeUntilRetry() time.Duration {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	switch cb.state {
	case StateOpen:
		elapsed := time.Since(cb.lastFailureTime)
		if elapsed >= cb.cooldown {
			return 0
		}
		return cb.cooldown - elapsed

	case StateHalfOpen:
		elapsed := time.Since(cb.halfOpenStart)
		if elapsed >= cb.halfOpenTimeout {
			return 0
		}
		return cb.halfOpenTimeout - elapsed

	default:
		return 0
	}
}

// IsHalfOpen returns true if the circuit is in half-open state
func (cb *CircuitBreaker) IsHalfOpen() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state == StateHalfOpen
}

// Threshold returns the configured failure threshold
func (cb *CircuitBreaker) Threshold() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.threshold
}
