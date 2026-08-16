package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func policy(requests int, window time.Duration, burst int) Policy {
	return Policy{Name: "test", Requests: requests, Window: window, Burst: burst}
}

func TestAllowsTheBurstThenRefuses(t *testing.T) {
	limiter := NewTokenBucket()
	now := time.Now()
	limiter.now = func() time.Time { return now }

	rule := policy(60, time.Minute, 5)
	for attempt := 1; attempt <= 5; attempt++ {
		if decision := limiter.Allow("user:1", rule); !decision.Allowed {
			t.Fatalf("request %d was refused, want it allowed within the burst", attempt)
		}
	}

	decision := limiter.Allow("user:1", rule)
	if decision.Allowed {
		t.Fatal("the request after the burst was allowed, want it refused")
	}
	if decision.RetryAfter <= 0 {
		t.Error("the refusal does not say when to try again")
	}
	if decision.Remaining != 0 {
		t.Errorf("Remaining = %d, want 0", decision.Remaining)
	}
}

func TestRefillsOverTheWindow(t *testing.T) {
	limiter := NewTokenBucket()
	now := time.Now()
	limiter.now = func() time.Time { return now }

	rule := policy(60, time.Minute, 2) // one request per second
	limiter.Allow("user:1", rule)
	limiter.Allow("user:1", rule)
	if limiter.Allow("user:1", rule).Allowed {
		t.Fatal("the bucket allowed more than its burst")
	}

	// One second later exactly one request is affordable again.
	now = now.Add(time.Second)
	if !limiter.Allow("user:1", rule).Allowed {
		t.Error("the allowance did not refill after a second")
	}
	if limiter.Allow("user:1", rule).Allowed {
		t.Error("the allowance refilled faster than the configured rate")
	}
}

// A fixed window lets a caller spend the whole allowance at the end of one
// window and again at the start of the next; a bucket that refills gradually
// does not.
func TestDoesNotAllowDoubleTheRateAcrossAWindowBoundary(t *testing.T) {
	limiter := NewTokenBucket()
	now := time.Now()
	limiter.now = func() time.Time { return now }

	rule := policy(10, time.Minute, 10)
	for range 10 {
		limiter.Allow("user:1", rule)
	}

	// The window "turned over", but the bucket only earned what time gave it.
	now = now.Add(time.Minute)

	allowed := 0
	for range 20 {
		if limiter.Allow("user:1", rule).Allowed {
			allowed++
		}
	}
	if allowed > 10 {
		t.Errorf("%d requests were allowed right after the boundary, want at most 10", allowed)
	}
}

func TestCountsEachCallerSeparately(t *testing.T) {
	limiter := NewTokenBucket()
	rule := policy(60, time.Minute, 1)

	if !limiter.Allow("user:1", rule).Allowed {
		t.Fatal("the first caller was refused")
	}
	if !limiter.Allow("user:2", rule).Allowed {
		t.Error("the second caller was refused because of the first")
	}
	if limiter.Allow("user:1", rule).Allowed {
		t.Error("the first caller got more than its allowance")
	}
}

// Reading and writing have their own allowances, so a screen refreshing a
// listing cannot use up the budget for issuing invoices.
func TestCountsEachCategorySeparately(t *testing.T) {
	limiter := NewTokenBucket()
	read := Policy{Name: "read", Requests: 60, Window: time.Minute, Burst: 1}
	write := Policy{Name: "write", Requests: 60, Window: time.Minute, Burst: 1}

	limiter.Allow("user:1", read)
	if limiter.Allow("user:1", read).Allowed {
		t.Fatal("the read allowance was not spent")
	}
	if !limiter.Allow("user:1", write).Allowed {
		t.Error("writing was refused because reading was throttled")
	}
}

// Requests arriving at the same moment must not hand out more than the burst.
func TestConcurrentRequestsNeverExceedTheBurst(t *testing.T) {
	limiter := NewTokenBucket()
	now := time.Now()
	limiter.now = func() time.Time { return now }

	rule := policy(60, time.Minute, 10)

	const callers = 200
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)

	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			if limiter.Allow("user:1", rule).Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != 10 {
		t.Errorf("%d requests were allowed at once, want exactly the burst of 10", allowed)
	}
}

func TestForgetsCallersThatWentAway(t *testing.T) {
	limiter := NewTokenBucket()
	now := time.Now()
	limiter.now = func() time.Time { return now }

	rule := policy(60, time.Minute, 1)
	limiter.Allow("user:1", rule)

	now = now.Add(3 * time.Minute)
	limiter.Allow("user:2", rule)

	limiter.mu.Lock()
	tracked := len(limiter.buckets)
	limiter.mu.Unlock()

	if tracked != 1 {
		t.Errorf("%d callers are still tracked, want the idle one forgotten", tracked)
	}
}

func TestADisabledPolicyLetsEverythingThrough(t *testing.T) {
	limiter := NewTokenBucket()
	rule := Policy{Name: "off"}

	for range 1000 {
		if !limiter.Allow("user:1", rule).Allowed {
			t.Fatal("a request was refused by a disabled policy")
		}
	}
}

func TestParsePolicy(t *testing.T) {
	fallback := Policy{Requests: 10, Window: time.Minute, Burst: 10}

	tests := map[string]Policy{
		"":                {Name: "test", Requests: 10, Window: time.Minute, Burst: 10},
		"300/1m":          {Name: "test", Requests: 300, Window: time.Minute, Burst: 300},
		"300/1m,burst=60": {Name: "test", Requests: 300, Window: time.Minute, Burst: 60},
		" 5 / 30s ":       {Name: "test", Requests: 5, Window: 30 * time.Second, Burst: 5},
		"1000/1h":         {Name: "test", Requests: 1000, Window: time.Hour, Burst: 1000},
		"off":             {Name: "test"},
	}

	for raw, want := range tests {
		got, err := ParsePolicy("test", raw, fallback)
		if err != nil {
			t.Errorf("ParsePolicy(%q) returned error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("ParsePolicy(%q) = %+v, want %+v", raw, got, want)
		}
	}
}

func TestParsePolicyRejectsNonsense(t *testing.T) {
	for _, raw := range []string{"300", "300/", "/1m", "abc/1m", "0/1m", "-5/1m", "300/0s", "300/1m,burst=0", "300/1m,limit=5"} {
		if _, err := ParsePolicy("test", raw, Policy{}); err == nil {
			t.Errorf("ParsePolicy(%q) accepted the value, want an error", raw)
		}
	}
}

func TestRetryAfterIsNeverZero(t *testing.T) {
	limiter := NewTokenBucket()
	rule := policy(1, time.Hour, 1)

	limiter.Allow("user:1", rule)
	decision := limiter.Allow("user:1", rule)

	if decision.RetryAfter < time.Second {
		t.Errorf("RetryAfter = %v, want at least a second so the header is usable", decision.RetryAfter)
	}
}
