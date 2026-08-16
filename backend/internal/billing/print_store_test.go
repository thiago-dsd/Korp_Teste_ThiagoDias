package billing_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thiagodias/korp-invoices/internal/billing"
	"github.com/thiagodias/korp-invoices/internal/contracts"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres/pgtest"
)

func newPrintTestStore(t *testing.T) (context.Context, *billing.Store, *pgxpool.Pool) {
	t.Helper()

	ctx, pool := pgtest.Pool(t, "BILLING_TEST_DATABASE_URL", billing.MigrationsFS, billing.MigrationsDir)
	return ctx, billing.NewStore(pool), pool
}

func outboxTypes(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()

	rows, err := pool.Query(ctx, `SELECT type FROM outbox_messages ORDER BY sequence`)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()

	types := make([]string, 0)
	for rows.Next() {
		var messageType string
		if err := rows.Scan(&messageType); err != nil {
			t.Fatalf("scan outbox message: %v", err)
		}
		types = append(types, messageType)
	}
	return types
}

func TestStartPrintingMovesToPrintingAndEnqueuesTheRequest(t *testing.T) {
	ctx, store, pool := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	printing, err := store.StartPrinting(ctx, created.ID)
	if err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}

	if printing.Status != billing.StatusPrinting {
		t.Errorf("status = %s, want %s", printing.Status, billing.StatusPrinting)
	}
	if printing.PrintingSince == nil {
		t.Error("PrintingSince is nil, want the moment the attempt started")
	}

	stored, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() returned error: %v", err)
	}
	if stored.Status != billing.StatusPrinting {
		t.Errorf("stored status = %s, want %s", stored.Status, billing.StatusPrinting)
	}

	types := outboxTypes(t, ctx, pool)
	if len(types) != 1 || types[0] != contracts.InvoicePrintRequested {
		t.Fatalf("outbox = %v, want a single %s", types, contracts.InvoicePrintRequested)
	}

	// The event must carry every item, since it is what stock debits.
	var payload contracts.PrintRequested
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM outbox_messages LIMIT 1`).Scan(&raw); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if err := (messaging.Message{Payload: raw}).Decode(&payload); err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if payload.InvoiceID != created.ID || len(payload.Items) != len(created.Items) {
		t.Errorf("payload = %+v, want the invoice and its %d items", payload, len(created.Items))
	}
}

func TestStartPrintingRejectsInvoicesThatAreNotOpen(t *testing.T) {
	ctx, store, pool := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if _, err := store.StartPrinting(ctx, created.ID); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}

	_, err = store.StartPrinting(ctx, created.ID)
	if !errors.Is(err, billing.ErrInvoiceNotPrintable) {
		t.Fatalf("second StartPrinting() error = %v, want ErrInvoiceNotPrintable", err)
	}
	if types := outboxTypes(t, ctx, pool); len(types) != 1 {
		t.Errorf("outbox has %d messages, want 1 (a rejected attempt asks for nothing)", len(types))
	}
}

func TestStartPrintingReportsMissingInvoice(t *testing.T) {
	ctx, store, _ := newPrintTestStore(t)

	if _, err := store.StartPrinting(ctx, uuid.New()); !errors.Is(err, billing.ErrInvoiceNotFound) {
		t.Errorf("StartPrinting() error = %v, want ErrInvoiceNotFound", err)
	}
}

// Two operators clicking print at the same moment must produce a single debit
// request: the row lock lets exactly one attempt through.
func TestConcurrentPrintRequestsOnTheSameInvoice(t *testing.T) {
	ctx, store, pool := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	const attempts = 6
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		accepted   int
		rejected   int
		unexpected []error
	)

	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()

			_, err := store.StartPrinting(ctx, created.ID)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, billing.ErrInvoiceNotPrintable):
				rejected++
			default:
				unexpected = append(unexpected, err)
			}
		}()
	}
	wg.Wait()

	if len(unexpected) > 0 {
		t.Fatalf("unexpected errors: %v", unexpected)
	}
	if accepted != 1 {
		t.Errorf("%d attempts started printing, want exactly 1", accepted)
	}
	if rejected != attempts-1 {
		t.Errorf("%d attempts were rejected, want %d", rejected, attempts-1)
	}
	if types := outboxTypes(t, ctx, pool); len(types) != 1 {
		t.Errorf("outbox has %d messages, want 1", len(types))
	}
}

// applyResult runs the billing message handler the way the consumer does.
func applyResult(t *testing.T, ctx context.Context, pool *pgxpool.Pool, messageType string, payload any) error {
	t.Helper()

	message, err := messaging.NewMessage(messageType, "invoice", payload)
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() returned error: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := billing.StockResultHandler(discardLogger())(ctx, tx, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func TestStockDebitedClosesTheInvoice(t *testing.T) {
	ctx, store, pool := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if _, err := store.StartPrinting(ctx, created.ID); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}

	if err := applyResult(t, ctx, pool, contracts.StockDebited,
		contracts.Debited{InvoiceID: created.ID}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	closed, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() returned error: %v", err)
	}
	if closed.Status != billing.StatusClosed {
		t.Errorf("status = %s, want %s", closed.Status, billing.StatusClosed)
	}
	if closed.PrintedAt == nil {
		t.Error("PrintedAt is nil on a closed invoice")
	}
	if closed.PrintingSince != nil {
		t.Error("PrintingSince is still set on a closed invoice")
	}
}

func TestStockRejectedReopensTheInvoiceWithTheReason(t *testing.T) {
	ctx, store, pool := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if _, err := store.StartPrinting(ctx, created.ID); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}

	err = applyResult(t, ctx, pool, contracts.StockRejected, contracts.Rejected{
		InvoiceID: created.ID,
		Code:      "insufficient_balance",
		Message:   "Product balance is not enough.",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	reopened, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() returned error: %v", err)
	}
	if reopened.Status != billing.StatusOpen {
		t.Errorf("status = %s, want %s", reopened.Status, billing.StatusOpen)
	}
	if reopened.FailureCode != "insufficient_balance" {
		t.Errorf("FailureCode = %q, want the reason from the stock service", reopened.FailureCode)
	}
	if reopened.FailureMessage == "" {
		t.Error("FailureMessage is empty, want the message for the operator")
	}

	// A reopened invoice can be printed again once the balance is fixed.
	if _, err := store.StartPrinting(ctx, created.ID); err != nil {
		t.Errorf("StartPrinting() after a rejection returned error: %v", err)
	}
}

func TestStockResultsAreIdempotent(t *testing.T) {
	ctx, store, pool := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if _, err := store.StartPrinting(ctx, created.ID); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}

	for range 2 {
		if err := applyResult(t, ctx, pool, contracts.StockDebited,
			contracts.Debited{InvoiceID: created.ID}); err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	}

	closed, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() returned error: %v", err)
	}
	if closed.Status != billing.StatusClosed {
		t.Errorf("status = %s, want %s", closed.Status, billing.StatusClosed)
	}
}

// A rejection that arrives after the invoice was already resolved must not
// reopen a closed invoice.
func TestLateRejectionDoesNotReopenAClosedInvoice(t *testing.T) {
	ctx, store, pool := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if _, err := store.StartPrinting(ctx, created.ID); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}
	if err := applyResult(t, ctx, pool, contracts.StockDebited,
		contracts.Debited{InvoiceID: created.ID}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	err = applyResult(t, ctx, pool, contracts.StockRejected, contracts.Rejected{
		InvoiceID: created.ID,
		Code:      "insufficient_balance",
		Message:   "Product balance is not enough.",
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	invoice, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() returned error: %v", err)
	}
	if invoice.Status != billing.StatusClosed {
		t.Errorf("status = %s, want it to stay %s", invoice.Status, billing.StatusClosed)
	}
}

// A confirmation that arrives after the timeout reopened the invoice must
// still close it: the stock was debited.
func TestLateConfirmationClosesAnInvoiceReopenedByTimeout(t *testing.T) {
	ctx, store, pool := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if _, err := store.StartPrinting(ctx, created.ID); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}

	reopened, err := store.ReopenStalePrintings(ctx, 0, "print_timeout", "No answer in time.")
	if err != nil {
		t.Fatalf("ReopenStalePrintings() returned error: %v", err)
	}
	if reopened != 1 {
		t.Fatalf("reopened %d invoices, want 1", reopened)
	}

	if err := applyResult(t, ctx, pool, contracts.StockDebited,
		contracts.Debited{InvoiceID: created.ID}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	invoice, err := store.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() returned error: %v", err)
	}
	if invoice.Status != billing.StatusClosed {
		t.Errorf("status = %s, want %s", invoice.Status, billing.StatusClosed)
	}
	if invoice.FailureCode != "" {
		t.Errorf("FailureCode = %q, want it cleared once the invoice closed", invoice.FailureCode)
	}
}

func TestReopenStalePrintingsLeavesFreshAttemptsAlone(t *testing.T) {
	ctx, store, _ := newPrintTestStore(t)
	created, err := store.Create(ctx, sampleItems())
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if _, err := store.StartPrinting(ctx, created.ID); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}

	reopened, err := store.ReopenStalePrintings(ctx, time.Hour, "print_timeout", "No answer in time.")
	if err != nil {
		t.Fatalf("ReopenStalePrintings() returned error: %v", err)
	}
	if reopened != 0 {
		t.Errorf("reopened %d invoices, want 0", reopened)
	}
}

func TestStockResultHandlerIgnoresUnknownTypes(t *testing.T) {
	ctx, _, pool := newPrintTestStore(t)

	if err := applyResult(t, ctx, pool, "something.else", map[string]string{}); err != nil {
		t.Errorf("handler returned error for an unknown type: %v", err)
	}
}

func TestStockResultHandlerRejectsMalformedPayload(t *testing.T) {
	ctx, _, pool := newPrintTestStore(t)

	err := applyResult(t, ctx, pool, contracts.StockDebited, map[string]string{"invoice_id": "not-a-uuid"})
	if err == nil {
		t.Error("handler accepted a malformed payload, want an error")
	}
}
