package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testPolicy retries without ever sleeping for real.
func testPolicy(attempts int, slept *[]time.Duration) RetryPolicy {
	return RetryPolicy{
		Attempts:  attempts,
		BaseDelay: 100 * time.Millisecond,
		MaxDelay:  400 * time.Millisecond,
		sleep: func(ctx context.Context, d time.Duration) error {
			if slept != nil {
				*slept = append(*slept, d)
			}
			return ctx.Err()
		},
		jitter: func(d time.Duration) time.Duration { return d },
	}
}

func TestRetrySucceedsOnFirstAttempt(t *testing.T) {
	calls := 0

	err := Retry(context.Background(), testPolicy(3, nil), func(ctx context.Context) error {
		calls++
		return nil
	})

	if err != nil {
		t.Fatalf("Retry() returned error: %v", err)
	}
	if calls != 1 {
		t.Errorf("operation ran %d times, want 1", calls)
	}
}

func TestRetryRecoversAfterTransientFailures(t *testing.T) {
	calls := 0

	err := Retry(context.Background(), testPolicy(3, nil), func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("connection reset")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("Retry() returned error: %v", err)
	}
	if calls != 3 {
		t.Errorf("operation ran %d times, want 3", calls)
	}
}

func TestRetryGivesUpAfterAttemptsAndReturnsLastError(t *testing.T) {
	calls := 0
	lastErr := errors.New("still failing")

	err := Retry(context.Background(), testPolicy(3, nil), func(ctx context.Context) error {
		calls++
		return lastErr
	})

	if !errors.Is(err, lastErr) {
		t.Errorf("Retry() error = %v, want the last operation error", err)
	}
	if calls != 3 {
		t.Errorf("operation ran %d times, want 3", calls)
	}
}

func TestRetryStopsOnPermanentError(t *testing.T) {
	calls := 0
	rejected := errors.New("product not found")

	err := Retry(context.Background(), testPolicy(5, nil), func(ctx context.Context) error {
		calls++
		return Permanent(rejected)
	})

	if calls != 1 {
		t.Errorf("operation ran %d times, want 1 (permanent errors are not retried)", calls)
	}
	if !errors.Is(err, rejected) {
		t.Errorf("Retry() error = %v, want %v unwrapped from the permanent marker", err, rejected)
	}
	if IsPermanent(err) {
		t.Error("Retry() returned the permanent marker, want the plain error")
	}
}

func TestRetryBacksOffExponentiallyUpToMaxDelay(t *testing.T) {
	var slept []time.Duration

	_ = Retry(context.Background(), testPolicy(5, &slept), func(ctx context.Context) error {
		return errors.New("failing")
	})

	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		400 * time.Millisecond, // capped by MaxDelay
	}
	if len(slept) != len(want) {
		t.Fatalf("slept %d times, want %d", len(slept), len(want))
	}
	for i, delay := range want {
		if slept[i] != delay {
			t.Errorf("delay %d = %v, want %v", i, slept[i], delay)
		}
	}
}

func TestRetryStopsWhenContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	err := Retry(ctx, testPolicy(5, nil), func(ctx context.Context) error {
		calls++
		cancel()
		return errors.New("failing")
	})

	if calls != 1 {
		t.Errorf("operation ran %d times, want 1", calls)
	}
	if err == nil {
		t.Error("Retry() returned no error, want the last failure")
	}
}

func TestRetryNormalizesInvalidPolicies(t *testing.T) {
	calls := 0

	err := Retry(context.Background(), RetryPolicy{
		Attempts: 0,
		sleep:    func(ctx context.Context, d time.Duration) error { return nil },
		jitter:   func(d time.Duration) time.Duration { return 0 },
	}, func(ctx context.Context) error {
		calls++
		return errors.New("failing")
	})

	if calls != 1 {
		t.Errorf("operation ran %d times, want 1", calls)
	}
	if err == nil {
		t.Error("Retry() returned no error, want one")
	}
}

