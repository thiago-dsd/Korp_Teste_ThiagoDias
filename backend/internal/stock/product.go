package stock

import (
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// Field limits mirror the database constraints, so invalid input is rejected
// with a helpful message before it ever reaches PostgreSQL.
const (
	MaxCodeLength        = 32
	MaxDescriptionLength = 200
	// MaxBalance keeps quantities inside the range of an INTEGER column.
	MaxBalance = 1_000_000_000
)

// Product is an item that can be sold through an invoice.
type Product struct {
	ID          uuid.UUID
	Code        string
	Description string
	Balance     int
	// Version is bumped by every write. A caller sends back the version it
	// read, which is how a change made in the meantime is noticed instead of
	// being silently overwritten.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewProduct validates and normalizes the data of a product to be created.
func NewProduct(code, description string, balance int) (Product, error) {
	details := map[string]string{}

	normalizedCode, err := normalizeCode(code)
	if err != nil {
		details["code"] = err.Error()
	}
	normalizedDescription, err := normalizeDescription(description)
	if err != nil {
		details["description"] = err.Error()
	}
	if err := validateBalance(balance); err != nil {
		details["balance"] = err.Error()
	}

	if len(details) > 0 {
		return Product{}, ErrInvalidProduct.WithDetails(details)
	}
	return Product{Code: normalizedCode, Description: normalizedDescription, Balance: balance}, nil
}

// Update applies new values to an existing product. The code is immutable:
// invoices reference products by code, so renaming one would rewrite history.
func (p *Product) Update(description string, balance int) error {
	details := map[string]string{}

	normalizedDescription, err := normalizeDescription(description)
	if err != nil {
		details["description"] = err.Error()
	}
	if err := validateBalance(balance); err != nil {
		details["balance"] = err.Error()
	}

	if len(details) > 0 {
		return ErrInvalidProduct.WithDetails(details)
	}

	p.Description = normalizedDescription
	p.Balance = balance
	return nil
}

// CanFulfill reports whether the product has enough balance for quantity.
func (p Product) CanFulfill(quantity int) bool {
	return quantity > 0 && p.Balance >= quantity
}

// Errors returned by the product domain.
var (
	// ErrInvalidProduct reports validation failures; the offending fields are
	// carried in the error details.
	ErrInvalidProduct = apperr.Invalid("invalid_product", "Product data is invalid.")
	// ErrProductChanged reports an edit based on a version that is no longer
	// current, which means somebody else changed the product first.
	ErrProductChanged = apperr.Conflict("product_changed",
		"This product changed while you were editing it. Reload it and try again.")
	// ErrProductNotFound reports a product that does not exist.
	ErrProductNotFound = apperr.NotFound("product_not_found", "Product was not found.")
	// ErrDuplicatedCode reports a product code already in use.
	ErrDuplicatedCode = apperr.Conflict("duplicated_product_code", "A product with this code already exists.")
	// ErrInsufficientBalance reports a debit larger than the available balance.
	ErrInsufficientBalance = apperr.Conflict("insufficient_balance", "Product balance is not enough.")
)

func normalizeCode(code string) (string, error) {
	code = strings.TrimSpace(code)
	switch {
	case code == "":
		return "", errText("must not be empty")
	case len([]rune(code)) > MaxCodeLength:
		return "", errText("must have at most 32 characters")
	}
	for _, char := range code {
		// A code identifies the product in printed invoices and in URLs, so it
		// is restricted to characters that survive both unchanged.
		isAllowed := unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_' || char == '.'
		if !isAllowed {
			return "", errText("must contain only letters, digits, dot, dash or underscore")
		}
	}
	return code, nil
}

func normalizeDescription(description string) (string, error) {
	description = strings.Join(strings.Fields(description), " ")
	switch {
	case description == "":
		return "", errText("must not be empty")
	case len([]rune(description)) > MaxDescriptionLength:
		return "", errText("must have at most 200 characters")
	}
	for _, char := range description {
		if unicode.IsControl(char) {
			return "", errText("must not contain control characters")
		}
	}
	return description, nil
}

func validateBalance(balance int) error {
	switch {
	case balance < 0:
		return errText("must not be negative")
	case balance > MaxBalance:
		return errText("is too large")
	}
	return nil
}

// errText is a small error carrying a field level message.
type errText string

func (e errText) Error() string { return string(e) }
