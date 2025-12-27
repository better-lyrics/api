package circuitbreaker

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cb := New(Config{
		Name:      "test",
		Threshold: 3,
		Cooldown:  10 * time.Second,
	})

	if cb.name != "test" {
		t.Errorf("Expected name 'test', got %q", cb.name)
	}
	if cb.threshold != 3 {
		t.Errorf("Expected threshold 3, got %d", cb.threshold)
	}
	if cb.cooldown != 10*time.Second {
		t.Errorf("Expected cooldown 10s, got %v", cb.cooldown)
	}
	if cb.state != StateClosed {
		t.Errorf("Expected initial state CLOSED, got %s", cb.state)
	}
}

func TestNew_Defaults(t *testing.T) {
	cb := New(Config{})

	if cb.threshold != 5 {
		t.Errorf("Expected default threshold 5, got %d", cb.threshold)
	}
	if cb.cooldown != 5*time.Minute {
		t.Errorf("Expected default cooldown 5m, got %v", cb.cooldown)
	}
	if cb.halfOpenTimeout != 30*time.Second {
		t.Errorf("Expected default halfOpenTimeout 30s, got %v", cb.halfOpenTimeout)
	}
	if cb.name != "default" {
		t.Errorf("Expected default name 'default', got %q", cb.name)
	}
}

