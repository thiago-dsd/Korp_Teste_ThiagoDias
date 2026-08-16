package httpx

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter throttles requests per client using a token bucket.
// It protects the services from accidental floods and trivial abuse.
type RateLimiter struct {
	limit    int           // burst size, in requests
	interval time.Duration // time to refill one token
	now      func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// NewRateLimiter allows limit requests per client per interval.
func NewRateLimiter(limit int, interval time.Duration) *RateLimiter {
	if limit < 1 {
		limit = 1
	}
	if interval <= 0 {
		interval = time.Second
	}
	return &RateLimiter{
		limit:    limit,
		interval: interval,
		now:      time.Now,
		buckets:  make(map[string]*bucket),
	}
}

// Allow reports whether the client may perform one more request now.
func (l *RateLimiter) Allow(client string) bool {
	now := l.now()
	refillPerSecond := float64(l.limit) / l.interval.Seconds()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.evictStale(now)

	current, ok := l.buckets[client]
	if !ok {
		l.buckets[client] = &bucket{tokens: float64(l.limit) - 1, lastSeen: now}
		return true
	}

	elapsed := now.Sub(current.lastSeen).Seconds()
	current.tokens = min(current.tokens+elapsed*refillPerSecond, float64(l.limit))
	current.lastSeen = now

	if current.tokens < 1 {
		return false
	}
	current.tokens--
	return true
}

// Middleware rejects requests above the limit with 429 and Retry-After.
func (l *RateLimiter) Middleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(clientIP(r)) {
				w.Header().Set("Retry-After", strconv.Itoa(max(int(l.interval.Seconds()), 1)))
				WriteJSON(w, r, http.StatusTooManyRequests, errorEnvelope{Error: ErrorBody{
					Code:      "rate_limited",
					Message:   "Too many requests. Please try again shortly.",
					RequestID: RequestIDFrom(r.Context()),
				}})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// evictStale drops buckets that are fully refilled and idle, keeping memory
// bounded without a background goroutine. The caller must hold the lock.
func (l *RateLimiter) evictStale(now time.Time) {
	idleFor := 2 * l.interval
	for client, b := range l.buckets {
		if now.Sub(b.lastSeen) > idleFor {
			delete(l.buckets, client)
		}
	}
}

// clientIP identifies the caller by remote address. Proxy headers are ignored
// on purpose: they are attacker controlled unless a trusted proxy sets them.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
