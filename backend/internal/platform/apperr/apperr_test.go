package apperr

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFromWrapsUnknownErrorsAsInternal(t *testing.T) {
	cause := errors.New("connection refused to 10.0.0.1:5432")

	appErr := From(fmt.Errorf("query products: %w", cause))

	if appErr.Kind != KindInternal {
		t.Errorf("Kind = %q, want %q", appErr.Kind, KindInternal)
	}
	if strings.Contains(appErr.Message, "10.0.0.1") {
		t.Errorf("Message = %q, want it to hide internal details", appErr.Message)
	}
	if !errors.Is(appErr, cause) {
		t.Error("From() lost the original cause, want it preserved for logging")
	}
}

func TestFromKeepsDomainErrors(t *testing.T) {
	original := Conflict("insufficient_balance", "Product balance is not enough.")

	appErr := From(fmt.Errorf("debit stock: %w", original))

	if appErr != original {
		t.Fatalf("From() = %v, want the original domain error", appErr)
	}
	if appErr.Kind != KindConflict {
		t.Errorf("Kind = %q, want %q", appErr.Kind, KindConflict)
	}
}

func TestFromNilReturnsNil(t *testing.T) {
	if appErr := From(nil); appErr != nil {
		t.Errorf("From(nil) = %v, want nil", appErr)
	}
}

func TestWithDetailsAndCauseDoNotMutateOriginal(t *testing.T) {
	base := Invalid("validation_failed", "Request is invalid.")

	detailed := base.WithDetails(map[string]string{"code": "must not be empty"})
	caused := base.WithCause(errors.New("boom"))

	if base.Details != nil {
		t.Errorf("base.Details = %v, want nil", base.Details)
	}
	if base.Unwrap() != nil {
		t.Errorf("base cause = %v, want nil", base.Unwrap())
	}
	if detailed.Details["code"] != "must not be empty" {
		t.Errorf("detailed.Details = %v, want the provided details", detailed.Details)
	}
	if caused.Unwrap() == nil {
		t.Error("caused error lost its cause")
	}
}

func TestKindHelpers(t *testing.T) {
	tests := []struct {
		err  error
		want Kind
	}{
		{Invalid("c", "m"), KindInvalid},
		{NotFound("c", "m"), KindNotFound},
		{Conflict("c", "m"), KindConflict},
		{Unauthorized("c", "m"), KindUnauthorized},
		{Unavailable("c", "m"), KindUnavailable},
		{Internal("c", "m"), KindInternal},
		{errors.New("plain"), KindInternal},
	}

	for _, tc := range tests {
		if got := KindOf(tc.err); got != tc.want {
			t.Errorf("KindOf(%v) = %q, want %q", tc.err, got, tc.want)
		}
		if !IsKind(tc.err, tc.want) {
			t.Errorf("IsKind(%v, %q) = false, want true", tc.err, tc.want)
		}
	}

	if KindOf(nil) != "" {
		t.Errorf("KindOf(nil) = %q, want empty", KindOf(nil))
	}
}

func TestIsMatchesSentinelsAcrossCopies(t *testing.T) {
	sentinel := NotFound("product_not_found", "Product was not found.")

	withCause := sentinel.WithCause(errors.New("no rows in result set"))
	withDetails := sentinel.WithDetails(map[string]string{"id": "unknown"})
	wrapped := fmt.Errorf("load product: %w", withCause)

	for _, err := range []error{withCause, withDetails, wrapped} {
		if !errors.Is(err, sentinel) {
			t.Errorf("errors.Is(%v, sentinel) = false, want true", err)
		}
	}

	other := NotFound("invoice_not_found", "Invoice was not found.")
	if errors.Is(other, sentinel) {
		t.Error("errors.Is matched errors with different codes, want no match")
	}

	sameCodeOtherKind := Conflict("product_not_found", "Conflict.")
	if errors.Is(sameCodeOtherKind, sentinel) {
		t.Error("errors.Is matched errors with different kinds, want no match")
	}
}

func TestErrorMessageIncludesCode(t *testing.T) {
	err := NotFound("product_not_found", "Product was not found.")
	if !strings.Contains(err.Error(), "product_not_found") {
		t.Errorf("Error() = %q, want it to contain the code", err.Error())
	}
}