func TestPermanentIgnoresNil(t *testing.T) {
	if Permanent(nil) != nil {
		t.Error("Permanent(nil) is not nil")
	}
	if IsPermanent(nil) {
		t.Error("IsPermanent(nil) = true, want false")
	}
}

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	breaker := NewBreaker(3, time.Minute)
	now := time.Now()
	breaker.now = func() time.Time { return now }

	failing := func(ctx context.Context) error { return errors.New("dependency down") }
	for range 3 {
		if err := breaker.Do(context.Background(), failing); err == nil {
			t.Fatal("Do() returned no error, want the operation failure")
		}
	}

	if state := breaker.State(); state != "open" {
		t.Errorf("state = %q, want open", state)
	}

	calls := 0
	err := breaker.Do(context.Background(), func(ctx context.Context) error {
		calls++
		return nil
	})
	if !errors.Is(err, ErrBreakerOpen) {
		t.Errorf("Do() error = %v, want ErrBreakerOpen", err)
	}
	if calls != 0 {
		t.Errorf("operation ran %d times while open, want 0", calls)
	}
}

func TestBreakerResetsFailureCountOnSuccess(t *testing.T) {
	breaker := NewBreaker(3, time.Minute)
	failing := func(ctx context.Context) error { return errors.New("dependency down") }
	succeeding := func(ctx context.Context) error { return nil }

	_ = breaker.Do(context.Background(), failing)
	_ = breaker.Do(context.Background(), failing)
	_ = breaker.Do(context.Background(), succeeding)
	_ = breaker.Do(context.Background(), failing)
	_ = breaker.Do(context.Background(), failing)

	if state := breaker.State(); state != "closed" {
		t.Errorf("state = %q, want closed", state)
	}
}

func TestBreakerClosesAgainAfterSuccessfulProbe(t *testing.T) {
	breaker := NewBreaker(2, time.Minute)
	now := time.Now()
	breaker.now = func() time.Time { return now }

	failing := func(ctx context.Context) error { return errors.New("dependency down") }
	_ = breaker.Do(context.Background(), failing)
	_ = breaker.Do(context.Background(), failing)

	now = now.Add(2 * time.Minute)

	calls := 0
	err := breaker.Do(context.Background(), func(ctx context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("probe returned error: %v", err)
	}
	if calls != 1 {
		t.Errorf("probe ran %d times, want 1", calls)
	}
	if state := breaker.State(); state != "closed" {
		t.Errorf("state = %q, want closed", state)
	}
}

func TestBreakerStaysOpenWhenProbeFails(t *testing.T) {
	breaker := NewBreaker(2, time.Minute)
	now := time.Now()
	breaker.now = func() time.Time { return now }

	failing := func(ctx context.Context) error { return errors.New("dependency down") }
	_ = breaker.Do(context.Background(), failing)
	_ = breaker.Do(context.Background(), failing)

	now = now.Add(2 * time.Minute)
	_ = breaker.Do(context.Background(), failing)

	if state := breaker.State(); state != "open" {
		t.Errorf("state = %q, want open", state)
	}
	if err := breaker.Do(context.Background(), failing); !errors.Is(err, ErrBreakerOpen) {
		t.Errorf("Do() error = %v, want ErrBreakerOpen", err)
	}
}

func TestBreakerIgnoresPermanentErrors(t *testing.T) {
	breaker := NewBreaker(2, time.Minute)
	rejected := func(ctx context.Context) error { return Permanent(errors.New("invalid request")) }

	for range 5 {
		if err := breaker.Do(context.Background(), rejected); err == nil {
			t.Fatal("Do() returned no error, want the rejection")
		}
	}

	if state := breaker.State(); state != "closed" {
		t.Errorf("state = %q, want closed (rejections are not dependency failures)", state)
	}
}

func TestNewBreakerNormalizesArguments(t *testing.T) {
	breaker := NewBreaker(0, 0)

	if breaker.failureThreshold != 1 {
		t.Errorf("failureThreshold = %d, want 1", breaker.failureThreshold)
	}
	if breaker.cooldown != 5*time.Second {
		t.Errorf("cooldown = %v, want 5s", breaker.cooldown)
	}
}
