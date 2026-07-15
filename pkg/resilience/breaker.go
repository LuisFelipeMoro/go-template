// breaker.go
package resilience

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// State is a circuit-breaker state.
type State int

const (
	// StateClosed passes calls through and counts failures.
	StateClosed State = iota
	// StateOpen fast-fails every call until the cooldown elapses.
	StateOpen
	// StateHalfOpen admits a limited number of probe calls.
	StateHalfOpen
)

// String renders the state for logs and metrics.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrOpen is returned when the breaker rejects a call without running it.
var ErrOpen = errors.New("circuit breaker open")

// BreakerConfig tunes CircuitBreaker; the zero value is usable via defaults.
type BreakerConfig struct {
	FailureThreshold int           // consecutive failures to open; defaults to 5
	Cooldown         time.Duration // open → half-open wait; defaults to 10s
	HalfOpenMax      int           // concurrent probes allowed; defaults to 1
}

// CircuitBreaker guards an outbound dependency. All state is mutex-serialized,
// so a single breaker is safe for concurrent use.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            State
	failures         int
	openedAt         time.Time
	halfOpenActive   int
	failureThreshold int
	cooldown         time.Duration
	halfOpenMax      int

	// now is an injectable clock for deterministic tests.
	now func() time.Time
}

// NewCircuitBreaker constructs a breaker, applying defaults for zero fields.
func NewCircuitBreaker(cfg BreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold < 1 {
		cfg.FailureThreshold = 5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 10 * time.Second
	}
	if cfg.HalfOpenMax < 1 {
		cfg.HalfOpenMax = 1
	}
	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: cfg.FailureThreshold,
		cooldown:         cfg.Cooldown,
		halfOpenMax:      cfg.HalfOpenMax,
		now:              time.Now,
	}
}

// State returns the current state, transitioning open → half-open if the
// cooldown has elapsed.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.maybeHalfOpen()
	return cb.state
}

// Execute runs fn unless the breaker rejects it with ErrOpen. Success and
// failure update the breaker state.
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	if err := cb.beforeCall(); err != nil {
		return err
	}

	err := fn(ctx)
	cb.afterCall(err)
	if err != nil {
		return fmt.Errorf("circuit breaker call: %w", err)
	}
	return nil
}

// beforeCall admits or rejects a call based on state and probe budget.
func (cb *CircuitBreaker) beforeCall() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.maybeHalfOpen()

	switch cb.state {
	case StateOpen:
		return ErrOpen
	case StateHalfOpen:
		if cb.halfOpenActive >= cb.halfOpenMax {
			return ErrOpen
		}
		cb.halfOpenActive++
		return nil
	default: // StateClosed
		return nil
	}
}

// afterCall records the outcome and drives state transitions.
func (cb *CircuitBreaker) afterCall(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	wasHalfOpen := cb.state == StateHalfOpen
	if wasHalfOpen {
		cb.halfOpenActive--
	}

	if err != nil {
		cb.failures++
		if wasHalfOpen || cb.failures >= cb.failureThreshold {
			cb.trip()
		}
		return
	}

	// Success.
	if wasHalfOpen {
		cb.reset()
		return
	}
	cb.failures = 0
}

// maybeHalfOpen moves an open breaker to half-open once the cooldown elapses.
// Callers must hold cb.mu.
func (cb *CircuitBreaker) maybeHalfOpen() {
	if cb.state == StateOpen && cb.now().Sub(cb.openedAt) >= cb.cooldown {
		cb.state = StateHalfOpen
		cb.halfOpenActive = 0
	}
}

// trip opens the breaker. Callers must hold cb.mu.
func (cb *CircuitBreaker) trip() {
	cb.state = StateOpen
	cb.openedAt = cb.now()
	cb.halfOpenActive = 0
}

// reset closes the breaker. Callers must hold cb.mu.
func (cb *CircuitBreaker) reset() {
	cb.state = StateClosed
	cb.failures = 0
	cb.halfOpenActive = 0
}
