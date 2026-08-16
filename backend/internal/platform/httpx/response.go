// Package httpx holds the HTTP building blocks shared by the services:
// JSON encoding, error translation, middlewares and server lifecycle.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// maxRequestBody bounds how much JSON a client may send, protecting the
// services from trivially large payloads.
const maxRequestBody = 1 << 20 // 1 MiB

// ErrorBody is the error payload returned by every endpoint.
type ErrorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// WriteJSON writes status and payload as JSON.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	if payload == nil || status == http.StatusNoContent {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// The status line is already sent, so the response cannot be fixed.
		slog.ErrorContext(r.Context(), "failed to encode response body", "error", err)
	}
}

// WriteError translates err into the standard error payload. Unexpected errors
// are logged with their cause and reported to the caller as a generic failure.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	appErr := apperr.From(err)
	if appErr == nil {
		appErr = apperr.Internal("internal_error", "An unexpected error occurred.")
	}
	status := StatusFor(appErr.Kind)

	if status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "request failed",
			"error", appErr.Error(),
			"code", appErr.Code,
			"path", r.URL.Path,
			"method", r.Method,
		)
	}

	WriteJSON(w, r, status, errorEnvelope{Error: ErrorBody{
		Code:      appErr.Code,
		Message:   appErr.Message,
		Details:   appErr.Details,
		RequestID: RequestIDFrom(r.Context()),
	}})
}

// StatusFor maps an error kind to an HTTP status code.
func StatusFor(kind apperr.Kind) int {
	switch kind {
	case apperr.KindInvalid:
		return http.StatusBadRequest
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindConflict:
		return http.StatusConflict
	case apperr.KindUnauthorized:
		return http.StatusUnauthorized
	case apperr.KindTooManyRequests:
		return http.StatusTooManyRequests
	case apperr.KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// DecodeJSON reads a JSON request body into dst, rejecting oversized bodies,
// unknown fields and trailing content.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body := http.MaxBytesReader(w, r.Body, maxRequestBody)

	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return apperr.Invalid("request_too_large", "Request body is too large.").WithCause(err)
		}
		return apperr.Invalid("invalid_json", "Request body is not valid JSON.").WithCause(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return apperr.Invalid("invalid_json", "Request body must contain a single JSON object.")
	}
	return nil
}
