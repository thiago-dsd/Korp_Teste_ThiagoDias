// Package logging builds the structured logger used by the services.
package logging

import (
	"io"
	"log/slog"
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
