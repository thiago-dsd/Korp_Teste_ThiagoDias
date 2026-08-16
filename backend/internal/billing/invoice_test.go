package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

func TestNewItemInputsKeepsOrderAndValues(t *testing.T) {
	first, second := uuid.New(), uuid.New()

	items, err := NewItemInputs([]ItemInput{
		{ProductID: first, Quantity: 2},
		{ProductID: second, Quantity: 3},
	})
	if err != nil {
		t.Fatalf("NewItemInputs() returned error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].ProductID != first || items[0].Quantity != 2 {
		t.Errorf("items[0] = %+v, want product %v with quantity 2", items[0], first)
	}
	if items[1].ProductID != second || items[1].Quantity != 3 {
		t.Errorf("items[1] = %+v, want product %v with quantity 3", items[1], second)
	}
}

func TestNewItemInputsMergesRepeatedProducts(t *testing.T) {
	product := uuid.New()
	other := uuid.New()

	items, err := NewItemInputs([]ItemInput{
		{ProductID: product, Quantity: 2},
		{ProductID: other, Quantity: 1},
		{ProductID: product, Quantity: 5},
	})
	if err != nil {
		t.Fatalf("NewItemInputs() returned error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (the repeated product is merged)", len(items))
	}
	if items[0].ProductID != product || items[0].Quantity != 7 {
		t.Errorf("items[0] = %+v, want product %v with quantity 7", items[0], product)
	}
}

func TestNewItemInputsRejectsInvalidLines(t *testing.T) {
	product := uuid.New()

	tests := []struct {
		name      string
		inputs    []ItemInput
		wantField string
	}{
		{
			name:      "no items",
			inputs:    nil,
			wantField: "items",
		},
		{
			name:      "empty product",
			inputs:    []ItemInput{{ProductID: uuid.Nil, Quantity: 1}},
			wantField: "items[0].product_id",
		},
		{
			name:      "zero quantity",
			inputs:    []ItemInput{{ProductID: product, Quantity: 0}},
			wantField: "items[0].quantity",
		},
		{
			name:      "negative quantity",
			inputs:    []ItemInput{{ProductID: product, Quantity: -3}},
			wantField: "items[0].quantity",
		},
		{
			name:      "quantity too large",
			inputs:    []ItemInput{{ProductID: product, Quantity: MaxItemQuantity + 1}},
			wantField: "items[0].quantity",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewItemInputs(tc.inputs)
			if err == nil {
				t.Fatalf("NewItemInputs() returned no error, want one for %q", tc.wantField)
			}

			appErr := apperr.From(err)
			if appErr.Kind != apperr.KindInvalid {
				t.Errorf("Kind = %q, want %q", appErr.Kind, apperr.KindInvalid)
			}
			if _, reported := appErr.Details[tc.wantField]; !reported {
				t.Errorf("details = %v, want a message for %q", appErr.Details, tc.wantField)
			}
		})
	}
}

func TestNewItemInputsRejectsTooManyProducts(t *testing.T) {
	inputs := make([]ItemInput, 0, MaxItemsPerInvoice+1)
	for range MaxItemsPerInvoice + 1 {
		inputs = append(inputs, ItemInput{ProductID: uuid.New(), Quantity: 1})
	}

	_, err := NewItemInputs(inputs)
	if err == nil {
		t.Fatal("NewItemInputs() returned no error, want one")
	}
	if _, reported := apperr.From(err).Details["items"]; !reported {
		t.Errorf("details = %v, want a message for items", apperr.From(err).Details)
	}
}

func TestNewItemInputsRejectsMergedQuantityOverLimit(t *testing.T) {
	product := uuid.New()

	_, err := NewItemInputs([]ItemInput{
		{ProductID: product, Quantity: MaxItemQuantity},
		{ProductID: product, Quantity: 1},
	})
	if err == nil {
		t.Fatal("NewItemInputs() returned no error for a merged quantity over the limit, want one")
	}
}

func TestStartPrintingOnlyFromOpen(t *testing.T) {
	tests := []struct {
		status  Status
		wantErr bool
	}{
		{status: StatusOpen},
		{status: StatusPrinting, wantErr: true},
		{status: StatusClosed, wantErr: true},
	}

	startedAt := time.Now().UTC()
	for _, tc := range tests {
		invoice := Invoice{Status: tc.status}
		err := invoice.StartPrinting(startedAt)

		if tc.wantErr {
			if err == nil {
				t.Errorf("StartPrinting() from %s returned no error, want one", tc.status)
			}
			if !errors.Is(err, ErrInvoiceNotPrintable) {
				t.Errorf("StartPrinting() error = %v, want ErrInvoiceNotPrintable", err)
			}
			if invoice.Status != tc.status {
				t.Errorf("status = %s, want it unchanged", invoice.Status)
			}
			continue
		}
		if err != nil {
			t.Errorf("StartPrinting() returned error: %v", err)
		}
		if invoice.Status != StatusPrinting {
			t.Errorf("status = %s, want %s", invoice.Status, StatusPrinting)
		}
		if invoice.PrintingSince == nil {
			t.Error("PrintingSince is nil, want the moment printing started")
		}
	}
}

