// Package logging builds the structured logger used by the services.
package logging

import (
	"context"
	"io"
	"log/slog"

	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"strings"
)

// New returns a JSON logger tagged with the service name.
// Unknown levels fall back to info rather than failing at startup.
func New(out io.Writer, serviceName, level string) *slog.Logger {
	handler := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: ParseLevel(level)})
	return slog.New(handler).With("service", serviceName)
}

// ParseLevel converts a textual level into a slog level.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ContextHandler stamps every record with the correlation id carried by the
// context, so a line written deep inside a handler can be tied to the request
// that caused it without every call site remembering to pass it.
//
// It matters most where the work leaves the request: an invoice printed here is
// debited by another service minutes later, and the two logs are only one story
// if they share this id.
type ContextHandler struct {
	slog.Handler
}

// WithContext wraps a logger so its records carry the correlation id.
func WithContext(logger *slog.Logger) *slog.Logger {
	return slog.New(&ContextHandler{Handler: logger.Handler()})
}

// Handle adds the correlation id when the context carries one.
func (h *ContextHandler) Handle(ctx context.Context, record slog.Record) error {
	if id := httpx.RequestIDFrom(ctx); id != "" {
		record.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, record)
}

// WithAttrs keeps the wrapper in place when attributes are added.
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ContextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup keeps the wrapper in place when a group is opened.
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	return &ContextHandler{Handler: h.Handler.WithGroup(name)}
}
