// Package resilience holds the retry and circuit breaker used on calls that
// cross service boundaries.
package resilience

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// RetryPolicy describes how a failing call is retried.
type RetryPolicy struct {
	// Attempts is the total number of tries, including the first one.
	Attempts int
	// BaseDelay is the wait before the second attempt; it doubles after that.
	BaseDelay time.Duration
	// MaxDelay caps the exponential growth.
	MaxDelay time.Duration
	// sleep and jitter are replaced in tests to keep them fast and predictable.
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func(d time.Duration) time.Duration
}

// DefaultRetryPolicy is a short, bounded policy suited to user facing calls.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{Attempts: 3, BaseDelay: 100 * time.Millisecond, MaxDelay: 2 * time.Second}
}

// permanentError marks a failure that must not be retried, such as a rejected
// request: retrying it would only produce the same answer.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as not retryable.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err was marked as not retryable.
func IsPermanent(err error) bool {
	var permanent *permanentError
	return errors.As(err, &permanent)
}

// Retry runs operation until it succeeds, the error is permanent, the attempts
// run out or ctx is done. The error returned is the last one produced by the
// operation, unwrapped from the permanent marker.
func Retry(ctx context.Context, policy RetryPolicy, operation func(ctx context.Context) error) error {
	attempts := max(policy.Attempts, 1)
	baseDelay := policy.BaseDelay
	if baseDelay <= 0 {
		baseDelay = 100 * time.Millisecond
	}
	maxDelay := policy.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 2 * time.Second
	}
	sleep := policy.sleep
	if sleep == nil {
		sleep = sleepContext
	}
	jitter := policy.jitter
	if jitter == nil {
		jitter = randomJitter
	}

	delay := baseDelay
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}

		err := operation(ctx)
		if err == nil {
			return nil
		}
		if IsPermanent(err) {
			return errors.Unwrap(err)
		}
		lastErr = err

		if attempt == attempts {
			break
		}
		if err := sleep(ctx, jitter(delay)); err != nil {
			return lastErr
		}
		delay = min(delay*2, maxDelay)
	}
	return lastErr
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// randomJitter spreads retries of concurrent callers over time, so a recovering
// dependency is not hit by a synchronized burst.
func randomJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}
