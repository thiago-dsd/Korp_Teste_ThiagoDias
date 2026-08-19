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

	"github.com/thiagodias/korp-invoices/internal/billing"
	"github.com/thiagodias/korp-invoices/internal/billing/stockclient"
	"github.com/thiagodias/korp-invoices/internal/config"
	"github.com/thiagodias/korp-invoices/internal/contracts"
	"github.com/thiagodias/korp-invoices/internal/platform/aiclient"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/health"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/idempotency"
	"github.com/thiagodias/korp-invoices/internal/platform/logging"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
	"github.com/thiagodias/korp-invoices/internal/platform/metrics"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres"
	"github.com/thiagodias/korp-invoices/internal/platform/ratelimit"
	"github.com/thiagodias/korp-invoices/internal/platform/retention"
)

const serviceName = "billing-service"

// modelFromEnvironment reads the deployment the assistants share, and answers
// nil when none is configured — which is what turns them off.
func modelFromEnvironment(logger *slog.Logger) aiclient.Model {
	config := aiclient.Config{
		Endpoint:   os.Getenv("AZURE_AI_FOUNDRY_ENDPOINT"),
		APIKey:     os.Getenv("AZURE_AI_FOUNDRY_API_KEY"),
		Deployment: os.Getenv("AZURE_AI_FOUNDRY_DEPLOYMENT"),
		APIVersion: os.Getenv("AZURE_AI_FOUNDRY_API_VERSION"),
	}
	if !config.Configured() {
		logger.Info("assistants are disabled; no model is configured")
		return nil
	}

	logger.Info("assistants are enabled", "deployment", config.Deployment)
	return aiclient.NewFoundry(config)
}

// buildAssistants wires the drafting and the search assistants over the same
// deployment. A nil model leaves both reporting themselves unavailable, and
// the screens do without them.
func buildAssistants(catalogue billing.CatalogueReader, logger *slog.Logger) (*billing.DraftAssistant, *billing.SearchAssistant) {
	model := modelFromEnvironment(logger)
	if model == nil {
		return billing.NewDraftAssistant(nil, catalogue, logger), billing.NewSearchAssistant(nil, catalogue, logger)
	}
	return billing.NewDraftAssistant(model, catalogue, logger), billing.NewSearchAssistant(model, catalogue, logger)
}

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

	logger := logging.WithContext(logging.New(os.Stdout, cfg.ServiceName, cfg.LogLevel))
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

	invoices := billing.NewStore(pool)

	// The assistant is optional: without a deployment the screens simply do
	// not offer it, and everything else keeps working.
	assistant, search := buildAssistants(stock, logger)

	api := billing.NewAPI(billing.NewService(invoices, stock, invoices), assistant, search)
	api.Routes(mux, verifier, billing.Limits{
		Limiter: limiter,
		Read:    cfg.RateLimits.Read,
		Write:   cfg.RateLimits.Write,
		AI:      cfg.RateLimits.AI,
		Bulk:    cfg.RateLimits.Bulk,
	})

	// The numbers worth being woken up for: how much this service is answering
	// and how much work is stuck on its way out or already given up on.
	registry := metrics.NewRegistry(cfg.ServiceName)
	registry.AddGauge("outbox_pending_messages", "Events not yet published.", func() (float64, error) {
		pending, _, err := outbox.PendingCount(ctx)
		return float64(pending), err
	})
	registry.AddGauge("outbox_stalled_messages", "Events failing long enough to need a person.", func() (float64, error) {
		_, stalled, err := outbox.PendingCount(ctx)
		return float64(stalled), err
	})
	registry.AddGauge("dead_letter_messages", "Messages this service gave up on.", func() (float64, error) {
		depth, err := rabbit.QueueDepth(messaging.DeadLetterQueue(contracts.BillingStockResultsQueue))
		return float64(depth), err
	})
	registry.Routes(mux, cfg.ServiceToken)

	// What the consumer gave up on is visible and can be sent back once the
	// reason it failed is fixed.
	messaging.NewDeadLetterAPI(rabbit, logger, contracts.BillingStockResultsQueue).Routes(mux, cfg.ServiceToken)

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

	middlewares := httpx.BaseMiddlewares(logger, cfg.AllowedOrigins, cfg.RequestTimeout)
	idempotencyStore := idempotency.NewPostgresStore(pool)

	// The tables that make retries and redeliveries safe are also the ones that
	// only ever grow; cleaning them is what keeps that safety cheap.
	retentionRunner := retention.NewRunner(cfg.Retention.Interval, logger,
		retention.Task{
			Name: "idempotency_keys",
			Run: func(ctx context.Context) (int, error) {
				return idempotencyStore.DeleteCompletedBefore(ctx, cfg.Retention.Idempotency)
			},
		},
		retention.Task{
			Name: "outbox_messages",
			Run: func(ctx context.Context) (int, error) {
				return outbox.DeletePublishedBefore(ctx, cfg.Retention.Messaging)
			},
		},
		retention.Task{
			Name: "processed_messages",
			Run: func(ctx context.Context) (int, error) {
				return messaging.DeleteProcessedBefore(ctx, pool, cfg.Retention.Messaging)
			},
		},
	)
	go retentionRunner.Run(ctx)

	middlewares = append(middlewares, registry.Middleware(), publicLimit, idempotency.Middleware(idempotencyStore, logger))

	handler := httpx.Chain(mux, middlewares...)

	return httpx.Serve(ctx, httpx.ServerConfig{
		Addr:            cfg.HTTPAddr,
		Handler:         handler,
		Logger:          logger,
		RequestTimeout:  cfg.RequestTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
	})
}
