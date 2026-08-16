// Package pagination implements cursor based paging.
//
// Pages are cut by the value of the sort key rather than by an offset, so a
// row inserted or removed between two requests never makes an item show up
// twice or disappear, and the query cost does not grow with the page number.
// The cursor is opaque on purpose: clients pass back what they received and
// the shape can change without breaking them.
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// Page size limits.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// ErrInvalidCursor reports a cursor that was not produced by this service.
var ErrInvalidCursor = apperr.Invalid("invalid_cursor",
	"The page cursor is not valid. Start the listing again.")

// Cursor points at the last item of a page.
type Cursor struct {
	// Key is the value of the column the listing is ordered by.
	Key string `json:"k"`
	// ID breaks ties when two rows share the same key.
	ID string `json:"i,omitempty"`
}

// Encode turns a cursor into the opaque string sent to clients.
func Encode(cursor Cursor) string {
	payload, err := json.Marshal(cursor)
	if err != nil {
		// Cursor only holds strings, so this cannot fail in practice.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// Decode reads a cursor produced by Encode.
func Decode(raw string) (Cursor, error) {
	if raw == "" {
		return Cursor{}, nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, ErrInvalidCursor.WithCause(err)
	}

	var cursor Cursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return Cursor{}, ErrInvalidCursor.WithCause(err)
	}
	if cursor.Key == "" {
		return Cursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

// NormalizeLimit keeps the page size inside what the service is willing to
// serve, so a client cannot ask for the whole table in one call.
func NormalizeLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	default:
		return limit
	}
}

// ParseLimit reads the limit from a query string value.
func ParseLimit(raw string) (int, error) {
	if raw == "" {
		return DefaultLimit, nil
	}

	var limit int
	if _, err := fmt.Sscanf(raw, "%d", &limit); err != nil || limit < 0 {
		return 0, apperr.Invalid("invalid_limit", "The page size is not valid.").
			WithDetails(map[string]string{"limit": fmt.Sprintf("must be a number between 1 and %d", MaxLimit)})
	}
	return NormalizeLimit(limit), nil
}
