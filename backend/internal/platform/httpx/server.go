package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// ServerConfig describes the HTTP server of a service.
type ServerConfig struct {
	Addr            string
	Handler         http.Handler
	Logger          *slog.Logger
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
}

// Serve runs the HTTP server until ctx is cancelled, then shuts it down
// gracefully so in-flight requests are allowed to finish.
func Serve(ctx context.Context, cfg ServerConfig) error {
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           cfg.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.RequestTimeout + 5*time.Second,
		WriteTimeout:      cfg.RequestTimeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		cfg.Logger.Info("http server listening", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	select {
	case err := <-serverErrors:
		if err != nil {
			return fmt.Errorf("http server failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		cfg.Logger.Info("shutting down http server")

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		return nil
	}
}
