// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/thiagodias/korp-invoices/internal/platform/ratelimit"
)

// Config holds the settings shared by every service in the system.
type Config struct {
	// ServiceName identifies the running service in logs and messages.
	ServiceName string
	// HTTPAddr is the address the HTTP server listens on, e.g. ":8081".
	HTTPAddr string
	// DatabaseURL is the PostgreSQL connection string.
	DatabaseURL string
	// RabbitMQURL is the AMQP connection string.
	RabbitMQURL string
	// ServiceToken authenticates service-to-service HTTP calls.
	ServiceToken string
	// AllowedOrigins lists the origins accepted by the CORS middleware.
	AllowedOrigins []string
	// StockServiceURL is the base URL of the stock service, used by billing.
	StockServiceURL string
	// IdentityServiceURL is where the public keys that verify access tokens
	// are published.
	IdentityServiceURL string
	// RequestTimeout bounds how long a single HTTP request may run.
	RequestTimeout time.Duration
	// ShutdownTimeout bounds how long graceful shutdown may take.
	ShutdownTimeout time.Duration
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
	// RateLimits bounds how fast each category of operation may be called.
	RateLimits RateLimits
	// LoginMaxFailures and LoginLockout close an account under attack. They
	// are counted in the database, so every instance sees the same number.
	LoginMaxFailures int
	LoginLockout     time.Duration
}

// RateLimits holds one policy per category of operation. They are separate
// because the reasons to limit them are: signing in is guessed at, the
// assistant is paid for per call, writing touches the database and the broker,
// and reading is cheap and constant while a screen is open.
type RateLimits struct {
	// Read covers listings and single reads.
	Read ratelimit.Policy
	// Write covers anything that changes state.
	Write ratelimit.Policy
	// Auth covers signing in, registering and refreshing a session.
	Auth ratelimit.Policy
	// AI covers the drafting assistant, which costs money per call.
	AI ratelimit.Policy
	// Public bounds unauthenticated traffic per address, as a floor under the
	// per user limits above.
	Public ratelimit.Policy
}

// Loader reads a variable and reports whether it was set.
// os.LookupEnv satisfies it; tests provide a map-backed implementation.
type Loader func(key string) (string, bool)

// FromEnv reads configuration from the process environment.
func FromEnv(serviceName string) (Config, error) {
	return Load(serviceName, os.LookupEnv)
}

// Load builds a Config from the given loader, applying defaults and validation.
// All problems are reported at once so a misconfigured deployment fails fast.
func Load(serviceName string, lookup Loader) (Config, error) {
	if serviceName == "" {
		return Config{}, fmt.Errorf("config: service name is required")
	}

	var problems []string

	cfg := Config{
		ServiceName:        serviceName,
		HTTPAddr:           stringVar(lookup, "HTTP_ADDR", ":8080"),
		DatabaseURL:        stringVar(lookup, "DATABASE_URL", ""),
		RabbitMQURL:        stringVar(lookup, "RABBITMQ_URL", ""),
		ServiceToken:       stringVar(lookup, "SERVICE_TOKEN", ""),
		AllowedOrigins:     listVar(lookup, "CORS_ALLOWED_ORIGINS", []string{"http://localhost:4200"}),
		StockServiceURL:    stringVar(lookup, "STOCK_SERVICE_URL", "http://localhost:8081"),
		IdentityServiceURL: stringVar(lookup, "IDENTITY_SERVICE_URL", "http://localhost:8083"),
		LogLevel:           strings.ToLower(stringVar(lookup, "LOG_LEVEL", "info")),
	}

	requestTimeout, err := durationVar(lookup, "REQUEST_TIMEOUT", 15*time.Second)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.RequestTimeout = requestTimeout

	shutdownTimeout, err := durationVar(lookup, "SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.ShutdownTimeout = shutdownTimeout

	if cfg.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}
	// The identity service does not publish events, so it runs without a broker.
	if cfg.RabbitMQURL == "" && serviceName != "identity-service" {
		problems = append(problems, "RABBITMQ_URL is required")
	}
	// The token protects service-to-service endpoints, so an empty value is a
	// security problem rather than a convenience default.
	if cfg.ServiceToken == "" {
		problems = append(problems, "SERVICE_TOKEN is required")
	}
	if len(cfg.AllowedOrigins) == 0 {
		problems = append(problems, "CORS_ALLOWED_ORIGINS must not be empty")
	}
	if !strings.HasPrefix(cfg.StockServiceURL, "http://") && !strings.HasPrefix(cfg.StockServiceURL, "https://") {
		problems = append(problems, "STOCK_SERVICE_URL must be an http or https URL")
	}
	if !strings.HasPrefix(cfg.IdentityServiceURL, "http://") && !strings.HasPrefix(cfg.IdentityServiceURL, "https://") {
		problems = append(problems, "IDENTITY_SERVICE_URL must be an http or https URL")
	}
	if !validLogLevels[cfg.LogLevel] {
		problems = append(problems, fmt.Sprintf("LOG_LEVEL %q is invalid (use debug, info, warn or error)", cfg.LogLevel))
	}

	limits, limitProblems := loadRateLimits(lookup)
	cfg.RateLimits = limits
	problems = append(problems, limitProblems...)

	failures, err := intVar(lookup, "LOGIN_MAX_FAILURES", 10)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.LoginMaxFailures = failures

	lockout, err := durationVar(lookup, "LOGIN_LOCKOUT", 15*time.Minute)
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.LoginLockout = lockout

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("config: %s", strings.Join(problems, "; "))
	}
	return cfg, nil
}

