// Command stock-service serves products and stock balances.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/thiagodias/korp-invoices/internal/config"
	"github.com/thiagodias/korp-invoices/internal/contracts"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/health"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/idempotency"
	"github.com/thiagodias/korp-invoices/internal/platform/logging"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres"
	"github.com/thiagodias/korp-invoices/internal/platform/ratelimit"
	"github.com/thiagodias/korp-invoices/internal/stock"
)

const serviceName = "stock-service"

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

	if err := postgres.Migrate(ctx, pool, stock.MigrationsFS, stock.MigrationsDir); err != nil {
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

	// Access tokens are verified with the public key of the identity service.
	verifier := authn.NewVerifier(cfg.JWKSURL())
	logger.Info("verifying access tokens", "jwks_url", cfg.JWKSURL())

	// Throttling: a public floor per address for anything unauthenticated, and
	// per user allowances on the routes themselves. Health probes and the
	// endpoints other services call are exempt, so monitoring and the print
	// flow are never throttled by the system's own traffic.
	limiter := ratelimit.NewTokenBucket()
	publicLimit := ratelimit.Exempt(
		ratelimit.Middleware(limiter, cfg.RateLimits.Public, ratelimit.ByClientIP),
		"/health/", "/internal/", "/.well-known/",
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", healthHandler.Live)
	mux.HandleFunc("GET /health/ready", healthHandler.Ready)

	failures := &stock.FailureSwitch{}
	api := stock.NewAPI(stock.NewService(stock.NewStore(pool)), failures)
	api.Routes(mux, verifier, stock.Limits{
		Limiter: limiter,
		Read:    cfg.RateLimits.Read,
		Write:   cfg.RateLimits.Write,
		Bulk:    cfg.RateLimits.Bulk,
	})
	api.InternalRoutes(mux, cfg.ServiceToken)

	// Print requests are consumed from the broker: the balances are debited
	// and the outcome is published back through this service's outbox.
	printConsumer := messaging.NewConsumer("stock.print_requests", pool, logger,
		stock.PrintRequestHandler(logger, failures))
	go func() {
		spec := messaging.QueueSpec{
			Name:        contracts.StockPrintRequestsQueue,
			RoutingKeys: []string{contracts.InvoicePrintRequested},
		}
		if err := rabbit.Consume(ctx, spec, 10, printConsumer.Handle); err != nil {
			logger.Error("print request consumer stopped with error", "error", err)
		}
	}()

	middlewares := httpx.BaseMiddlewares(logger, cfg.AllowedOrigins, cfg.RequestTimeout)
	middlewares = append(middlewares, publicLimit, idempotency.Middleware(idempotency.NewPostgresStore(pool), logger))

	handler := httpx.Chain(mux, middlewares...)

	return httpx.Serve(ctx, httpx.ServerConfig{
		Addr:            cfg.HTTPAddr,
		Handler:         handler,
		Logger:          logger,
		RequestTimeout:  cfg.RequestTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
	})
}
