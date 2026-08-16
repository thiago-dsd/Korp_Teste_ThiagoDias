// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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
	// RequestTimeout bounds how long a single HTTP request may run.
	RequestTimeout time.Duration
	// ShutdownTimeout bounds how long graceful shutdown may take.
	ShutdownTimeout time.Duration
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
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
		ServiceName:     serviceName,
		HTTPAddr:        stringVar(lookup, "HTTP_ADDR", ":8080"),
		DatabaseURL:     stringVar(lookup, "DATABASE_URL", ""),
		RabbitMQURL:     stringVar(lookup, "RABBITMQ_URL", ""),
		ServiceToken:    stringVar(lookup, "SERVICE_TOKEN", ""),
		AllowedOrigins:  listVar(lookup, "CORS_ALLOWED_ORIGINS", []string{"http://localhost:4200"}),
		StockServiceURL: stringVar(lookup, "STOCK_SERVICE_URL", "http://localhost:8081"),
		LogLevel:        strings.ToLower(stringVar(lookup, "LOG_LEVEL", "info")),
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
	if cfg.RabbitMQURL == "" {
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
	if !validLogLevels[cfg.LogLevel] {
		problems = append(problems, fmt.Sprintf("LOG_LEVEL %q is invalid (use debug, info, warn or error)", cfg.LogLevel))
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("config: %s", strings.Join(problems, "; "))
	}
	return cfg, nil
}

var validLogLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

func stringVar(lookup Loader, key, fallback string) string {
	if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
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
