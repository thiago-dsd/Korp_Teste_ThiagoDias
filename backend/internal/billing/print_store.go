package billing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/thiagodias/korp-invoices/internal/contracts"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
)

// StartPrinting moves an open invoice to PRINTING and records the print
// request in the outbox, both in the same transaction: the stock service is
// asked to debit exactly when, and only when, the status change is committed.
func (s *Store) StartPrinting(ctx context.Context, id uuid.UUID) (Invoice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invoice{}, fmt.Errorf("begin print transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Locking the invoice serializes two operators clicking print at once:
	// the second one finds the invoice already in PRINTING and is rejected.
	invoice, err := scanInvoice(tx.QueryRow(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invoice{}, ErrInvoiceNotFound
		}
		return Invoice{}, fmt.Errorf("select invoice for printing: %w", err)
	}

	items, err := s.itemsOfTx(ctx, tx, invoice.ID)
	if err != nil {
		return Invoice{}, err
	}
	invoice.Items = items

	if len(invoice.Items) == 0 {
		return Invoice{}, ErrInvalidInvoice.WithDetails(map[string]string{
			"items": "invoice has no items to print",
		})
	}
	if err := invoice.StartPrinting(time.Now().UTC()); err != nil {
		return Invoice{}, err
	}

	// The moment the attempt started is stamped by the database, not by this
	// process. The reconciler decides that an attempt timed out by comparing it
	// against now() on the same server, and two clocks that disagree would make
	// it reopen healthy attempts or never notice lost ones.
	if err := tx.QueryRow(ctx, `
		UPDATE invoices
		SET status = $2, printing_since = now(), print_attempt = $3,
		    failure_code = NULL, failure_message = NULL, updated_at = now()
		WHERE id = $1
		RETURNING printing_since`,
		invoice.ID, invoice.Status, invoice.PrintAttempt).Scan(&invoice.PrintingSince); err != nil {
		return Invoice{}, fmt.Errorf("update invoice status: %w", err)
	}

	event := contracts.PrintRequested{
		InvoiceID:     invoice.ID,
		InvoiceNumber: invoice.Number,
		Attempt:       invoice.PrintAttempt,
		Items:         make([]contracts.PrintItem, 0, len(invoice.Items)),
	}
	for _, item := range invoice.Items {
		event.Items = append(event.Items, contracts.PrintItem{
			ProductID: item.ProductID,
			Quantity:  item.Quantity,
		})
	}

	message, err := messaging.NewMessage(contracts.InvoicePrintRequested, invoice.ID.String(), event)
	if err != nil {
		return Invoice{}, err
	}
	if err := messaging.EnqueueTx(ctx, tx, message); err != nil {
		return Invoice{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Invoice{}, fmt.Errorf("commit print request: %w", err)
	}
	return invoice, nil
}

// CloseTx closes a printed invoice inside the caller's transaction, which is
// the one that also records the message as processed.
//
// It deliberately does not check which attempt the confirmation came from. The
// stock service records a debit per invoice, not per attempt, so once the
// balances were taken they stay taken and the invoice is printed no matter
// which request got the answer through. Ignoring a late confirmation would
// leave an invoice open whose stock is already gone.
func CloseTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	invoice, err := lockInvoice(ctx, tx, id)
	if err != nil {
		return err
	}
	if invoice.Status == StatusClosed {
		return nil
	}
	if err := invoice.Close(time.Now().UTC()); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invoices
		SET status = $2, printed_at = $3, printing_since = NULL,
		    failure_code = NULL, failure_message = NULL, updated_at = now()
		WHERE id = $1`, invoice.ID, invoice.Status, invoice.PrintedAt); err != nil {
		return fmt.Errorf("close invoice: %w", err)
	}
	return nil
}

// ReopenTx returns an invoice to OPEN with the reason of the failure, inside
// the caller's transaction.
//
// A rejection is only true of the attempt that produced it, so it is applied
// only to that attempt. The broker can hold an answer back long enough for the
// attempt to time out and the operator to print again; applying it then would
// cancel a request that is still on its way and blame it for a balance that may
// have been replenished since. Attempt zero comes from a service that does not
// number attempts yet, and falls back to the status check alone.
func ReopenTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, attempt int, code, message string) error {
	invoice, err := lockInvoice(ctx, tx, id)
	if err != nil {
		return err
	}
	if invoice.Status != StatusPrinting {
		// The invoice was already resolved, by the reconciler or by a previous
		// answer; there is nothing to reopen.
		return nil
	}
	if attempt != 0 && attempt != invoice.PrintAttempt {
		// The answer belongs to an attempt that was already given up on.
		return nil
	}
	if err := invoice.Reopen(code, message); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invoices
		SET status = $2, printing_since = NULL, failure_code = $3, failure_message = $4, updated_at = now()
		WHERE id = $1`, invoice.ID, invoice.Status, code, message); err != nil {
		return fmt.Errorf("reopen invoice: %w", err)
	}
	return nil
}

// ReopenStalePrintings returns invoices that have been printing for longer
// than timeout to OPEN, so a lost answer never leaves an invoice stuck.
// It reports how many invoices were reopened.
func (s *Store) ReopenStalePrintings(ctx context.Context, timeout time.Duration, code, message string) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE invoices
		SET status = 'OPEN', printing_since = NULL, failure_code = $2, failure_message = $3, updated_at = now()
		WHERE status = 'PRINTING' AND printing_since < now() - $1::interval`,
		timeout.String(), code, message)
	if err != nil {
		return 0, fmt.Errorf("reopen stale printings: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func lockInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Invoice, error) {
	invoice, err := scanInvoice(tx.QueryRow(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invoice{}, ErrInvoiceNotFound
		}
		return Invoice{}, fmt.Errorf("select invoice: %w", err)
	}
	return invoice, nil
}

func (s *Store) itemsOfTx(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) ([]Item, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, product_id, product_code, product_description, quantity
		FROM invoice_items
		WHERE invoice_id = $1
		ORDER BY product_code`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("select invoice items: %w", err)
	}
	defer rows.Close()

	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.ProductID, &item.ProductCode,
			&item.ProductDescription, &item.Quantity); err != nil {
			return nil, fmt.Errorf("scan invoice item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read invoice items: %w", err)
	}
	return items, nil
}
