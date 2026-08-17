// Package metrics exposes the few numbers worth watching, in the text format
// Prometheus scrapes.
//
// It is written by hand rather than pulled in as a dependency because what this
// system needs is small and specific: how much traffic each service is
// answering and, above all, how much work is stuck. The interesting numbers are
// already computed inside the services — the outbox knows what has not been
// published, the broker knows what was dead lettered — and until now they only
// reached the logs.
package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"sync"

	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

// Registry holds the counters a service reports.
type Registry struct {
	service string

	mu       sync.Mutex
	requests map[requestKey]int64

	gauges []gauge
}

type requestKey struct {
	method string
	status int
}

type gauge struct {
	name string
	help string
	read func() (float64, error)
}

// NewRegistry builds a registry for a service.
func NewRegistry(service string) *Registry {
	return &Registry{service: service, requests: map[requestKey]int64{}}
}

// AddGauge registers a number read at scrape time. Reading it can fail — the
// broker may be unreachable — and a failed read is reported as absent rather
// than as zero, because zero stuck messages and "cannot tell" are different
// things to be woken up for.
func (r *Registry) AddGauge(name, help string, read func() (float64, error)) {
	r.gauges = append(r.gauges, gauge{name: name, help: help, read: read})
}

// Middleware counts every answered request.
//
// Requests are counted by method and status only. The path is deliberately left
// out: invoice ids in a label would give the scrape an unbounded number of
// series, which is the usual way a metrics endpoint becomes the outage.
func (r *Registry) Middleware() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, request)

			r.mu.Lock()
			r.requests[requestKey{method: request.Method, status: recorder.status}]++
			r.mu.Unlock()
		})
	}
}

// Handler serves the metrics. It is registered on the internal routes: the
// numbers say how much work is stuck and how much traffic there is, which is
// nobody's business but the people running the system.
func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		fmt.Fprintf(w, "# HELP http_requests_total Requests answered by this service.\n")
		fmt.Fprintf(w, "# TYPE http_requests_total counter\n")

		r.mu.Lock()
		keys := make([]requestKey, 0, len(r.requests))
		for key := range r.requests {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].method != keys[j].method {
				return keys[i].method < keys[j].method
			}
			return keys[i].status < keys[j].status
		})
		for _, key := range keys {
			fmt.Fprintf(w, "http_requests_total{service=%q,method=%q,status=%q} %d\n",
				r.service, key.method, strconv.Itoa(key.status), r.requests[key])
		}
		r.mu.Unlock()

		for _, g := range r.gauges {
			value, err := g.read()
			if err != nil {
				// Absent rather than zero: "cannot tell" is not "nothing stuck".
				continue
			}
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", g.name, g.help, g.name)
			fmt.Fprintf(w, "%s{service=%q} %g\n", g.name, r.service, value)
		}

		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		fmt.Fprintf(w, "# HELP go_goroutines Goroutines currently running.\n# TYPE go_goroutines gauge\n")
		fmt.Fprintf(w, "go_goroutines{service=%q} %d\n", r.service, runtime.NumGoroutine())
		fmt.Fprintf(w, "# HELP go_memory_bytes Bytes of heap in use.\n# TYPE go_memory_bytes gauge\n")
		fmt.Fprintf(w, "go_memory_bytes{service=%q} %d\n", r.service, memory.HeapAlloc)
	}
}

// Routes registers the endpoint behind the service token.
func (r *Registry) Routes(mux *http.ServeMux, serviceToken string) {
	mux.Handle("GET /internal/metrics", httpx.RequireServiceToken(serviceToken)(r.Handler()))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}
