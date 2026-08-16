package resilience

import (
	"context"
	"sync"
	"time"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// ErrBreakerOpen is returned while the circuit is open, so callers fail fast
// with a message the user can act on instead of waiting for a timeout.
var ErrBreakerOpen = apperr.Unavailable("dependency_unavailable",
	"The service is temporarily unavailable. Please try again in a moment.")

// breakerState is the state of the circuit.
type breakerState int

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

// Breaker stops calling a dependency that keeps failing, and probes it again
// after a cool down period.
type Breaker struct {
	failureThreshold int
	successThreshold int
	cooldown         time.Duration
	now              func() time.Time

	mu        sync.Mutex
	state     breakerState
	failures  int
	successes int
	openedAt  time.Time
}

// NewBreaker opens the circuit after failureThreshold consecutive failures and
// probes the dependency again after cooldown.
func NewBreaker(failureThreshold int, cooldown time.Duration) *Breaker {
	if failureThreshold < 1 {
		failureThreshold = 1
	}
	if cooldown <= 0 {
		cooldown = 5 * time.Second
	}
	return &Breaker{
		failureThreshold: failureThreshold,
		successThreshold: 1,
		cooldown:         cooldown,
		now:              time.Now,
	}
}

// Do runs operation unless the circuit is open, recording the outcome.
func (b *Breaker) Do(ctx context.Context, operation func(ctx context.Context) error) error {
	if err := b.allow(); err != nil {
		return err
	}

	err := operation(ctx)
	// A rejected request says nothing about the health of the dependency, so
	// permanent errors do not open the circuit.
	if err == nil || IsPermanent(err) {
		b.recordSuccess()
		return err
	}
	b.recordFailure()
	return err
}

// State reports the circuit state as a readable string, for logs and health.
func (b *Breaker) State() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case stateOpen:
		if b.now().Sub(b.openedAt) >= b.cooldown {
			return "half-open"
		}
		return "open"
	case stateHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

func (b *Breaker) allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == stateOpen {
		if b.now().Sub(b.openedAt) < b.cooldown {
			return ErrBreakerOpen
		}
		// Cool down elapsed: let a single probe through.
		b.state = stateHalfOpen
		b.successes = 0
	}
	return nil
}

func (b *Breaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures = 0
	if b.state == stateHalfOpen {
		b.successes++
		if b.successes >= b.successThreshold {
			b.state = stateClosed
			b.successes = 0
		}
	}
}

func (b *Breaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == stateHalfOpen {
		// The probe failed: stay open for another cool down period.
		b.state = stateOpen
		b.openedAt = b.now()
		b.failures = b.failureThreshold
		return
	}

	b.failures++
	if b.failures >= b.failureThreshold {
		b.state = stateOpen
		b.openedAt = b.now()
	}
}
