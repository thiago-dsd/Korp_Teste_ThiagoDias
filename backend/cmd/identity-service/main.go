// Command identity-service owns accounts and issues the tokens the other
// services trust.
package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/thiagodias/korp-invoices/internal/config"
	"github.com/thiagodias/korp-invoices/internal/identity"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/health"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/logging"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres"
	"github.com/thiagodias/korp-invoices/internal/platform/ratelimit"
	"github.com/thiagodias/korp-invoices/internal/platform/retention"
)

const serviceName = "identity-service"

func main() {
	if err := run(); err != nil {
		slog.Error("service stopped with error", "service", serviceName, "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.FromEnv(serviceName)
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.ServiceName, cfg.LogLevel)
	slog.SetDefault(logger)

	privateKey, generated, err := identity.LoadOrGeneratePrivateKey(os.Getenv("JWT_PRIVATE_KEY"))
	if err != nil {
		return fmt.Errorf("load signing key: %w", err)
	}
	if generated {
		logger.Warn("no JWT_PRIVATE_KEY configured; generated a temporary signing key. " +
			"Sessions will stop working when this service restarts.")
	}

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, postgres.DefaultPoolOptions())
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	if err := postgres.Migrate(ctx, pool, identity.MigrationsFS, identity.MigrationsDir); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	logger.Info("database schema is up to date")

	issuer := identity.NewTokenIssuer(privateKey)
	store := identity.NewStore(pool)
	service := identity.NewService(store, issuer).WithLockoutPolicy(identity.LockoutPolicy{
		MaxFailures: cfg.LoginMaxFailures,
		Lockout:     cfg.LoginLockout,
	})

	// The service verifies its own tokens with the key it holds, so it never
	// calls itself over HTTP.
	verifier := authn.NewVerifierWithKeys(publicKeysOf(issuer, &privateKey.PublicKey))

	healthHandler := health.NewHandler(cfg.ServiceName, health.Check{Name: "database", Probe: pool.Ping})

	// Throttling: a public floor per address, and per route allowances added by
	// the API. Health probes and the published key set are exempt, since the
	// other services read the key set to verify tokens.
	limiter := ratelimit.NewTokenBucket()
	publicLimit := ratelimit.Exempt(
		ratelimit.Middleware(limiter, cfg.RateLimits.Public, ratelimit.ByClientIP),
		"/health/", "/.well-known/",
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", healthHandler.Live)
	mux.HandleFunc("GET /health/ready", healthHandler.Ready)
	identity.NewAPI(service, issuer).Routes(mux, verifier, identity.Limits{
		Limiter: limiter,
		Auth:    cfg.RateLimits.Auth,
		Read:    cfg.RateLimits.Read,
		Write:   cfg.RateLimits.Write,
	})

	// Sessions that expired long ago are of no use to anyone.
	// Sessions expire on their own; the rows do not. Same runner as the other
	// services, so there is one place where retention is configured.
	go retention.NewRunner(cfg.Retention.Interval, logger,
		retention.Task{
			Name: "refresh_tokens",
			Run:  func(ctx context.Context) (int, error) { return store.DeleteExpiredTokens(ctx) },
		},
	).Run(ctx)

	middlewares := httpx.BaseMiddlewares(logger, cfg.AllowedOrigins, cfg.RequestTimeout)
	middlewares = append(middlewares, publicLimit)
	handler := httpx.Chain(mux, middlewares...)

	return httpx.Serve(ctx, httpx.ServerConfig{
		Addr:            cfg.HTTPAddr,
		Handler:         handler,
		Logger:          logger,
		RequestTimeout:  cfg.RequestTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
	})
}

func publicKeysOf(issuer *identity.TokenIssuer, public *rsa.PublicKey) map[string]*rsa.PublicKey {
	keys := map[string]*rsa.PublicKey{}
	for _, entry := range issuer.PublicJWKS()["keys"].([]map[string]string) {
		keys[entry["kid"]] = public
	}
	return keys
}
