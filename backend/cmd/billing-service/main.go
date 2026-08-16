// Command billing-service serves invoices and drives their printing.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thiagodias/korp-invoices/internal/billing"
	"github.com/thiagodias/korp-invoices/internal/billing/stockclient"
	"github.com/thiagodias/korp-invoices/internal/config"
	"github.com/thiagodias/korp-invoices/internal/contracts"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/health"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/idempotency"
	"github.com/thiagodias/korp-invoices/internal/platform/logging"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres"
)

const serviceName = "billing-service"

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

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, postgres.DefaultPoolOptions())
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	if err := postgres.Migrate(ctx, pool, billing.MigrationsFS, billing.MigrationsDir); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	logger.Info("database schema is up to date")

	rabbit, err := messaging.Connect(ctx, cfg.RabbitMQURL)
	if err != nil {
		return fmt.Errorf("connect to broker: %w", err)
	}
	defer func() { _ = rabbit.Close() }()

	outbox := messaging.NewOutbox(pool)
	relay := messaging.NewRelay(outbox, rabbit, logger, messaging.DefaultRelayOptions())
	go func() {
		if err := relay.Run(ctx); err != nil {
			logger.Error("outbox relay stopped with error", "error", err)
		}
	}()

	healthHandler := health.NewHandler(cfg.ServiceName,
		health.Check{Name: "database", Probe: pool.Ping},
		health.Check{Name: "broker", Probe: rabbit.Ping},
	)

	stock := stockclient.New(cfg.StockServiceURL, cfg.ServiceToken)
	logger.Info("stock service configured", "url", cfg.StockServiceURL)

	// Access tokens are verified with the public key of the identity service.
	verifier := authn.NewVerifier(cfg.JWKSURL())
	logger.Info("verifying access tokens", "jwks_url", cfg.JWKSURL())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", healthHandler.Live)
	mux.HandleFunc("GET /health/ready", healthHandler.Ready)

	invoices := billing.NewStore(pool)
	api := billing.NewAPI(billing.NewService(invoices, stock, invoices))
	api.Routes(mux, verifier)

	// Answers from the stock service close or reopen the invoice.
	resultConsumer := messaging.NewConsumer("billing.stock_results", pool, logger,
		billing.StockResultHandler(logger))
	go func() {
		spec := messaging.QueueSpec{
			Name:        contracts.BillingStockResultsQueue,
			RoutingKeys: []string{contracts.StockDebited, contracts.StockRejected},
		}
		if err := rabbit.Consume(ctx, spec, 10, resultConsumer.Handle); err != nil {
			logger.Error("stock result consumer stopped with error", "error", err)
		}
	}()

	// Invoices whose answer never arrived are reopened, so none stays stuck.
	go billing.NewReconciler(invoices, logger).Run(ctx)

	limiter := httpx.NewRateLimiter(120, time.Minute)
	middlewares := httpx.BaseMiddlewares(logger, cfg.AllowedOrigins, cfg.RequestTimeout, limiter)
	middlewares = append(middlewares, idempotency.Middleware(idempotency.NewPostgresStore(pool), logger))

	handler := httpx.Chain(mux, middlewares...)

	return httpx.Serve(ctx, httpx.ServerConfig{
		Addr:            cfg.HTTPAddr,
		Handler:         handler,
		Logger:          logger,
		RequestTimeout:  cfg.RequestTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
	})
}
