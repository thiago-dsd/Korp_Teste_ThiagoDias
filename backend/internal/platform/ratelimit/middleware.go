package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

// ErrTooManyRequests is the single answer to any throttled request. It says
// what to do and nothing about how the limit works.
var ErrTooManyRequests = apperr.New(apperr.KindTooManyRequests, "rate_limited",
	"Too many requests. Please wait a moment and try again.")

// KeyFunc decides who a request is counted against.
type KeyFunc func(r *http.Request) string

// ByUser counts against the signed in person, falling back to the address when
// there is no session yet.
//
// Counting authenticated traffic per user rather than per address matters: a
// whole office behind one address would otherwise share a single allowance,
// and one person could exhaust it for everyone.
func ByUser(r *http.Request) string {
	if user, err := authn.UserFrom(r.Context()); err == nil {
		return "user:" + user.ID.String()
	}
	return ByClientIP(r)
}

// ByClientIP counts against the address the request came from.
func ByClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "ip:" + r.RemoteAddr
	}
	return "ip:" + host
}

// ByAccount counts against a value read from the request, such as the email
// being signed in as. It is what stops an attacker spread over many addresses
// from working through the passwords of a single account.
func ByAccount(value string) string {
	return "account:" + strings.ToLower(strings.TrimSpace(value))
}

// Middleware throttles the handlers it wraps under the given policy.
func Middleware(limiter Limiter, policy Policy, key KeyFunc) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		if policy.Disabled() {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			decision := limiter.Allow(key(r), policy)
			writeHeaders(w, decision)

			if !decision.Allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(decision.RetryAfter.Seconds())))
				httpx.WriteError(w, r, ErrTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Exempt returns a middleware that skips throttling for the paths a service
// must always answer: health probes, which monitoring polls constantly, and the
// endpoints other services call, which are already authenticated by the shared
// token and would otherwise share one address between them.
func Exempt(inner httpx.Middleware, prefixes ...string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		throttled := inner(next)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, prefix := range prefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}
			throttled.ServeHTTP(w, r)
		})
	}
}

func writeHeaders(w http.ResponseWriter, decision Decision) {
	if decision.Limit <= 0 {
		return
	}

	w.Header().Set("RateLimit-Limit", strconv.Itoa(decision.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(decision.Remaining))
	w.Header().Set("RateLimit-Reset", strconv.Itoa(int(decision.Reset.Seconds())))
}
