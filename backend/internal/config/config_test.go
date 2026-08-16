package config

import (
	"strings"
	"testing"
	"time"
)

func mapLoader(values map[string]string) Loader {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":  "postgres://user:pass@localhost:5432/stock",
		"RABBITMQ_URL":  "amqp://guest:guest@localhost:5672/",
		"SERVICE_TOKEN": "local-development-token",
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load("stock-service", mapLoader(validEnv()))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.ServiceName != "stock-service" {
		t.Errorf("ServiceName = %q, want %q", cfg.ServiceName, "stock-service")
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.RequestTimeout != 15*time.Second {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, 15*time.Second)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 10*time.Second)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "http://localhost:4200" {
		t.Errorf("AllowedOrigins = %v, want [http://localhost:4200]", cfg.AllowedOrigins)
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	env := validEnv()
	env["HTTP_ADDR"] = ":9001"
	env["LOG_LEVEL"] = "DEBUG"
	env["REQUEST_TIMEOUT"] = "45s"
	env["SHUTDOWN_TIMEOUT"] = "30"
	env["CORS_ALLOWED_ORIGINS"] = "http://localhost:4200, https://app.example.com ,"

	cfg, err := Load("billing-service", mapLoader(env))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.HTTPAddr != ":9001" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":9001")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.RequestTimeout != 45*time.Second {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, 45*time.Second)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 30*time.Second)
	}
	want := []string{"http://localhost:4200", "https://app.example.com"}
	if len(cfg.AllowedOrigins) != len(want) {
		t.Fatalf("AllowedOrigins = %v, want %v", cfg.AllowedOrigins, want)
	}
	for i, origin := range want {
		if cfg.AllowedOrigins[i] != origin {
			t.Errorf("AllowedOrigins[%d] = %q, want %q", i, cfg.AllowedOrigins[i], origin)
		}
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(map[string]string)
		wantDetail string
	}{
		{
			name:       "missing database url",
			mutate:     func(env map[string]string) { delete(env, "DATABASE_URL") },
			wantDetail: "DATABASE_URL is required",
		},
		{
			name:       "blank database url",
			mutate:     func(env map[string]string) { env["DATABASE_URL"] = "   " },
			wantDetail: "DATABASE_URL is required",
		},
		{
			name:       "missing broker url",
			mutate:     func(env map[string]string) { delete(env, "RABBITMQ_URL") },
			wantDetail: "RABBITMQ_URL is required",
		},
		{
			name:       "missing service token",
			mutate:     func(env map[string]string) { delete(env, "SERVICE_TOKEN") },
			wantDetail: "SERVICE_TOKEN is required",
		},
		{
			name:       "invalid log level",
			mutate:     func(env map[string]string) { env["LOG_LEVEL"] = "verbose" },
			wantDetail: "LOG_LEVEL",
		},
		{
			name:       "non positive request timeout",
			mutate:     func(env map[string]string) { env["REQUEST_TIMEOUT"] = "0" },
			wantDetail: "REQUEST_TIMEOUT must be a positive duration",
		},
		{
			name:       "unparsable shutdown timeout",
			mutate:     func(env map[string]string) { env["SHUTDOWN_TIMEOUT"] = "soon" },
			wantDetail: "SHUTDOWN_TIMEOUT must be a positive duration",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			tc.mutate(env)

			_, err := Load("stock-service", mapLoader(env))
			if err == nil {
				t.Fatalf("Load() returned no error, want one mentioning %q", tc.wantDetail)
			}
			if !strings.Contains(err.Error(), tc.wantDetail) {
				t.Errorf("Load() error = %q, want it to mention %q", err, tc.wantDetail)
			}
		})
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Load("stock-service", mapLoader(map[string]string{}))
	if err == nil {
		t.Fatal("Load() returned no error, want one")
	}
	for _, detail := range []string{"DATABASE_URL", "RABBITMQ_URL", "SERVICE_TOKEN"} {
		if !strings.Contains(err.Error(), detail) {
			t.Errorf("Load() error = %q, want it to mention %q", err, detail)
		}
	}
}

func TestLoadRequiresServiceName(t *testing.T) {
	if _, err := Load("", mapLoader(validEnv())); err == nil {
		t.Fatal("Load() returned no error for empty service name, want one")
	}
}

func TestLoadStockServiceURL(t *testing.T) {
	cfg, err := Load("billing-service", mapLoader(validEnv()))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.StockServiceURL != "http://localhost:8081" {
		t.Errorf("StockServiceURL = %q, want the default", cfg.StockServiceURL)
	}

	env := validEnv()
	env["STOCK_SERVICE_URL"] = "https://stock.internal:8443"
	cfg, err = Load("billing-service", mapLoader(env))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.StockServiceURL != "https://stock.internal:8443" {
		t.Errorf("StockServiceURL = %q, want the configured value", cfg.StockServiceURL)
	}

	env["STOCK_SERVICE_URL"] = "stock.internal"
	if _, err := Load("billing-service", mapLoader(env)); err == nil {
		t.Error("Load() accepted a URL without scheme, want an error")
	}
}