func TestCircuitBreaker_ClosedState(t *testing.T) {
	cb := New(Config{Threshold: 3, Cooldown: time.Second})

	// Should allow requests in closed state
	if !cb.Allow() {
		t.Error("Expected Allow() to return true in CLOSED state")
	}

	// State should be closed
	if cb.State() != StateClosed {
		t.Errorf("Expected CLOSED state, got %s", cb.State())
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := New(Config{Threshold: 3, Cooldown: time.Minute})

	// Record failures up to threshold
	cb.RecordFailure() // 1
	if cb.State() != StateClosed {
		t.Error("Expected CLOSED after 1 failure")
	}

	cb.RecordFailure() // 2
	if cb.State() != StateClosed {
		t.Error("Expected CLOSED after 2 failures")
	}

	cb.RecordFailure() // 3 - should trip
	if cb.State() != StateOpen {
		t.Errorf("Expected OPEN after %d failures, got %s", 3, cb.State())
	}

	// Should block requests when open
	if cb.Allow() {
		t.Error("Expected Allow() to return false in OPEN state")
	}
}

func TestCircuitBreaker_SuccessResetsFailures(t *testing.T) {
	cb := New(Config{Threshold: 3, Cooldown: time.Minute})

	// Record some failures
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.Failures() != 2 {
		t.Errorf("Expected 2 failures, got %d", cb.Failures())
	}

	// Success should reset counter
	cb.RecordSuccess()

	if cb.Failures() != 0 {
		t.Errorf("Expected 0 failures after success, got %d", cb.Failures())
	}
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	cb := New(Config{Threshold: 2, Cooldown: 100 * time.Millisecond})

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatalf("Expected OPEN state, got %s", cb.State())
	}

	// Wait for cooldown
	time.Sleep(150 * time.Millisecond)

	// Next Allow() should transition to half-open and return true
	if !cb.Allow() {
		t.Error("Expected Allow() to return true after cooldown")
	}

	if cb.State() != StateHalfOpen {
		t.Errorf("Expected HALF-OPEN state after cooldown, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenSuccess(t *testing.T) {
	cb := New(Config{Threshold: 2, Cooldown: 50 * time.Millisecond})

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for cooldown and transition to half-open
	time.Sleep(60 * time.Millisecond)
	cb.Allow()

	if cb.State() != StateHalfOpen {
		t.Fatalf("Expected HALF-OPEN state, got %s", cb.State())
	}

	// Success in half-open should close circuit
	cb.RecordSuccess()

	if cb.State() != StateClosed {
		t.Errorf("Expected CLOSED state after success in half-open, got %s", cb.State())
	}

	if cb.Failures() != 0 {
		t.Errorf("Expected 0 failures after closing, got %d", cb.Failures())
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	cb := New(Config{Threshold: 2, Cooldown: 50 * time.Millisecond})

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for cooldown and transition to half-open
	time.Sleep(60 * time.Millisecond)
	cb.Allow()

	if cb.State() != StateHalfOpen {
		t.Fatalf("Expected HALF-OPEN state, got %s", cb.State())
	}

	// Failure in half-open should re-open circuit
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Errorf("Expected OPEN state after failure in half-open, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenBlocksMultipleRequests(t *testing.T) {
	cb := New(Config{Threshold: 2, Cooldown: 50 * time.Millisecond})

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for cooldown and transition to half-open
	time.Sleep(60 * time.Millisecond)

	// First request should be allowed
	if !cb.Allow() {
		t.Error("Expected first request in half-open to be allowed")
	}

	// Subsequent requests should be blocked (only one test request at a time)
	if cb.Allow() {
		t.Error("Expected subsequent requests in half-open to be blocked")
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := New(Config{Threshold: 2, Cooldown: time.Minute})

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatalf("Expected OPEN state, got %s", cb.State())
	}

	// Reset should close circuit
	cb.Reset()

	if cb.State() != StateClosed {
		t.Errorf("Expected CLOSED state after reset, got %s", cb.State())
	}

	if cb.Failures() != 0 {
		t.Errorf("Expected 0 failures after reset, got %d", cb.Failures())
	}

	// Should allow requests again
	if !cb.Allow() {
		t.Error("Expected Allow() to return true after reset")
	}
}

func TestCircuitBreaker_TimeUntilRetry(t *testing.T) {
	cb := New(Config{Threshold: 2, Cooldown: 100 * time.Millisecond})

	// Closed state should return 0
	if cb.TimeUntilRetry() != 0 {
		t.Errorf("Expected 0 time until retry in CLOSED state, got %v", cb.TimeUntilRetry())
	}

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Open state should return remaining cooldown time
	remaining := cb.TimeUntilRetry()
	if remaining <= 0 || remaining > 100*time.Millisecond {
		t.Errorf("Expected positive time until retry, got %v", remaining)
	}

	// Wait for cooldown
	time.Sleep(110 * time.Millisecond)

	// Should return 0 after cooldown
	if cb.TimeUntilRetry() != 0 {
		t.Errorf("Expected 0 time until retry after cooldown, got %v", cb.TimeUntilRetry())
	}
}

func TestCircuitBreaker_IsOpen(t *testing.T) {
	cb := New(Config{Threshold: 2, Cooldown: time.Minute})

	if cb.IsOpen() {
		t.Error("Expected IsOpen() to return false initially")
	}

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if !cb.IsOpen() {
		t.Error("Expected IsOpen() to return true after tripping")
	}
}

func TestCircuitBreaker_Stats(t *testing.T) {
	cb := New(Config{Threshold: 3, Cooldown: time.Minute})

	cb.RecordFailure()
	cb.RecordFailure()

	state, failures, lastFailure := cb.Stats()

	if state != StateClosed {
		t.Errorf("Expected CLOSED state, got %s", state)
	}
	if failures != 2 {
		t.Errorf("Expected 2 failures, got %d", failures)
	}
	if lastFailure.IsZero() {
		t.Error("Expected non-zero lastFailure time")
	}
}

func TestCircuitBreaker_StateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateClosed, "CLOSED"},
		{StateOpen, "OPEN"},
		{StateHalfOpen, "HALF-OPEN"},
		{State(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		if tt.state.String() != tt.expected {
			t.Errorf("Expected %q, got %q", tt.expected, tt.state.String())
		}
	}
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cb := New(Config{Threshold: 100, Cooldown: time.Minute})

	// Simulate concurrent access
	done := make(chan bool)
	for i := 0; i < 50; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				cb.Allow()
				cb.RecordFailure()
				cb.RecordSuccess()
				cb.Failures()
				cb.State()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 50; i++ {
		<-done
	}

	// Should not panic and state should be valid
	state := cb.State()
	if state != StateClosed && state != StateOpen && state != StateHalfOpen {
		t.Errorf("Invalid state after concurrent access: %v", state)
	}
}

func TestCircuitBreaker_IsHalfOpen(t *testing.T) {
	cb := New(Config{Threshold: 2, Cooldown: 50 * time.Millisecond})

	// Should not be half-open initially
	if cb.IsHalfOpen() {
		t.Error("Expected IsHalfOpen() to return false initially")
	}

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Should not be half-open when open
	if cb.IsHalfOpen() {
		t.Error("Expected IsHalfOpen() to return false when OPEN")
	}

	// Wait for cooldown and transition to half-open
	time.Sleep(60 * time.Millisecond)
	cb.Allow()

	// Should be half-open now
	if !cb.IsHalfOpen() {
		t.Error("Expected IsHalfOpen() to return true after transition")
	}

	// After success, should not be half-open
	cb.RecordSuccess()
	if cb.IsHalfOpen() {
		t.Error("Expected IsHalfOpen() to return false after success")
	}
}

func TestCircuitBreaker_HalfOpenTimeout(t *testing.T) {
	cb := New(Config{
		Threshold:       2,
		Cooldown:        50 * time.Millisecond,
		HalfOpenTimeout: 100 * time.Millisecond,
	})

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatalf("Expected OPEN state, got %s", cb.State())
	}

	// Wait for cooldown and transition to half-open
	time.Sleep(60 * time.Millisecond)

	// First call should transition to HALF-OPEN and allow
	if !cb.Allow() {
		t.Error("Expected first Allow() after cooldown to return true")
	}
	if cb.State() != StateHalfOpen {
		t.Fatalf("Expected HALF-OPEN state, got %s", cb.State())
	}

	// Second call should block (test request in progress)
	if cb.Allow() {
		t.Error("Expected second Allow() in HALF-OPEN to return false")
	}

	// Wait for half-open timeout to expire
	time.Sleep(110 * time.Millisecond)

	// Next Allow() should detect timeout, reset to OPEN, and return false
	if cb.Allow() {
		t.Error("Expected Allow() to return false after half-open timeout")
	}
	if cb.State() != StateOpen {
		t.Errorf("Expected OPEN state after half-open timeout, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenTimeUntilRetry(t *testing.T) {
	cb := New(Config{
		Threshold:       2,
		Cooldown:        50 * time.Millisecond,
		HalfOpenTimeout: 100 * time.Millisecond,
	})

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for cooldown and transition to half-open
	time.Sleep(60 * time.Millisecond)
	cb.Allow()

	if cb.State() != StateHalfOpen {
		t.Fatalf("Expected HALF-OPEN state, got %s", cb.State())
	}

	// TimeUntilRetry should return remaining half-open timeout
	remaining := cb.TimeUntilRetry()
	if remaining <= 0 || remaining > 100*time.Millisecond {
		t.Errorf("Expected positive time until retry in HALF-OPEN, got %v", remaining)
	}

	// Wait for timeout to expire
	time.Sleep(110 * time.Millisecond)

	// TimeUntilRetry should return 0 after timeout
	if cb.TimeUntilRetry() != 0 {
		t.Errorf("Expected 0 time until retry after half-open timeout, got %v", cb.TimeUntilRetry())
	}
}

func TestCircuitBreaker_HalfOpenTransitionRecordsStart(t *testing.T) {
	cb := New(Config{
		Threshold:       2,
		Cooldown:        50 * time.Millisecond,
		HalfOpenTimeout: 100 * time.Millisecond,
	})

	// Initially halfOpenStart should be zero
	if !cb.halfOpenStart.IsZero() {
		t.Error("Expected halfOpenStart to be zero initially")
	}

	// Trip the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// halfOpenStart should still be zero in OPEN state
	if !cb.halfOpenStart.IsZero() {
		t.Error("Expected halfOpenStart to be zero in OPEN state")
	}

	// Wait for cooldown and transition to half-open
	time.Sleep(60 * time.Millisecond)
	beforeTransition := time.Now()
	cb.Allow()
	afterTransition := time.Now()

	// halfOpenStart should be set after transition
	if cb.halfOpenStart.IsZero() {
		t.Error("Expected halfOpenStart to be set after transition to HALF-OPEN")
	}

	// halfOpenStart should be between before and after transition
	if cb.halfOpenStart.Before(beforeTransition) || cb.halfOpenStart.After(afterTransition) {
		t.Errorf("halfOpenStart %v not between %v and %v", cb.halfOpenStart, beforeTransition, afterTransition)
	}
}

func TestCircuitBreaker_ResetClearsHalfOpenStart(t *testing.T) {
	cb := New(Config{
		Threshold:       2,
		Cooldown:        50 * time.Millisecond,
		HalfOpenTimeout: 100 * time.Millisecond,
	})

	// Trip the circuit and transition to half-open
	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	cb.Allow()

	if cb.halfOpenStart.IsZero() {
		t.Fatal("Expected halfOpenStart to be set")
	}

	// Reset should clear halfOpenStart
	cb.Reset()

	if !cb.halfOpenStart.IsZero() {
		t.Error("Expected halfOpenStart to be cleared after Reset()")
	}
}
