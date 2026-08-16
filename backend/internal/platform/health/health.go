// Package health exposes liveness and readiness endpoints.
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

// Check reports whether a dependency is usable.
type Check struct {
	Name  string
	Probe func(ctx context.Context) error
}

// Handler answers liveness and readiness probes for a service.
type Handler struct {
	serviceName string
	checks      []Check
	timeout     time.Duration
}

// NewHandler builds a health handler for the given dependencies.
func NewHandler(serviceName string, checks ...Check) *Handler {
	return &Handler{serviceName: serviceName, checks: checks, timeout: 2 * time.Second}
}

type statusResponse struct {
	Status       string            `json:"status"`
	Service      string            `json:"service"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// Live reports that the process is running. It never touches dependencies, so
// a failing database does not cause the container to be restarted.
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, r, http.StatusOK, statusResponse{Status: "ok", Service: h.serviceName})
}

// Ready reports whether every dependency answers, returning 503 otherwise so
// traffic is only routed to instances that can serve it.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	dependencies := make(map[string]string, len(h.checks))
	ready := true

	for _, check := range h.checks {
		if err := check.Probe(ctx); err != nil {
			// The reason stays in the logs; the payload only says it is down.
			dependencies[check.Name] = "down"
			ready = false
			continue
		}
		dependencies[check.Name] = "up"
	}

	status := http.StatusOK
	body := statusResponse{Status: "ok", Service: h.serviceName, Dependencies: dependencies}
	if !ready {
		status = http.StatusServiceUnavailable
		body.Status = "degraded"
	}
	httpx.WriteJSON(w, r, status, body)
}
