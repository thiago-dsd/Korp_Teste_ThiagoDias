package stock_test

import (
	"net/url"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
	"github.com/thiagodias/korp-invoices/internal/stock"
)

func parse(t *testing.T, rawQuery string) (stock.Query, error) {
	t.Helper()

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("the test query string is malformed: %v", err)
	}
	return stock.ParseQuery(values)
}

func TestParseQueryDefaults(t *testing.T) {
	query, err := parse(t, "")
	if err != nil {
		t.Fatalf("ParseQuery() returned error: %v", err)
	}

	if query.Sort != stock.SortByCode {
		t.Errorf("Sort = %q, want the catalogue order", query.Sort)
	}
	if query.Order != pagination.Ascending {
		t.Errorf("Order = %q, want ascending", query.Order)
	}
	if query.Limit != pagination.DefaultLimit {
		t.Errorf("Limit = %d, want the default", query.Limit)
	}
	if query.MinBalance != nil || query.MaxBalance != nil {
		t.Error("balance bounds are set without being asked for")
	}
}

func TestParseQueryReadsEveryFilter(t *testing.T) {
	query, err := parse(t, "search=+bolt+&min_balance=1&max_balance=10&sort=balance&order=desc&limit=5&cursor=abc")
	if err != nil {
		t.Fatalf("ParseQuery() returned error: %v", err)
	}

	if query.Search != "bolt" {
		t.Errorf("Search = %q, want it trimmed", query.Search)
	}
	if query.MinBalance == nil || *query.MinBalance != 1 {
		t.Errorf("MinBalance = %v, want 1", query.MinBalance)
	}
	if query.MaxBalance == nil || *query.MaxBalance != 10 {
		t.Errorf("MaxBalance = %v, want 10", query.MaxBalance)
	}
	if query.Sort != stock.SortByBalance || query.Order != pagination.Descending {
		t.Errorf("sorting = %q %q, want balance desc", query.Sort, query.Order)
	}
	if query.Limit != 5 || query.Cursor != "abc" {
		t.Errorf("paging = %d %q, want 5 and abc", query.Limit, query.Cursor)
	}
}

// Asking for what is out of stock is the reason the balance filter exists, so
// a maximum of zero has to survive parsing rather than look like "not set".
func TestParseQueryKeepsAZeroMaximumBalance(t *testing.T) {
	query, err := parse(t, "max_balance=0")
	if err != nil {
		t.Fatalf("ParseQuery() returned error: %v", err)
	}

	if query.MaxBalance == nil || *query.MaxBalance != 0 {
		t.Fatalf("MaxBalance = %v, want 0", query.MaxBalance)
	}
}

func TestParseQueryRejectsInvalidFilters(t *testing.T) {
	tests := map[string]string{
		"min_balance=abc":              "min_balance",
		"min_balance=-1":               "min_balance",
		"max_balance=notanumber":       "max_balance",
		"min_balance=10&max_balance=1": "min_balance",
		"sort=price":                   "sort",
		"order=sideways":               "order",
		"limit=abc":                    "limit",
	}

	for rawQuery, field := range tests {
		t.Run(rawQuery, func(t *testing.T) {
			_, err := parse(t, rawQuery)
			if err == nil {
				t.Fatalf("ParseQuery(%q) returned no error, want one for %q", rawQuery, field)
			}

			appErr := apperr.From(err)
			if appErr.Kind != apperr.KindInvalid {
				t.Errorf("Kind = %q, want %q", appErr.Kind, apperr.KindInvalid)
			}
			if _, reported := appErr.Details[field]; !reported {
				t.Errorf("details = %v, want a message for %q", appErr.Details, field)
			}
		})
	}
}

func TestParseQueryReportsEveryProblemAtOnce(t *testing.T) {
	_, err := parse(t, "sort=price&order=sideways&min_balance=-2")

	details := apperr.From(err).Details
	for _, field := range []string{"sort", "order", "min_balance"} {
		if _, reported := details[field]; !reported {
			t.Errorf("details = %v, want a message for %q", details, field)
		}
	}
}

func TestParseQueryRejectsALongSearchTerm(t *testing.T) {
	long := make([]byte, stock.MaxSearchLength+1)
	for i := range long {
		long[i] = 'x'
	}

	if _, err := parse(t, "search="+string(long)); err == nil {
		t.Fatal("ParseQuery() accepted an oversized search term, want an error")
	}
}
