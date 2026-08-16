package idempotency

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

// maxBody bounds how much of a request body is read to compute its hash.
const maxBody = 1 << 20 // 1 MiB

// Middleware replays the stored response when a write request carries an
// Idempotency-Key that was already processed. Requests without the header are
// passed through untouched, so the header stays optional for clients that do
// not need the guarantee.
func Middleware(store Store, logger *slog.Logger) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(HeaderKey)
			if key == "" || !isWrite(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if err := ValidateKey(key); err != nil {
				httpx.WriteError(w, r, err)
				return
			}

			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
			if err != nil {
				httpx.WriteError(w, r, ErrInvalidKey.WithCause(err))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			endpoint := r.Method + " " + r.URL.Path
			hash := RequestHash(r.Method, r.URL.Path, body)

			stored, err := store.Reserve(r.Context(), endpoint, key, hash)
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			if stored != nil {
				replay(w, r, *stored)
				return
			}

			recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)

			// Server side failures are not cached: the client must be able to
			// retry the very same request and get a real attempt.
			if recorder.status >= http.StatusInternalServerError {
				if err := store.Release(context.WithoutCancel(r.Context()), endpoint, key); err != nil {
					logger.ErrorContext(r.Context(), "failed to release idempotency key", "error", err)
				}
				return
			}

			record := Record{StatusCode: recorder.status, Body: recorder.body.Bytes()}
			if err := store.Complete(context.WithoutCancel(r.Context()), endpoint, key, record); err != nil {
				// The response was already produced, so the request succeeded;
				// only the replay guarantee is lost.
				logger.ErrorContext(r.Context(), "failed to store idempotent response", "error", err)
			}
		})
	}
}

func replay(w http.ResponseWriter, r *http.Request, record Record) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Idempotent-Replay", "true")
	w.WriteHeader(record.StatusCode)

	if len(record.Body) > 0 {
		if _, err := w.Write(record.Body); err != nil {
			slog.ErrorContext(r.Context(), "failed to write replayed response", "error", err)
		}
	}
}

func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// responseRecorder captures the response so it can be replayed later.
type responseRecorder struct {
	http.ResponseWriter
	status  int
	body    bytes.Buffer
	written bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if !r.written {
		r.status = status
		r.written = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.written = true
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}