// JWKSURL is where the identity service publishes its verification keys.
func (c Config) JWKSURL() string {
	return strings.TrimSuffix(c.IdentityServiceURL, "/") + "/.well-known/jwks.json"
}

// Default policies. They are deliberately far apart: a screen open on a
// listing makes many more calls than a person writing invoices, and neither
// looks anything like someone guessing passwords.
var defaultRateLimits = RateLimits{
	Read:  ratelimit.Policy{Requests: 300, Window: time.Minute, Burst: 60},
	Write: ratelimit.Policy{Requests: 60, Window: time.Minute, Burst: 15},
	// Signing in is deliberately generous per address: a whole office shares
	// one, and throttling it would lock out colleagues because one person
	// mistyped a password. What stops someone working through passwords is the
	// per account lockout, which counts in the database and does not punish
	// anybody else.
	Auth:   ratelimit.Policy{Requests: 30, Window: time.Minute, Burst: 10},
	AI:     ratelimit.Policy{Requests: 20, Window: time.Minute, Burst: 5},
	Public: ratelimit.Policy{Requests: 600, Window: time.Minute, Burst: 100},
}

// loadRateLimits reads the policies, which are written as "300/1m" or
// "300/1m,burst=60", and "off" to turn a category off entirely.
func loadRateLimits(lookup Loader) (RateLimits, []string) {
	var problems []string

	parse := func(name, key string, fallback ratelimit.Policy) ratelimit.Policy {
		policy, err := ratelimit.ParsePolicy(name, stringVar(lookup, key, ""), fallback)
		if err != nil {
			problems = append(problems, err.Error())
		}
		return policy
	}

	// One switch turns throttling off for an environment that does not want it,
	// such as a load test.
	if enabled := stringVar(lookup, "RATE_LIMIT_ENABLED", "true"); enabled == "false" {
		return RateLimits{}, nil
	}

	return RateLimits{
		Read:   parse("RATE_LIMIT_READ", "RATE_LIMIT_READ", defaultRateLimits.Read),
		Write:  parse("RATE_LIMIT_WRITE", "RATE_LIMIT_WRITE", defaultRateLimits.Write),
		Auth:   parse("RATE_LIMIT_AUTH", "RATE_LIMIT_AUTH", defaultRateLimits.Auth),
		AI:     parse("RATE_LIMIT_AI", "RATE_LIMIT_AI", defaultRateLimits.AI),
		Public: parse("RATE_LIMIT_PUBLIC", "RATE_LIMIT_PUBLIC", defaultRateLimits.Public),
	}, problems
}

var validLogLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

func stringVar(lookup Loader, key, fallback string) string {
	if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func intVar(lookup Loader, key string, fallback int) (int, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback, fmt.Errorf("%s must be a positive number", key)
	}
	return value, nil
}

func listVar(lookup Loader, key string, fallback []string) []string {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback
	}
	var values []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func durationVar(lookup Loader, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	raw = strings.TrimSpace(raw)
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return fallback, fmt.Errorf("%s must be a positive duration", key)
		}
		return time.Duration(seconds) * time.Second, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}
