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
	InvoiceID     uuid.UUID `json:"invoice_id"`
	InvoiceNumber int64     `json:"invoice_number"`
	// Attempt identifies which print of this invoice is being asked for.
	//
	// An invoice can be printed more than once: the first attempt may time out
	// and be reopened while its request is still on its way. The stock service
	// echoes this number back, so billing can tell the answer it is waiting for
	// from one that belongs to an attempt it already gave up on.
	//
	// Zero means the sender does not know about attempts, which is how a
	// service running the previous version is recognised.
	Attempt int         `json:"attempt,omitempty"`
	Items   []PrintItem `json:"items"`
}

// PrintItem is a product and the quantity to debit.
type PrintItem struct {
	ProductID uuid.UUID `json:"product_id"`
	Quantity  int       `json:"quantity"`
}

// Debited is the payload of StockDebited. It reports a fact about the invoice
// rather than about one attempt: once the balances were taken they stay taken,
// whichever request asked for it.
type Debited struct {
	InvoiceID uuid.UUID `json:"invoice_id"`
	// Attempt is the print this answers, echoed from the request.
	Attempt int `json:"attempt,omitempty"`
}

// Rejected is the payload of StockRejected. The message is written for the
// operator waiting in front of the screen.
//
// Unlike a debit, a rejection is only true of the attempt that produced it: the
// balance it found missing may have been replenished before the next attempt.
type Rejected struct {
	InvoiceID uuid.UUID `json:"invoice_id"`
	// Attempt is the print this answers, echoed from the request.
	Attempt int    `json:"attempt,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Queue names used by the services.
const (
	// StockPrintRequestsQueue receives print requests for the stock service.
	StockPrintRequestsQueue = "stock.print_requests"
	// BillingStockResultsQueue receives debit results for the billing service.
	BillingStockResultsQueue = "billing.stock_results"
)
