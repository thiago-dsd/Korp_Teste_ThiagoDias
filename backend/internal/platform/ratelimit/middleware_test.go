package ratelimit_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/ratelimit"
)

func okHandler(calls *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.WriteHeader(http.StatusOK)
	})
}

func request(path, address string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = address + ":54321"
	return r
}

func TestRefusesWithAConsistentAnswer(t *testing.T) {
	calls := 0
	policy := ratelimit.Policy{Name: "read", Requests: 60, Window: time.Minute, Burst: 1}
	handler := ratelimit.Middleware(ratelimit.NewTokenBucket(), policy, ratelimit.ByClientIP)(okHandler(&calls))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, request("/products", "10.0.0.1"))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request("/products", "10.0.0.1"))

	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
	if calls != 1 {
		t.Errorf("the handler ran %d times, want 1", calls)
	}

	retryAfter, err := strconv.Atoi(second.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Errorf("Retry-After = %q, want a number of seconds", second.Header().Get("Retry-After"))
	}
	if second.Header().Get("RateLimit-Limit") != "60" {
		t.Errorf("RateLimit-Limit = %q, want 60", second.Header().Get("RateLimit-Limit"))
	}

	// The answer is the same envelope as any other error, and says nothing
	// about how the limit is implemented.
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if body.Error.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", body.Error.Code)
	}
	for _, leaked := range []string{"bucket", "token", "10.0.0.1", "60/1m"} {
		if strings.Contains(second.Body.String(), leaked) {
			t.Errorf("the answer mentions %q, want nothing about the implementation", leaked)
		}
	}
}

func TestReportsWhatIsLeftWhileAllowing(t *testing.T) {
	calls := 0
	policy := ratelimit.Policy{Name: "read", Requests: 60, Window: time.Minute, Burst: 10}
	handler := ratelimit.Middleware(ratelimit.NewTokenBucket(), policy, ratelimit.ByClientIP)(okHandler(&calls))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request("/products", "10.0.0.1"))

	if remaining := recorder.Header().Get("RateLimit-Remaining"); remaining != "9" {
		t.Errorf("RateLimit-Remaining = %q, want 9", remaining)
	}
}

// Counting authenticated traffic per person keeps an office behind one address
// from sharing a single allowance.
func TestCountsSignedInPeopleSeparatelyBehindOneAddress(t *testing.T) {
	calls := 0
	policy := ratelimit.Policy{Name: "read", Requests: 60, Window: time.Minute, Burst: 1}
	handler := ratelimit.Middleware(ratelimit.NewTokenBucket(), policy, ratelimit.ByUser)(okHandler(&calls))

	for _, user := range []authn.User{{ID: uuid.New()}, {ID: uuid.New()}} {
		r := request("/products", "10.0.0.1")
		r = r.WithContext(authn.WithUser(r.Context(), user))

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)

		if recorder.Code != http.StatusOK {
			t.Errorf("a request from %v was refused, want each person to have their own allowance", user.ID)
		}
	}
}

func TestFallsBackToTheAddressWhenThereIsNoSession(t *testing.T) {
	calls := 0
	policy := ratelimit.Policy{Name: "auth", Requests: 60, Window: time.Minute, Burst: 1}
	handler := ratelimit.Middleware(ratelimit.NewTokenBucket(), policy, ratelimit.ByUser)(okHandler(&calls))

	handler.ServeHTTP(httptest.NewRecorder(), request("/auth/login", "10.0.0.1"))

	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, request("/auth/login", "10.0.0.1"))
	if refused.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want the address to be throttled when nobody is signed in", refused.Code)
	}

	other := httptest.NewRecorder()
	handler.ServeHTTP(other, request("/auth/login", "10.0.0.2"))
	if other.Code != http.StatusOK {
		t.Errorf("status = %d, want another address to have its own allowance", other.Code)
	}
}

// Health probes and the endpoints other services call must never be throttled:
// monitoring polls constantly and the print flow depends on service to service
// calls that all arrive from one address.
func TestExemptPathsAreNeverThrottled(t *testing.T) {
	calls := 0
	policy := ratelimit.Policy{Name: "public", Requests: 60, Window: time.Minute, Burst: 1}
	handler := ratelimit.Exempt(
		ratelimit.Middleware(ratelimit.NewTokenBucket(), policy, ratelimit.ByClientIP),
		"/health/", "/internal/", "/.well-known/",
	)(okHandler(&calls))

	for _, path := range []string{"/health/live", "/health/ready", "/internal/products", "/.well-known/jwks.json"} {
		for range 50 {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request(path, "10.0.0.1"))

			if recorder.Code != http.StatusOK {
				t.Fatalf("%s was throttled, want it always served", path)
			}
		}
	}

	// Everything else still is.
	throttled := httptest.NewRecorder()
	handler.ServeHTTP(throttled, request("/products", "10.0.0.1"))
	if throttled.Code != http.StatusOK {
		t.Fatalf("the first ordinary request was refused")
	}

	throttled = httptest.NewRecorder()
	handler.ServeHTTP(throttled, request("/products", "10.0.0.1"))
	if throttled.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want ordinary paths to still be throttled", throttled.Code)
	}
}

func TestServesEverythingWhenThePolicyIsOff(t *testing.T) {
	calls := 0
	handler := ratelimit.Middleware(ratelimit.NewTokenBucket(), ratelimit.Policy{Name: "off"}, ratelimit.ByClientIP)(
		okHandler(&calls))

	for range 100 {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request("/products", "10.0.0.1"))
		if recorder.Code != http.StatusOK {
			t.Fatal("a request was refused while throttling is turned off")
		}
	}
	if calls != 100 {
		t.Errorf("the handler ran %d times, want 100", calls)
	}
}

// After waiting what the answer asked for, the caller is served again.
func TestServesTheCallerAgainAfterTheWait(t *testing.T) {
	calls := 0
	policy := ratelimit.Policy{Name: "read", Requests: 600, Window: time.Minute, Burst: 1} // ten per second
	handler := ratelimit.Middleware(ratelimit.NewTokenBucket(), policy, ratelimit.ByClientIP)(okHandler(&calls))

	handler.ServeHTTP(httptest.NewRecorder(), request("/products", "10.0.0.1"))

	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, request("/products", "10.0.0.1"))
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want the second request refused", refused.Code)
	}

	wait, err := strconv.Atoi(refused.Header().Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After is not a number: %v", err)
	}
	time.Sleep(time.Duration(wait) * time.Second)

	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, request("/products", "10.0.0.1"))
	if allowed.Code != http.StatusOK {
		t.Errorf("status = %d, want the caller served after waiting as told", allowed.Code)
	}
}
