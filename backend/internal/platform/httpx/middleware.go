package httpx

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// RequestIDHeader carries the correlation id across services and responses.
const RequestIDHeader = "X-Request-Id"

// ServiceTokenHeader carries the shared secret on service-to-service calls.
const ServiceTokenHeader = "X-Service-Token"

type contextKey string

const requestIDKey contextKey = "request-id"

// Middleware decorates an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so the first one listed runs first.
func Chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// BaseMiddlewares returns the middleware stack every service applies, in the
// order they must run: correlate, log, protect, bound.
//
// Throttling is not here on purpose. It depends on what the endpoint does and,
// for authenticated traffic, on who is calling, so each service adds it around
// the routes it serves.
func BaseMiddlewares(logger *slog.Logger, allowedOrigins []string, requestTimeout time.Duration) []Middleware {
	return []Middleware{
		RequestID(),
		Logger(logger),
		Recover(logger),
		CORS(allowedOrigins),
		Timeout(requestTimeout),
	}
}

// RequestIDFrom returns the request id stored in ctx, or an empty string.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithRequestID returns a context carrying the given request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID reuses the incoming correlation id when it looks safe and
// generates a new one otherwise, then echoes it back in the response.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := sanitizeRequestID(r.Header.Get(RequestIDHeader))
			if id == "" {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
		})
	}
}

// Logger records one structured line per request.
func Logger(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(recorder, r)

			logger.LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", recorder.status),
				slog.Duration("duration", time.Since(started)),
			)
		})
	}
}

// Recover turns a panic into a 500 response instead of dropping the connection.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorContext(r.Context(), "recovered from panic",
						"panic", recovered,
						"path", r.URL.Path,
					)
					WriteError(w, r, apperr.Internal("internal_error", "An unexpected error occurred."))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout bounds how long a handler may take to produce a response.
func Timeout(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORS allows the configured origins only; anything else is served without
// CORS headers, so browsers block the response.
func CORS(allowedOrigins []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := origin != "" && slices.Contains(allowedOrigins, origin)

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers",
					strings.Join([]string{"Authorization", "Content-Type", "Idempotency-Key", RequestIDHeader}, ", "))
				w.Header().Set("Access-Control-Expose-Headers", RequestIDHeader)
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			w.Header().Add("Vary", "Origin")

			if r.Method == http.MethodOptions {
				status := http.StatusNoContent
				if !allowed {
					status = http.StatusForbidden
				}
				w.WriteHeader(status)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireServiceToken guards service-to-service endpoints with a shared secret.
func RequireServiceToken(token string) Middleware {
	expected := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := []byte(r.Header.Get(ServiceTokenHeader))
			if len(expected) == 0 || subtle.ConstantTimeCompare(provided, expected) != 1 {
				WriteError(w, r, apperr.Unauthorized("unauthorized", "Service token is missing or invalid."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.written {
		r.status = status
		r.written = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.written = true
	return r.ResponseWriter.Write(b)
}

func newRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand only fails in broken environments; a timestamp still
		// gives log lines something to correlate on.
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}

// sanitizeRequestID accepts only short, printable ASCII ids so a client cannot
// inject control characters into logs or response headers.
func sanitizeRequestID(id string) string {
	if len(id) == 0 || len(id) > 64 {
		return ""
	}
	for _, char := range id {
		isAllowed := char == '-' || char == '_' ||
			(char >= '0' && char <= '9') ||
			(char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z')
		if !isAllowed {
			return ""
		}
	}
	return id
}
