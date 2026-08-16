// Package ratelimit throttles requests per caller.
//
// Operations differ in what they cost and in what abusing them achieves, so
// they are throttled by category rather than by one number for the whole
// service: signing in is guessed at, drafting an invoice is paid for by the
// call, writing touches the database and the broker, and reading is cheap and
// happens constantly while a screen is open.
package ratelimit

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Policy is how much a caller may do in a period.
type Policy struct {
	// Name identifies the category in logs.
	Name string
	// Requests is how many requests are allowed per Window.
	Requests int
	// Window is the period the allowance refills over.
	Window time.Duration
	// Burst is how many requests may happen back to back. A screen firing
	// several calls at once is normal, so the burst is what keeps the limit
	// from punishing it while still bounding the sustained rate.
	Burst int
}

// Disabled reports whether the policy lets everything through.
func (p Policy) Disabled() bool { return p.Requests <= 0 }

// ParsePolicy reads a policy written as "120/1m" or "120/1m,burst=40".
func ParsePolicy(name, raw string, fallback Policy) (Policy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		fallback.Name = name
		return fallback, nil
	}
	if strings.EqualFold(raw, "off") {
		return Policy{Name: name}, nil
	}

	parts := strings.Split(raw, ",")
	rate := strings.SplitN(strings.TrimSpace(parts[0]), "/", 2)
	if len(rate) != 2 {
		return Policy{}, fmt.Errorf("%s must look like 120/1m", name)
	}

	requests, err := strconv.Atoi(strings.TrimSpace(rate[0]))
	if err != nil || requests <= 0 {
		return Policy{}, fmt.Errorf("%s must allow a positive number of requests", name)
	}

	window, err := time.ParseDuration(strings.TrimSpace(rate[1]))
	if err != nil || window <= 0 {
		return Policy{}, fmt.Errorf("%s must have a positive window, such as 1m", name)
	}

	policy := Policy{Name: name, Requests: requests, Window: window, Burst: requests}
	for _, option := range parts[1:] {
		key, value, found := strings.Cut(strings.TrimSpace(option), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "burst") {
			return Policy{}, fmt.Errorf("%s has an option it does not understand: %q", name, option)
		}
		burst, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || burst <= 0 {
			return Policy{}, fmt.Errorf("%s must have a positive burst", name)
		}
		policy.Burst = burst
	}
	return policy, nil
}

// Decision is the answer for one request.
type Decision struct {
	// Allowed reports whether the request may proceed.
	Allowed bool
	// Limit and Remaining describe the allowance, for the response headers.
	Limit     int
	Remaining int
	// RetryAfter is how long the caller should wait before trying again.
	RetryAfter time.Duration
	// Reset is when the allowance is full again.
	Reset time.Duration
}

// Limiter decides whether a caller may perform one more request.
//
// It is an interface so the in-memory implementation can be replaced by a
// shared one, backed by a cache or the database, when the limit has to hold
// across instances rather than per process.
type Limiter interface {
	Allow(key string, policy Policy) Decision
}

// TokenBucket allows short bursts and a steady rate on top of them.
//
// The bucket refills continuously rather than resetting on a boundary, which
// avoids the burst a fixed window invites right after it turns over: with a
// window that resets at every minute, a caller can spend the whole allowance
// at :59 and again at :00.
type TokenBucket struct {
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// NewTokenBucket builds an in-memory limiter.
//
// The allowance is per process: two instances of a service each allow the
// configured rate. Limits are set with that in mind, and the interface above is
// where a shared implementation goes when the deployment needs one.
func NewTokenBucket() *TokenBucket {
	return &TokenBucket{now: time.Now, buckets: map[string]*bucket{}}
}

// Allow reports whether the caller may perform one more request.
func (t *TokenBucket) Allow(key string, policy Policy) Decision {
	if policy.Disabled() {
		return Decision{Allowed: true, Limit: 0, Remaining: 0}
	}

	capacity := float64(policy.Burst)
	if capacity <= 0 {
		capacity = float64(policy.Requests)
	}
	refillPerSecond := float64(policy.Requests) / policy.Window.Seconds()

	now := t.now()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.evictStale(now, policy.Window)

	// The key is namespaced by policy so a caller's reads and writes are
	// counted separately.
	entry, known := t.buckets[policy.Name+"\x00"+key]
	if !known {
		entry = &bucket{tokens: capacity, lastSeen: now}
		t.buckets[policy.Name+"\x00"+key] = entry
	} else {
		elapsed := now.Sub(entry.lastSeen).Seconds()
		entry.tokens = math.Min(entry.tokens+elapsed*refillPerSecond, capacity)
		entry.lastSeen = now
	}

	if entry.tokens < 1 {
		missing := 1 - entry.tokens
		retryAfter := time.Duration(missing / refillPerSecond * float64(time.Second))
		return Decision{
			Allowed:    false,
			Limit:      policy.Requests,
			Remaining:  0,
			RetryAfter: max(retryAfter, time.Second),
			Reset:      time.Duration((capacity - entry.tokens) / refillPerSecond * float64(time.Second)),
		}
	}

	entry.tokens--
	return Decision{
		Allowed:   true,
		Limit:     policy.Requests,
		Remaining: int(entry.tokens),
		Reset:     time.Duration((capacity - entry.tokens) / refillPerSecond * float64(time.Second)),
	}
}

// evictStale drops buckets that are full again and idle, so memory does not
// grow with every caller ever seen. The caller must hold the lock.
func (t *TokenBucket) evictStale(now time.Time, window time.Duration) {
	idleFor := 2 * window
	for key, entry := range t.buckets {
		if now.Sub(entry.lastSeen) > idleFor {
			delete(t.buckets, key)
		}
	}
}
