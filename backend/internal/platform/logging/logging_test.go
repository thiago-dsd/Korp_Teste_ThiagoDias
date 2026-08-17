package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" info ":  slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"unknown": slog.LevelInfo,
		"":        slog.LevelInfo,
	}

	for input, want := range tests {
		if got := ParseLevel(input); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestNewTagsServiceAndHonoursLevel(t *testing.T) {
	var buffer strings.Builder
	logger := New(&buffer, "stock-service", "warn")

	logger.Info("this must be filtered out")
	logger.Warn("stock is low", "product_code", "P-1")

	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("logged %d lines, want 1 (info must be filtered at warn level)", len(lines))
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if entry["service"] != "stock-service" {
		t.Errorf("service = %v, want stock-service", entry["service"])
	}
	if entry["msg"] != "stock is low" {
		t.Errorf("msg = %v, want %q", entry["msg"], "stock is low")
	}
	if entry["product_code"] != "P-1" {
		t.Errorf("product_code = %v, want P-1", entry["product_code"])
	}
}

// Every line written while serving a request carries the correlation id, so a
// handler deep in the call stack does not have to remember to pass it.
func TestWithContextStampsTheCorrelationId(t *testing.T) {
	var buffer bytes.Buffer
	logger := WithContext(New(&buffer, "test-service", "info"))

	ctx := httpx.WithRequestID(context.Background(), "req-42")
	logger.InfoContext(ctx, "something happened")

	if !strings.Contains(buffer.String(), `"request_id":"req-42"`) {
		t.Errorf("log line does not carry the correlation id: %s", buffer.String())
	}
}

func TestWithContextLeavesLinesWithoutARequestAlone(t *testing.T) {
	var buffer bytes.Buffer
	logger := WithContext(New(&buffer, "test-service", "info"))

	logger.InfoContext(context.Background(), "started")

	if strings.Contains(buffer.String(), "request_id") {
		t.Errorf("expected no correlation id outside a request: %s", buffer.String())
	}
}
