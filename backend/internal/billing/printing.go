package billing

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/thiagodias/korp-invoices/internal/contracts"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
	"github.com/thiagodias/korp-invoices/internal/platform/resilience"
)

// PrintTimeout is how long an invoice may stay in PRINTING before the service
// assumes the answer was lost and reopens it.
const PrintTimeout = 2 * time.Minute

// Reasons reported to the operator when a print does not complete.
const (
	printTimeoutCode    = "print_timeout"
	printTimeoutMessage = "The stock service did not answer in time. The invoice is open again; please try printing once more."
)

// PrintStore is the persistence used by the printing use cases.
type PrintStore interface {
	StartPrinting(ctx context.Context, id uuid.UUID) (Invoice, error)
	ReopenStalePrintings(ctx context.Context, timeout time.Duration, code, message string) (int, error)
}

// RequestPrint starts printing an invoice. It returns as soon as the request
// is recorded: the balances are debited by the stock service, and the invoice
// is closed when the answer arrives.
func (s *Service) RequestPrint(ctx context.Context, id uuid.UUID) (Invoice, error) {
	return s.printing.StartPrinting(ctx, id)
}

// StockResultHandler applies the answer of the stock service to an invoice.
// It is written as a message handler so the status change and the record that
// the message was processed share one transaction.
func StockResultHandler(logger *slog.Logger) messaging.TxHandler {
	return func(ctx context.Context, tx pgx.Tx, message messaging.Message) error {
		switch message.Type {
		case contracts.StockDebited:
			var event contracts.Debited
			if err := message.Decode(&event); err != nil {
				return resilience.Permanent(err)
			}
			if err := CloseTx(ctx, tx, event.InvoiceID); err != nil {
				return err
			}
			logger.InfoContext(ctx, "invoice closed after stock debit", "invoice_id", event.InvoiceID)
			return nil

		case contracts.StockRejected:
			var event contracts.Rejected
			if err := message.Decode(&event); err != nil {
				return resilience.Permanent(err)
			}
			if err := ReopenTx(ctx, tx, event.InvoiceID, event.Attempt, event.Code, event.Message); err != nil {
				return err
			}
			logger.WarnContext(ctx, "invoice reopened after stock rejection",
				"invoice_id", event.InvoiceID, "attempt", event.Attempt, "reason", event.Code)
			return nil

		default:
			// An unknown type is not going to become known by retrying.
			logger.WarnContext(ctx, "ignoring unknown message type", "type", message.Type)
			return nil
		}
	}
}

// Reconciler reopens invoices whose print request never got an answer, for
// example because the stock service was down long enough for the message to be
// dead lettered. Without it an invoice could stay in PRINTING forever.
type Reconciler struct {
	store    PrintStore
	logger   *slog.Logger
	timeout  time.Duration
	interval time.Duration
}

// NewReconciler builds a reconciler with the default timings.
func NewReconciler(store PrintStore, logger *slog.Logger) *Reconciler {
	return &Reconciler{store: store, logger: logger, timeout: PrintTimeout, interval: 30 * time.Second}
}

// WithTimings replaces the timeout and interval, mainly to keep tests fast.
func (r *Reconciler) WithTimings(timeout, interval time.Duration) *Reconciler {
	r.timeout = timeout
	r.interval = interval
	return r
}

// Run reconciles stuck invoices until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	r.logger.Info("print reconciler started", "timeout", r.timeout, "interval", r.interval)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("print reconciler stopped")
			return
		case <-ticker.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce reopens every invoice that has been printing for too long.
func (r *Reconciler) RunOnce(ctx context.Context) {
	reopened, err := r.store.ReopenStalePrintings(ctx, r.timeout, printTimeoutCode, printTimeoutMessage)
	if err != nil {
		r.logger.ErrorContext(ctx, "failed to reconcile stuck invoices", "error", err)
		return
	}
	if reopened > 0 {
		r.logger.WarnContext(ctx, "reopened invoices stuck in printing", "count", reopened)
	}
}