func TestCloseFinishesTheInvoice(t *testing.T) {
	printedAt := time.Now().UTC()

	invoice := Invoice{Status: StatusPrinting, FailureCode: "old", FailureMessage: "old failure"}
	if err := invoice.Close(printedAt); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if invoice.Status != StatusClosed {
		t.Errorf("status = %s, want %s", invoice.Status, StatusClosed)
	}
	if invoice.PrintedAt == nil || !invoice.PrintedAt.Equal(printedAt) {
		t.Errorf("PrintedAt = %v, want %v", invoice.PrintedAt, printedAt)
	}
	if invoice.FailureCode != "" || invoice.FailureMessage != "" {
		t.Error("a closed invoice still carries a failure reason, want it cleared")
	}
}

// An invoice reopened by the timeout can still receive a late confirmation:
// the stock was debited, so it must end up closed rather than staying open.
func TestCloseAcceptsAnInvoiceReopenedByTimeout(t *testing.T) {
	invoice := Invoice{Status: StatusOpen, FailureCode: printTimeoutCode}

	if err := invoice.Close(time.Now().UTC()); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if invoice.Status != StatusClosed {
		t.Errorf("status = %s, want %s", invoice.Status, StatusClosed)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	printedAt := time.Now().UTC()
	invoice := Invoice{Status: StatusClosed, PrintedAt: &printedAt}

	if err := invoice.Close(printedAt.Add(time.Hour)); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if !invoice.PrintedAt.Equal(printedAt) {
		t.Errorf("PrintedAt = %v, want the original %v", invoice.PrintedAt, printedAt)
	}
}

func TestReopenOnlyFromPrinting(t *testing.T) {
	startedAt := time.Now().UTC()
	invoice := Invoice{Status: StatusPrinting, PrintingSince: &startedAt}

	if err := invoice.Reopen("insufficient_balance", "Product balance is not enough."); err != nil {
		t.Fatalf("Reopen() returned error: %v", err)
	}
	if invoice.Status != StatusOpen {
		t.Errorf("status = %s, want %s", invoice.Status, StatusOpen)
	}
	if invoice.FailureCode != "insufficient_balance" {
		t.Errorf("FailureCode = %q, want the reason of the failure", invoice.FailureCode)
	}
	if invoice.FailureMessage == "" {
		t.Error("FailureMessage is empty, want a message for the operator")
	}
	if invoice.PrintingSince != nil {
		t.Error("PrintingSince is still set on a reopened invoice")
	}

	for _, status := range []Status{StatusOpen, StatusClosed} {
		invoice := Invoice{Status: status}
		if err := invoice.Reopen("code", "message"); !errors.Is(err, ErrInvalidStatusTransition) {
			t.Errorf("Reopen() from %s error = %v, want ErrInvalidStatusTransition", status, err)
		}
	}
}

func TestStartPrintingClearsPreviousFailure(t *testing.T) {
	invoice := Invoice{Status: StatusOpen, FailureCode: "insufficient_balance", FailureMessage: "not enough"}

	if err := invoice.StartPrinting(time.Now().UTC()); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}
	if invoice.FailureCode != "" || invoice.FailureMessage != "" {
		t.Error("a new attempt still carries the previous failure, want it cleared")
	}
}

func TestCanPrint(t *testing.T) {
	tests := map[Status]bool{
		StatusOpen:     true,
		StatusPrinting: false,
		StatusClosed:   false,
	}

	for status, want := range tests {
		if got := (Invoice{Status: status}).CanPrint(); got != want {
			t.Errorf("CanPrint() with status %s = %v, want %v", status, got, want)
		}
	}
}

func TestTotalQuantity(t *testing.T) {
	invoice := Invoice{Items: []Item{{Quantity: 2}, {Quantity: 5}}}

	if got := invoice.TotalQuantity(); got != 7 {
		t.Errorf("TotalQuantity() = %d, want 7", got)
	}
	if got := (Invoice{}).TotalQuantity(); got != 0 {
		t.Errorf("TotalQuantity() of an empty invoice = %d, want 0", got)
	}
}

func TestValidStatus(t *testing.T) {
	tests := map[string]bool{
		"OPEN":     true,
		"PRINTING": true,
		"CLOSED":   true,
		"open":     false,
		"":         false,
		"UNKNOWN":  false,
	}

	for value, want := range tests {
		if got := ValidStatus(value); got != want {
			t.Errorf("ValidStatus(%q) = %v, want %v", value, got, want)
		}
	}
}
