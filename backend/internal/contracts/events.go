// Package contracts holds the events exchanged between the services. Both
// sides depend on this package, so a change to a payload is a change both
// compile against.
package contracts

import "github.com/google/uuid"

// Event types, which are also the routing keys on the broker.
const (
	// InvoicePrintRequested asks the stock service to debit the balances of an
	// invoice that started printing.
	InvoicePrintRequested = "invoice.print_requested"
	// StockDebited reports that every item of an invoice was debited.
	StockDebited = "stock.debited"
	// StockRejected reports that the debit did not happen, and why.
	StockRejected = "stock.rejected"
)

// PrintRequested is the payload of InvoicePrintRequested.
type PrintRequested struct {
	InvoiceID     uuid.UUID   `json:"invoice_id"`
	InvoiceNumber int64       `json:"invoice_number"`
	Items         []PrintItem `json:"items"`
}

// PrintItem is a product and the quantity to debit.
type PrintItem struct {
	ProductID uuid.UUID `json:"product_id"`
	Quantity  int       `json:"quantity"`
}

// Debited is the payload of StockDebited.
type Debited struct {
	InvoiceID uuid.UUID `json:"invoice_id"`
}

// Rejected is the payload of StockRejected. The message is written for the
// operator waiting in front of the screen.
type Rejected struct {
	InvoiceID uuid.UUID `json:"invoice_id"`
	Code      string    `json:"code"`
	Message   string    `json:"message"`
}

// Queue names used by the services.
const (
	// StockPrintRequestsQueue receives print requests for the stock service.
	StockPrintRequestsQueue = "stock.print_requests"
	// BillingStockResultsQueue receives debit results for the billing service.
	BillingStockResultsQueue = "billing.stock_results"
)
