package stock

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
)

// SortField is a column the catalogue can be ordered by.
type SortField string

const (
	// SortByCode is the natural order of a catalogue.
	SortByCode SortField = "code"
	// SortByBalance answers "what is running out?", which is the other reason
	// someone opens this listing.
	SortByBalance SortField = "balance"
)

// MaxSearchLength bounds the search term.
const MaxSearchLength = 100

// Query describes a page of the catalogue.
//
// The filters exist to answer the questions an operator actually has in front
// of this screen: where is this product, and what is running out.
type Query struct {
	// Search matches code and description.
	Search string
	// MinBalance and MaxBalance bound the balance. A max of zero lists what is
	// out of stock, which is the reason the filter exists.
	MinBalance *int
	MaxBalance *int
	// Sort and Order decide how the page is read.
	Sort  SortField
	Order pagination.Direction
	// Limit is how many products to return.
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
	query := Query{Search: get("search"), Cursor: get("cursor")}

	if len([]rune(query.Search)) > MaxSearchLength {
		details["search"] = fmt.Sprintf("must have at most %d characters", MaxSearchLength)
	}

	if raw := get("min_balance"); raw != "" {
		value, err := parseBalance(raw)
		if err != nil {
			details["min_balance"] = err.Error()
		} else {
			query.MinBalance = &value
		}
	}
	if raw := get("max_balance"); raw != "" {
		value, err := parseBalance(raw)
		if err != nil {
			details["max_balance"] = err.Error()
		} else {
			query.MaxBalance = &value
		}
	}
	if query.MinBalance != nil && query.MaxBalance != nil && *query.MinBalance > *query.MaxBalance {
		details["min_balance"] = "must not be greater than max_balance"
	}

	switch SortField(strings.ToLower(get("sort"))) {
	case "", SortByCode:
		query.Sort = SortByCode
	case SortByBalance:
		query.Sort = SortByBalance
	default:
		details["sort"] = "must be code or balance"
	}

	// Ordering by code reads naturally from A to Z; ordering by balance is
	// asked for when looking for what is running out, so it starts at the
	// lowest balance either way.
	order, err := pagination.ParseDirection(get("order"), pagination.Ascending)
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

func parseBalance(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	switch {
	case err != nil:
		return 0, errText("must be a whole number")
	case value < 0:
		return 0, errText("must not be negative")
	case value > MaxBalance:
		return 0, errText("is too large")
	}
	return value, nil
}
