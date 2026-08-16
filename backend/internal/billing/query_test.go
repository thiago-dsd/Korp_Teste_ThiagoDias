package billing_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/billing"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
)

func parse(t *testing.T, rawQuery string) (billing.Query, error) {
	t.Helper()

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("the test query string is malformed: %v", err)
	}
	return billing.ParseQuery(values)
}

func TestParseQueryDefaults(t *testing.T) {
	query, err := parse(t, "")
	if err != nil {
		t.Fatalf("ParseQuery() returned error: %v", err)
	}

	if query.Order != pagination.Descending {
		t.Errorf("Order = %q, want the newest invoice first", query.Order)
	}
	if query.Limit != pagination.DefaultLimit {
		t.Errorf("Limit = %d, want the default", query.Limit)
	}
	if len(query.Statuses) != 0 || query.Number != nil || query.HasFailure != nil {
		t.Error("filters are set without being asked for")
	}
}

func TestParseQueryAcceptsSeveralStatuses(t *testing.T) {
	query, err := parse(t, "status=open,+printing")
	if err != nil {
		t.Fatalf("ParseQuery() returned error: %v", err)
	}

	if len(query.Statuses) != 2 || query.Statuses[0] != "OPEN" || query.Statuses[1] != "PRINTING" {
		t.Errorf("Statuses = %v, want OPEN and PRINTING", query.Statuses)
	}
}

func TestParseQueryReadsTheRemainingFilters(t *testing.T) {
	productID := uuid.New()
	rawQuery := "number=42&created_from=2026-08-01&created_to=2026-08-31" +
		"&product_id=" + productID.String() + "&product_code=P-1&has_failure=true&order=asc&limit=5"

	query, err := parse(t, rawQuery)
	if err != nil {
		t.Fatalf("ParseQuery() returned error: %v", err)
	}

	if query.Number == nil || *query.Number != 42 {
		t.Errorf("Number = %v, want 42", query.Number)
	}
	if query.CreatedFrom == nil || !query.CreatedFrom.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("CreatedFrom = %v, want the start of the day", query.CreatedFrom)
	}
	// A plain end date covers the whole day, not just its first instant.
	if query.CreatedTo == nil || query.CreatedTo.Day() != 31 || query.CreatedTo.Hour() != 23 {
		t.Errorf("CreatedTo = %v, want the end of the day", query.CreatedTo)
	}
	if query.ProductID == nil || *query.ProductID != productID {
		t.Errorf("ProductID = %v, want %v", query.ProductID, productID)
	}
	if query.ProductCode != "P-1" {
		t.Errorf("ProductCode = %q, want P-1", query.ProductCode)
	}
	if query.HasFailure == nil || !*query.HasFailure {
		t.Errorf("HasFailure = %v, want true", query.HasFailure)
	}
	if query.Order != pagination.Ascending || query.Limit != 5 {
		t.Errorf("paging = %q %d, want asc and 5", query.Order, query.Limit)
	}
}

func TestParseQueryAcceptsFullTimestamps(t *testing.T) {
	query, err := parse(t, "created_from=2026-08-16T10:30:00Z")
	if err != nil {
		t.Fatalf("ParseQuery() returned error: %v", err)
	}

	if query.CreatedFrom == nil || query.CreatedFrom.Hour() != 10 || query.CreatedFrom.Minute() != 30 {
		t.Errorf("CreatedFrom = %v, want the exact moment", query.CreatedFrom)
	}
}

func TestParseQueryRejectsInvalidFilters(t *testing.T) {
	tests := map[string]string{
		"status=PAID":            "status",
		"number=zero":            "number",
		"number=0":               "number",
		"number=-3":              "number",
		"created_from=yesterday": "created_from",
		"created_to=31-08-2026":  "created_to",
		"created_from=2026-08-31&created_to=2026-08-01": "created_from",
		"product_id=not-a-uuid":                         "product_id",
		"has_failure=maybe":                             "has_failure",
		"order=sideways":                                "order",
		"limit=abc":                                     "limit",
	}

	for rawQuery, field := range tests {
		t.Run(rawQuery, func(t *testing.T) {
			_, err := parse(t, rawQuery)
			if err == nil {
				t.Fatalf("ParseQuery(%q) returned no error, want one for %q", rawQuery, field)
			}
			if _, reported := apperr.From(err).Details[field]; !reported {
				t.Errorf("details = %v, want a message for %q", apperr.From(err).Details, field)
			}
		})
	}
}

func TestParseQueryRejectsALongProductCode(t *testing.T) {
	long := make([]byte, billing.MaxProductCodeLength+1)
	for i := range long {
		long[i] = 'x'
	}

	if _, err := parse(t, "product_code="+string(long)); err == nil {
		t.Fatal("ParseQuery() accepted an oversized product code, want an error")
	}
}
