package metrics_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/metrics"
)

func scrape(t *testing.T, registry *metrics.Registry) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	registry.Handler()(recorder, httptest.NewRequest(http.MethodGet, "/internal/metrics", nil))
	return recorder.Body.String()
}

func TestRequestsAreCountedByMethodAndStatus(t *testing.T) {
	registry := metrics.NewRegistry("stock-service")
	handler := registry.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	for range 3 {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/products", nil))
	}

	body := scrape(t, registry)
	if !strings.Contains(body, `http_requests_total{service="stock-service",method="POST",status="201"} 3`) {
		t.Errorf("counter missing from the scrape:\n%s", body)
	}
}

// An id in a label would give the scrape an unbounded number of series, which
// is the usual way a metrics endpoint becomes the outage it was meant to warn
// about.
func TestPathsAreNotUsedAsLabels(t *testing.T) {
	registry := metrics.NewRegistry("billing-service")
	handler := registry.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	handler.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/invoices/8f14e45f-ceea-467a-9f8a-9a1c2b3d4e5f", nil))

	if strings.Contains(scrape(t, registry), "8f14e45f") {
		t.Error("the scrape carries a path with an id in it")
	}
}

// "Cannot tell" and "nothing is stuck" are different things to be woken up for.
func TestAGaugeThatCannotBeReadIsAbsentRatherThanZero(t *testing.T) {
	registry := metrics.NewRegistry("stock-service")
	registry.AddGauge("dead_letter_messages", "Messages given up on.", func() (float64, error) {
		return 0, errors.New("broker unreachable")
	})

	if strings.Contains(scrape(t, registry), "dead_letter_messages") {
		t.Error("an unreadable gauge was reported as a value")
	}
}

func TestGaugesAreReportedWhenTheyCanBeRead(t *testing.T) {
	registry := metrics.NewRegistry("stock-service")
	registry.AddGauge("outbox_stalled_messages", "Events needing a person.", func() (float64, error) {
		return 4, nil
	})

	if !strings.Contains(scrape(t, registry), `outbox_stalled_messages{service="stock-service"} 4`) {
		t.Error("gauge missing from the scrape")
	}
}

func TestRuntimeNumbersAreAlwaysReported(t *testing.T) {
	body := scrape(t, metrics.NewRegistry("identity-service"))

	for _, want := range []string{"go_goroutines", "go_memory_bytes"} {
		if !strings.Contains(body, want) {
			t.Errorf("%s missing from the scrape", want)
		}
	}
}
