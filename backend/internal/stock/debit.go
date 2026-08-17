package stock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/thiagodias/korp-invoices/internal/contracts"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
	"github.com/thiagodias/korp-invoices/internal/platform/resilience"
)

// ErrSimulatedFailure is returned while the failure switch is on. It exists to
// demonstrate how the system behaves when the stock service cannot do its job.
var ErrSimulatedFailure = apperr.Unavailable("simulated_failure",
	"Stock failure simulation is enabled.")

// FailureSwitch turns a simulated stock failure on and off at runtime, so the
// failure scenario can be shown without stopping the service.
type FailureSwitch struct {
	enabled atomic.Bool
}

// Enable turns the simulated failure on or off.
func (s *FailureSwitch) Enable(enabled bool) { s.enabled.Store(enabled) }

// Enabled reports whether the simulated failure is on.
func (s *FailureSwitch) Enabled() bool { return s.enabled.Load() }

// DebitItem is a product and the quantity to take from its balance.
type DebitItem struct {
	ProductID uuid.UUID
	Quantity  int
}

// DebitTx takes the quantities of an invoice from the product balances inside
// the caller's transaction. It reports a domain error when the invoice cannot
// be fulfilled, and does not touch any balance in that case.
//
// The debit is idempotent per invoice: a second request for an invoice already
// debited changes nothing and succeeds.
func DebitTx(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, items []DebitItem) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO stock_debits (invoice_id) VALUES ($1)
		ON CONFLICT (invoice_id) DO NOTHING`, invoiceID)
	if err != nil {
		return fmt.Errorf("record stock debit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Already debited by an earlier delivery of the same request.
		return nil
	}

	// Products are debited in a stable order so two invoices touching the same
	// products can never deadlock each other.
	ordered := slices.Clone(items)
	slices.SortFunc(ordered, func(a, b DebitItem) int {
		return strings.Compare(a.ProductID.String(), b.ProductID.String())
	})

	for _, item := range ordered {
		if item.Quantity <= 0 {
			return ErrInvalidProduct.WithDetails(map[string]string{
				"quantity": "must be greater than zero",
			})
		}

		// The condition on balance is what makes concurrent debits safe: the
		// database rejects the update instead of letting the balance go
		// negative, no matter how many invoices try at the same time.
		tag, err := tx.Exec(ctx, `
			UPDATE products
			SET balance = balance - $2, updated_at = now()
			WHERE id = $1 AND balance >= $2`, item.ProductID, item.Quantity)
		if err != nil {
			return fmt.Errorf("debit product balance: %w", err)
		}
		if tag.RowsAffected() == 1 {
			continue
		}

		// Nothing was updated: either the product is gone or the balance is
		// not enough. The difference matters to the operator.
		var balance int
		err = tx.QueryRow(ctx, `SELECT balance FROM products WHERE id = $1`, item.ProductID).Scan(&balance)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProductNotFound.WithDetails(map[string]string{
					"product_id": item.ProductID.String(),
				})
			}
			return fmt.Errorf("read product balance: %w", err)
		}
		return ErrInsufficientBalance.WithDetails(map[string]string{
			"product_id": item.ProductID.String(),
			"available":  fmt.Sprint(balance),
			"required":   fmt.Sprint(item.Quantity),
		})
	}
	return nil
}

// PrintRequestHandler debits the balances of an invoice and answers with the
// outcome. The debit, the answer and the record that the message was processed
// are committed together, so the stock service never debits without telling
// billing about it.
func PrintRequestHandler(logger *slog.Logger, failures *FailureSwitch) messaging.TxHandler {
	return func(ctx context.Context, tx pgx.Tx, message messaging.Message) error {
		if message.Type != contracts.InvoicePrintRequested {
			logger.WarnContext(ctx, "ignoring unknown message type", "type", message.Type)
			return nil
		}

		var event contracts.PrintRequested
		if err := message.Decode(&event); err != nil {
			return resilience.Permanent(err)
		}
		if failures != nil && failures.Enabled() {
			// A transient failure: the consumer retries and, if the switch is
			// still on, the message is dead lettered and billing reopens the
			// invoice by timeout.
			logger.WarnContext(ctx, "failing print request on purpose",
				"invoice_id", event.InvoiceID)
			return ErrSimulatedFailure
		}

		items := make([]DebitItem, 0, len(event.Items))
		for _, item := range event.Items {
			items = append(items, DebitItem{ProductID: item.ProductID, Quantity: item.Quantity})
		}

		// The debit runs in a savepoint: a rejection rolls back the balance
		// changes while keeping the answer that is enqueued below.
		attempt, err := tx.Begin(ctx)
		if err != nil {
			return fmt.Errorf("open debit savepoint: %w", err)
		}

		debitErr := DebitTx(ctx, attempt, event.InvoiceID, items)
		if debitErr != nil {
			if rollbackErr := attempt.Rollback(ctx); rollbackErr != nil {
				return fmt.Errorf("rollback debit: %w", rollbackErr)
			}
		} else if err := attempt.Commit(ctx); err != nil {
			return fmt.Errorf("commit debit: %w", err)
		}

		answer, err := buildAnswer(event.InvoiceID, event.Attempt, debitErr)
		if err != nil {
			return err
		}
		if err := messaging.EnqueueTx(ctx, tx, answer); err != nil {
			return err
		}

		if debitErr != nil {
			logger.WarnContext(ctx, "rejected print request",
				"invoice_id", event.InvoiceID, "reason", apperr.From(debitErr).Code)
			return nil
		}
		logger.InfoContext(ctx, "debited stock for invoice",
			"invoice_id", event.InvoiceID, "items", len(items))
		return nil
	}
}

// buildAnswer reports the outcome back to billing, echoing the attempt the
// request carried so a late answer can be recognised on the other side.
func buildAnswer(invoiceID uuid.UUID, attempt int, debitErr error) (messaging.Message, error) {
	if debitErr == nil {
		return messaging.NewMessage(contracts.StockDebited, invoiceID.String(),
			contracts.Debited{InvoiceID: invoiceID, Attempt: attempt})
	}

	appErr := apperr.From(debitErr)
	// Only expected business outcomes are reported back; anything else is a
	// real failure and must be retried instead of answered.
	switch appErr.Kind {
	case apperr.KindConflict, apperr.KindNotFound, apperr.KindInvalid:
		return messaging.NewMessage(contracts.StockRejected, invoiceID.String(), contracts.Rejected{
			InvoiceID: invoiceID,
			Attempt:   attempt,
			Code:      appErr.Code,
			Message:   appErr.Message,
		})
	default:
		return messaging.Message{}, debitErr
	}
}
