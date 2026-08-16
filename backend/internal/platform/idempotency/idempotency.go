// Package idempotency makes repeated write requests safe: a request replayed
// with the same Idempotency-Key returns the original response instead of
// performing the work twice.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// HeaderKey is the header carrying the client generated key.
const HeaderKey = "Idempotency-Key"

// MaxKeyLength bounds the accepted key size.
const MaxKeyLength = 128

// StaleReservationAge is how long an unfinished reservation blocks the same
// key. After it, a new request may take the reservation over, so a crashed
// request never blocks a key forever.
const StaleReservationAge = 2 * time.Minute

// Record is a stored response for a key.
type Record struct {
	StatusCode int
	Body       []byte
}

// Store persists reservations and their responses.
type Store interface {
	// Reserve claims the key for the given endpoint and request hash.
	// It returns the stored response when the same request already finished,
	// ErrRequestInProgress when an equal request is still running and
	// ErrKeyReuse when the key was used for a different request.
	Reserve(ctx context.Context, endpoint, key, requestHash string) (*Record, error)
	// Complete stores the response produced for a reservation.
	Complete(ctx context.Context, endpoint, key string, record Record) error
	// Release drops a reservation, allowing the client to retry.
	Release(ctx context.Context, endpoint, key string) error
}

// Errors reported by the idempotency layer.
var (
	// ErrKeyReuse reports a key reused with a different request payload.
	ErrKeyReuse = apperr.Conflict("idempotency_key_reuse",
		"This Idempotency-Key was already used for a different request.")
	// ErrRequestInProgress reports a duplicate that is still being processed.
	ErrRequestInProgress = apperr.Conflict("request_in_progress",
		"An identical request is still being processed. Please retry shortly.")
	// ErrInvalidKey reports a malformed key.
	ErrInvalidKey = apperr.Invalid("invalid_idempotency_key",
		"Idempotency-Key must be up to 128 printable ASCII characters.")
)

// ValidateKey checks the format of a client provided key.
func ValidateKey(key string) error {
	if key == "" || len(key) > MaxKeyLength {
		return ErrInvalidKey
	}
	for _, char := range key {
		if char < '!' || char > '~' {
			return ErrInvalidKey
		}
	}
	return nil
}

// RequestHash fingerprints a request, so the same key can only be reused for
// an identical call.
func RequestHash(method, path string, body []byte) string {
	digest := sha256.New()
	digest.Write([]byte(method))
	digest.Write([]byte{0})
	digest.Write([]byte(path))
	digest.Write([]byte{0})
	digest.Write(body)
	return hex.EncodeToString(digest.Sum(nil))
}
