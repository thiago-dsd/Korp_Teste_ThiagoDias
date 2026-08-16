package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	limiter := NewRateLimiter(3, time.Second)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	for i := range 3 {
		if !limiter.Allow("10.0.0.1") {
			t.Fatalf("request %d was blocked, want allowed", i+1)
		}
	}
	if limiter.Allow("10.0.0.1") {
		t.Error("request 4 was allowed, want blocked")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	limiter := NewRateLimiter(2, time.Second)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	limiter.Allow("10.0.0.1")
	limiter.Allow("10.0.0.1")
	if limiter.Allow("10.0.0.1") {
		t.Fatal("request was allowed while the bucket is empty")
	}

	now = now.Add(600 * time.Millisecond)
	if !limiter.Allow("10.0.0.1") {
		t.Error("request was blocked after refill, want allowed")
	}
}

func TestRateLimiterIsolatesClients(t *testing.T) {
	limiter := NewRateLimiter(1, time.Second)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	if !limiter.Allow("10.0.0.1") {
		t.Fatal("first client was blocked on its first request")
	}
	if !limiter.Allow("10.0.0.2") {
		t.Error("second client was blocked by the first client's usage")
	}
}

func TestRateLimiterEvictsIdleClients(t *testing.T) {
	limiter := NewRateLimiter(1, time.Second)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	limiter.Allow("10.0.0.1")
	now = now.Add(5 * time.Second)
	limiter.Allow("10.0.0.2")

	limiter.mu.Lock()
	_, stillTracked := limiter.buckets["10.0.0.1"]
	limiter.mu.Unlock()

	if stillTracked {
		t.Error("idle client is still tracked, want it evicted")
	}
}

func TestRateLimiterMiddlewareReturnsTooManyRequests(t *testing.T) {
	limiter := NewRateLimiter(1, time.Second)
	handler := limiter.Middleware()(okHandler())

	newRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodGet, "/products", nil)
		request.RemoteAddr = "10.0.0.1:54321"
		return request
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, newRequest())
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, newRequest())
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header is missing")
	}
}

func TestNewRateLimiterNormalizesArguments(t *testing.T) {
	limiter := NewRateLimiter(0, 0)

	if limiter.limit != 1 {
		t.Errorf("limit = %d, want 1", limiter.limit)
	}
	if limiter.interval != time.Second {
		t.Errorf("interval = %v, want 1s", limiter.interval)
	}
}
