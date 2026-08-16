package stock

import (
	"strings"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

func TestNewProductNormalizesInput(t *testing.T) {
	product, err := NewProduct("  P-1 ", "  Steel   bolt\tM8 ", 10)
	if err != nil {
		t.Fatalf("NewProduct() returned error: %v", err)
	}

	if product.Code != "P-1" {
		t.Errorf("Code = %q, want %q", product.Code, "P-1")
	}
	if product.Description != "Steel bolt M8" {
		t.Errorf("Description = %q, want %q", product.Description, "Steel bolt M8")
	}
	if product.Balance != 10 {
		t.Errorf("Balance = %d, want 10", product.Balance)
	}
}

func TestNewProductRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		description string
		balance     int
		wantField   string
	}{
		{name: "empty code", code: "   ", description: "Bolt", balance: 1, wantField: "code"},
		{name: "code too long", code: strings.Repeat("A", 33), description: "Bolt", balance: 1, wantField: "code"},
		{name: "code with space", code: "P 1", description: "Bolt", balance: 1, wantField: "code"},
		{name: "code with slash", code: "P/1", description: "Bolt", balance: 1, wantField: "code"},
		{name: "empty description", code: "P-1", description: "  ", balance: 1, wantField: "description"},
		{name: "description too long", code: "P-1", description: strings.Repeat("x", 201), balance: 1, wantField: "description"},
		{name: "negative balance", code: "P-1", description: "Bolt", balance: -1, wantField: "balance"},
		{name: "balance too large", code: "P-1", description: "Bolt", balance: MaxBalance + 1, wantField: "balance"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProduct(tc.code, tc.description, tc.balance)
			if err == nil {
				t.Fatalf("NewProduct() returned no error, want one for field %q", tc.wantField)
			}

			appErr := apperr.From(err)
			if appErr.Kind != apperr.KindInvalid {
				t.Errorf("Kind = %q, want %q", appErr.Kind, apperr.KindInvalid)
			}
			if _, reported := appErr.Details[tc.wantField]; !reported {
				t.Errorf("details = %v, want a message for field %q", appErr.Details, tc.wantField)
			}
		})
	}
}

func TestNewProductReportsEveryInvalidFieldAtOnce(t *testing.T) {
	_, err := NewProduct("", "", -5)

	appErr := apperr.From(err)
	for _, field := range []string{"code", "description", "balance"} {
		if _, reported := appErr.Details[field]; !reported {
			t.Errorf("details = %v, want a message for field %q", appErr.Details, field)
		}
	}
}

func TestNewProductAcceptsZeroBalance(t *testing.T) {
	product, err := NewProduct("P-1", "Bolt", 0)
	if err != nil {
		t.Fatalf("NewProduct() returned error: %v", err)
	}
	if product.Balance != 0 {
		t.Errorf("Balance = %d, want 0", product.Balance)
	}
}

func TestUpdateChangesDescriptionAndBalance(t *testing.T) {
	product, err := NewProduct("P-1", "Bolt", 10)
	if err != nil {
		t.Fatalf("NewProduct() returned error: %v", err)
	}

	if err := product.Update("  Stainless  bolt ", 42); err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}

	if product.Description != "Stainless bolt" {
		t.Errorf("Description = %q, want %q", product.Description, "Stainless bolt")
	}
	if product.Balance != 42 {
		t.Errorf("Balance = %d, want 42", product.Balance)
	}
	if product.Code != "P-1" {
		t.Errorf("Code = %q, want it unchanged", product.Code)
	}
}

func TestUpdateRejectsInvalidInputAndKeepsState(t *testing.T) {
	product, err := NewProduct("P-1", "Bolt", 10)
	if err != nil {
		t.Fatalf("NewProduct() returned error: %v", err)
	}

	if err := product.Update("Bolt", -1); err == nil {
		t.Fatal("Update() returned no error for a negative balance, want one")
	}
	if product.Balance != 10 {
		t.Errorf("Balance = %d, want the previous value 10", product.Balance)
	}
}

func TestCanFulfill(t *testing.T) {
	product := Product{Balance: 5}

	tests := []struct {
		quantity int
		want     bool
	}{
		{quantity: 1, want: true},
		{quantity: 5, want: true},
		{quantity: 6, want: false},
		{quantity: 0, want: false},
		{quantity: -1, want: false},
	}

	for _, tc := range tests {
		if got := product.CanFulfill(tc.quantity); got != tc.want {
			t.Errorf("CanFulfill(%d) = %v, want %v", tc.quantity, got, tc.want)
		}
	}
}
