package billing

import (
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

// Status is the lifecycle state of an invoice.
type Status string

const (
	// StatusOpen means the invoice can still be changed and printed.
	StatusOpen Status = "OPEN"
	// StatusPrinting means printing started and the stock debit is running.
	StatusPrinting Status = "PRINTING"
	// StatusClosed means the invoice was printed and the stock was debited.
	StatusClosed Status = "CLOSED"
)

// Limits protecting the service from unreasonable invoices.
const (
	MaxItemsPerInvoice = 100
	MaxItemQuantity    = 1_000_000
)

// Item is a product and the quantity used by an invoice. Code and description
// are a snapshot: renaming a product later must not rewrite printed invoices.
type Item struct {
	ID                 uuid.UUID
	ProductID          uuid.UUID
	ProductCode        string
	ProductDescription string
	Quantity           int
}

// Invoice is a fiscal note with sequential numbering.
type Invoice struct {
	ID     uuid.UUID
	Number int64
	Status Status
	Items  []Item
	// FailureCode and FailureMessage explain why the last print attempt did
	// not go through; both are cleared when a new attempt starts.
	FailureCode    string
	FailureMessage string
	// PrintingSince is when the current print attempt started, used to detect
	// attempts that never got an answer.
	PrintingSince *time.Time
	// PrintAttempt counts how many times this invoice was sent to print. It is
	// what tells the answer of the attempt being waited for from the answer of
	// an attempt that was already given up on.
	PrintAttempt int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	PrintedAt    *time.Time
}

// ItemInput is a requested invoice line, before product data is resolved.
type ItemInput struct {
	ProductID uuid.UUID
	Quantity  int
}

// NewItemInputs validates the requested lines and merges repeated products by
// summing their quantities, so an invoice never holds the same product twice.
func NewItemInputs(inputs []ItemInput) ([]ItemInput, error) {
	if len(inputs) == 0 {
		return nil, ErrInvalidInvoice.WithDetails(map[string]string{"items": "must contain at least one product"})
	}

	details := map[string]string{}
	merged := make([]ItemInput, 0, len(inputs))
	positionOf := make(map[uuid.UUID]int, len(inputs))

	for index, input := range inputs {
		field := "items[" + strconv.Itoa(index) + "]"

		if input.ProductID == uuid.Nil {
			details[field+".product_id"] = "must not be empty"
			continue
		}
		switch {
		case input.Quantity <= 0:
			details[field+".quantity"] = "must be greater than zero"
			continue
		case input.Quantity > MaxItemQuantity:
			details[field+".quantity"] = "is too large"
			continue
		}

		if position, repeated := positionOf[input.ProductID]; repeated {
			total := merged[position].Quantity + input.Quantity
			if total > MaxItemQuantity {
				details[field+".quantity"] = "is too large"
				continue
			}
			merged[position].Quantity = total
			continue
		}
		positionOf[input.ProductID] = len(merged)
		merged = append(merged, input)
	}

	if len(details) > 0 {
		return nil, ErrInvalidInvoice.WithDetails(details)
	}
	if len(merged) > MaxItemsPerInvoice {
		return nil, ErrInvalidInvoice.WithDetails(map[string]string{
			"items": "must contain at most " + strconv.Itoa(MaxItemsPerInvoice) + " distinct products",
		})
	}
	return merged, nil
}

// CanPrint reports whether printing may start.
func (i Invoice) CanPrint() bool { return i.Status == StatusOpen }

// StartPrinting moves an open invoice to PRINTING. Any other status is
// rejected, which is what keeps a closed invoice from being printed twice.
func (i *Invoice) StartPrinting(startedAt time.Time) error {
	if !i.CanPrint() {
		return ErrInvoiceNotPrintable.WithDetails(map[string]string{"status": string(i.Status)})
	}
	i.Status = StatusPrinting
	i.PrintingSince = &startedAt
	// Every attempt gets its own number, so an answer can be traced back to the
	// request that caused it.
	i.PrintAttempt++
	i.FailureCode = ""
	i.FailureMessage = ""
	return nil
}

// Close finishes a successful print. It also accepts an invoice that was
// reopened while the answer was on its way: the stock was already debited, so
// the invoice must end up closed either way.
func (i *Invoice) Close(printedAt time.Time) error {
	if i.Status == StatusClosed {
		return nil
	}
	i.Status = StatusClosed
	i.PrintedAt = &printedAt
	i.PrintingSince = nil
	i.FailureCode = ""
	i.FailureMessage = ""
	return nil
}

// Reopen returns an invoice to OPEN after a failed print, carrying the reason
// so the operator can fix the problem and try again.
func (i *Invoice) Reopen(code, message string) error {
	if i.Status != StatusPrinting {
		return ErrInvalidStatusTransition.WithDetails(map[string]string{"status": string(i.Status)})
	}
	i.Status = StatusOpen
	i.PrintingSince = nil
	i.FailureCode = code
	i.FailureMessage = message
	return nil
}

// TotalQuantity is the sum of the quantities of every item.
func (i Invoice) TotalQuantity() int {
	total := 0
	for _, item := range i.Items {
		total += item.Quantity
	}
	return total
}

// ValidStatus reports whether value is a known status.
func ValidStatus(value string) bool {
	switch Status(value) {
	case StatusOpen, StatusPrinting, StatusClosed:
		return true
	default:
		return false
	}
}

// Errors returned by the invoice domain.
var (
	// ErrInvalidInvoice reports validation failures on invoice data.
	ErrInvalidInvoice = apperr.Invalid("invalid_invoice", "Invoice data is invalid.")
	// ErrInvoiceNotFound reports an invoice that does not exist.
	ErrInvoiceNotFound = apperr.NotFound("invoice_not_found", "Invoice was not found.")
	// ErrInvoiceNotPrintable reports a print requested for an invoice that is
	// not open, such as one already printed.
	ErrInvoiceNotPrintable = apperr.Conflict("invoice_not_printable",
		"Only invoices with status OPEN can be printed.")
	// ErrInvalidStatusTransition reports an unsupported status change.
	ErrInvalidStatusTransition = apperr.Conflict("invalid_status_transition",
		"The invoice is not in a status that allows this operation.")
)
