package billing

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
)

// MaxProductCodeLength bounds the product code accepted as a filter.
const MaxProductCodeLength = 32

// Query describes a page of invoices.
//
// Each filter answers a question someone actually asks in front of this
// listing: where is invoice number N, what is still open, what was issued this
// month, which invoices used this product, and what failed and needs attention.
type Query struct {
	// Statuses filters by state. Several may be given at once, which is how
	// "everything not closed yet" is asked for.
	Statuses []string
	// Number finds one invoice by its number.
	Number *int64
	// CreatedFrom and CreatedTo bound when the invoice was issued.
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	// ProductID and ProductCode list the invoices that used a product, which
	// is how a balance is investigated.
	ProductID   *uuid.UUID
	ProductCode string
	// HasFailure separates the invoices whose last print attempt did not go
	// through from the ones that are simply open.
	HasFailure *bool
	// Order reads the listing from the newest number or from the oldest.
	Order pagination.Direction
	// Limit is how many invoices to return.
	Limit int
	// Cursor points at the end of the previous page.
	Cursor string
}

// ErrInvalidFilter reports a filter the service cannot work with.
var ErrInvalidFilter = apperr.Invalid("invalid_filter", "The listing filters are not valid.")

// ParseQuery reads and validates the filters from the query string values.
func ParseQuery(values map[string][]string) (Query, error) {
	get := func(key string) string {
		if list, ok := values[key]; ok && len(list) > 0 {
			return strings.TrimSpace(list[0])
		}
		return ""
	}

	details := map[string]string{}
	query := Query{Cursor: get("cursor")}

	// Status accepts a list, so "OPEN,PRINTING" is a single request.
	if raw := get("status"); raw != "" {
		for _, value := range strings.Split(raw, ",") {
			status := strings.ToUpper(strings.TrimSpace(value))
			if status == "" {
				continue
			}
			if !ValidStatus(status) {
				details["status"] = "must be OPEN, PRINTING or CLOSED"
				break
			}
			query.Statuses = append(query.Statuses, status)
		}
	}

	if raw := get("number"); raw != "" {
		number, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || number <= 0 {
			details["number"] = "must be a positive whole number"
		} else {
			query.Number = &number
		}
	}

	if raw := get("created_from"); raw != "" {
		moment, err := parseMoment(raw, false)
		if err != nil {
			details["created_from"] = err.Error()
		} else {
			query.CreatedFrom = &moment
		}
	}
	if raw := get("created_to"); raw != "" {
		// A date alone means the whole day, so the end of the range is the
		// last instant of it rather than midnight.
		moment, err := parseMoment(raw, true)
		if err != nil {
			details["created_to"] = err.Error()
		} else {
			query.CreatedTo = &moment
		}
	}
	if query.CreatedFrom != nil && query.CreatedTo != nil && query.CreatedFrom.After(*query.CreatedTo) {
		details["created_from"] = "must not be after created_to"
	}

	if raw := get("product_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			details["product_id"] = "must be a valid identifier"
		} else {
			query.ProductID = &id
		}
	}
	if raw := get("product_code"); raw != "" {
		if len([]rune(raw)) > MaxProductCodeLength {
			details["product_code"] = fmt.Sprintf("must have at most %d characters", MaxProductCodeLength)
		} else {
			query.ProductCode = raw
		}
	}

	if raw := get("has_failure"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			details["has_failure"] = "must be true or false"
		} else {
			query.HasFailure = &value
		}
	}

	// The newest invoice is what someone wants to see first.
	order, err := pagination.ParseDirection(get("order"), pagination.Descending)
	if err != nil {
		details["order"] = "must be asc or desc"
	} else {
		query.Order = order
	}

	limit, err := pagination.ParseLimit(get("limit"))
	if err != nil {
		details["limit"] = fmt.Sprintf("must be a number between 1 and %d", pagination.MaxLimit)
	} else {
		query.Limit = limit
	}

	if len(details) > 0 {
		return Query{}, ErrInvalidFilter.WithDetails(details)
	}
	return query, nil
}

// parseMoment accepts either a full timestamp or a plain date. A plain date is
// read in UTC, at the start of the day or at its very end.
func parseMoment(raw string, endOfDay bool) (time.Time, error) {
	if moment, err := time.Parse(time.RFC3339, raw); err == nil {
		return moment, nil
	}

	day, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, errText("must be a date (2026-08-16) or a timestamp (2026-08-16T10:00:00Z)")
	}
	if endOfDay {
		return day.Add(24*time.Hour - time.Nanosecond), nil
	}
	return day, nil
}

// errText is a small error carrying a field level message.
type errText string

func (e errText) Error() string { return string(e) }
