package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveIgnoresDependencies(t *testing.T) {
	handler := NewHandler("stock-service", Check{
		Name:  "database",
		Probe: func(ctx context.Context) error { return errors.New("database is down") },
	})

	recorder := httptest.NewRecorder()
	handler.Live(recorder, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestReadyReportsHealthyDependencies(t *testing.T) {
	handler := NewHandler("stock-service",
		Check{Name: "database", Probe: func(ctx context.Context) error { return nil }},
		Check{Name: "broker", Probe: func(ctx context.Context) error { return nil }},
	)

	recorder := httptest.NewRecorder()
	handler.Ready(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body statusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Dependencies["database"] != "up" || body.Dependencies["broker"] != "up" {
		t.Errorf("dependencies = %v, want both up", body.Dependencies)
	}
}

func TestReadyFailsWhenDependencyIsDown(t *testing.T) {
	handler := NewHandler("billing-service",
		Check{Name: "database", Probe: func(ctx context.Context) error { return nil }},
		Check{Name: "broker", Probe: func(ctx context.Context) error {
			return errors.New("dial tcp 10.0.0.5:5672: connection refused")
		}},
	)

	recorder := httptest.NewRecorder()
	handler.Ready(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(recorder.Body.String(), "10.0.0.5") {
		t.Errorf("body = %q, want internal details hidden", recorder.Body.String())
	}

	var body statusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	if body.Dependencies["broker"] != "down" {
		t.Errorf("broker = %q, want down", body.Dependencies["broker"])
	}
}
