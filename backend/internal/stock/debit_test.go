package stock_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thiagodias/korp-invoices/internal/contracts"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
	"github.com/thiagodias/korp-invoices/internal/stock"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// debit runs a debit in its own transaction, the way the message handler does.
func debit(ctx context.Context, pool *pgxpool.Pool, invoiceID uuid.UUID, items []stock.DebitItem) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := stock.DebitTx(ctx, tx, invoiceID, items); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func balanceOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, productID uuid.UUID) int {
	t.Helper()

	var balance int
	if err := pool.QueryRow(ctx, `SELECT balance FROM products WHERE id = $1`, productID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return balance
}

func TestDebitTakesTheQuantityFromTheBalance(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	hammer := createProduct(t, ctx, store, "P-2", "Hammer", 4)

	err := debit(ctx, pool, uuid.New(), []stock.DebitItem{
		{ProductID: bolt.ID, Quantity: 2},
		{ProductID: hammer.ID, Quantity: 1},
	})
	if err != nil {
		t.Fatalf("debit returned error: %v", err)
	}

	if got := balanceOf(t, ctx, pool, bolt.ID); got != 8 {
		t.Errorf("bolt balance = %d, want 8", got)
	}
	if got := balanceOf(t, ctx, pool, hammer.ID); got != 3 {
		t.Errorf("hammer balance = %d, want 3", got)
	}
}

func TestDebitRejectsInsufficientBalanceWithoutTouchingAnyProduct(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	hammer := createProduct(t, ctx, store, "P-2", "Hammer", 1)

	err := debit(ctx, pool, uuid.New(), []stock.DebitItem{
		{ProductID: bolt.ID, Quantity: 2},
		{ProductID: hammer.ID, Quantity: 5},
	})

	if !errors.Is(err, stock.ErrInsufficientBalance) {
		t.Fatalf("debit error = %v, want ErrInsufficientBalance", err)
	}
	if got := balanceOf(t, ctx, pool, bolt.ID); got != 10 {
		t.Errorf("bolt balance = %d, want 10 (a rejected invoice debits nothing)", got)
	}
	if got := balanceOf(t, ctx, pool, hammer.ID); got != 1 {
		t.Errorf("hammer balance = %d, want 1", got)
	}
}

func TestDebitReportsUnknownProduct(t *testing.T) {
	ctx, _, pool := newTestStore(t)

	err := debit(ctx, pool, uuid.New(), []stock.DebitItem{{ProductID: uuid.New(), Quantity: 1}})

	if !errors.Is(err, stock.ErrProductNotFound) {
		t.Errorf("debit error = %v, want ErrProductNotFound", err)
	}
}

func TestDebitIsIdempotentPerInvoice(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	invoiceID := uuid.New()
	items := []stock.DebitItem{{ProductID: bolt.ID, Quantity: 3}}

	for range 3 {
		if err := debit(ctx, pool, invoiceID, items); err != nil {
			t.Fatalf("debit returned error: %v", err)
		}
	}

	if got := balanceOf(t, ctx, pool, bolt.ID); got != 7 {
		t.Errorf("balance = %d, want 7 (the same invoice must debit only once)", got)
	}
}

// The scenario named in the challenge: one unit in stock, two invoices asking
// for it at the same time. Exactly one must succeed.
func TestConcurrentDebitsOfTheLastUnit(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 1)

	const attempts = 8
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		succeeded  int
		rejected   int
		unexpected []error
	)

	wg.Add(attempts)
	for range attempts {
		go func() {
			defer wg.Done()

			err := debit(ctx, pool, uuid.New(), []stock.DebitItem{{ProductID: bolt.ID, Quantity: 1}})

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, stock.ErrInsufficientBalance):
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
	if succeeded != 1 {
		t.Errorf("%d invoices took the last unit, want exactly 1", succeeded)
	}
	if rejected != attempts-1 {
		t.Errorf("%d invoices were rejected, want %d", rejected, attempts-1)
	}
	if got := balanceOf(t, ctx, pool, bolt.ID); got != 0 {
		t.Errorf("balance = %d, want 0 and never negative", got)
	}
}

// Two invoices using the same two products in opposite order must not
// deadlock: the debit always locks products in a stable order.
func TestConcurrentDebitsOfTheSameProductsDoNotDeadlock(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	first := createProduct(t, ctx, store, "P-1", "Steel bolt", 100)
	second := createProduct(t, ctx, store, "P-2", "Hammer", 100)

	const rounds = 10
	var wg sync.WaitGroup
	errs := make(chan error, rounds*2)

	wg.Add(rounds * 2)
	for range rounds {
		go func() {
			defer wg.Done()
			errs <- debit(ctx, pool, uuid.New(), []stock.DebitItem{
				{ProductID: first.ID, Quantity: 1},
				{ProductID: second.ID, Quantity: 1},
			})
		}()
		go func() {
			defer wg.Done()
			errs <- debit(ctx, pool, uuid.New(), []stock.DebitItem{
				{ProductID: second.ID, Quantity: 1},
				{ProductID: first.ID, Quantity: 1},
			})
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("debit returned error: %v", err)
		}
	}
	if got := balanceOf(t, ctx, pool, first.ID); got != 100-rounds*2 {
		t.Errorf("balance = %d, want %d", got, 100-rounds*2)
	}
}

// handlePrintRequest runs the message handler the way the consumer does.
func handlePrintRequest(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	failures *stock.FailureSwitch, event contracts.PrintRequested) error {
	t.Helper()

	message, err := messaging.NewMessage(contracts.InvoicePrintRequested, event.InvoiceID.String(), event)
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() returned error: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	handler := stock.PrintRequestHandler(discardLogger(), failures)
	if err := handler(ctx, tx, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func outboxMessages(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
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

func TestPrintRequestHandlerDebitsAndAnswers(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	invoiceID := uuid.New()

	err := handlePrintRequest(t, ctx, pool, nil, contracts.PrintRequested{
		InvoiceID: invoiceID,
		Items:     []contracts.PrintItem{{ProductID: bolt.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if got := balanceOf(t, ctx, pool, bolt.ID); got != 8 {
		t.Errorf("balance = %d, want 8", got)
	}
	if types := outboxMessages(t, ctx, pool); len(types) != 1 || types[0] != contracts.StockDebited {
		t.Errorf("outbox = %v, want a single %s", types, contracts.StockDebited)
	}
}

func TestPrintRequestHandlerAnswersRejectionWithoutDebiting(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 1)

	err := handlePrintRequest(t, ctx, pool, nil, contracts.PrintRequested{
		InvoiceID: uuid.New(),
		Items:     []contracts.PrintItem{{ProductID: bolt.ID, Quantity: 5}},
	})
	if err != nil {
		t.Fatalf("handler returned error: %v (a rejection is an answer, not a failure)", err)
	}

	if got := balanceOf(t, ctx, pool, bolt.ID); got != 1 {
		t.Errorf("balance = %d, want 1 untouched", got)
	}
	if types := outboxMessages(t, ctx, pool); len(types) != 1 || types[0] != contracts.StockRejected {
		t.Errorf("outbox = %v, want a single %s", types, contracts.StockRejected)
	}

	// The rejected invoice must remain debitable once the balance is fixed.
	var debits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_debits`).Scan(&debits); err != nil {
		t.Fatalf("count debits: %v", err)
	}
	if debits != 0 {
		t.Errorf("stock debits = %d, want 0 for a rejected invoice", debits)
	}
}

func TestPrintRequestHandlerFailsWhileFailureSimulationIsOn(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	failures := &stock.FailureSwitch{}
	failures.Enable(true)

	err := handlePrintRequest(t, ctx, pool, failures, contracts.PrintRequested{
		InvoiceID: uuid.New(),
		Items:     []contracts.PrintItem{{ProductID: bolt.ID, Quantity: 2}},
	})
	if !errors.Is(err, stock.ErrSimulatedFailure) {
		t.Fatalf("handler error = %v, want ErrSimulatedFailure", err)
	}
	if got := balanceOf(t, ctx, pool, bolt.ID); got != 10 {
		t.Errorf("balance = %d, want 10 untouched", got)
	}

	// Turning the switch off restores the normal behaviour.
	failures.Enable(false)
	if err := handlePrintRequest(t, ctx, pool, failures, contracts.PrintRequested{
		InvoiceID: uuid.New(),
		Items:     []contracts.PrintItem{{ProductID: bolt.ID, Quantity: 2}},
	}); err != nil {
		t.Fatalf("handler returned error after disabling the simulation: %v", err)
	}
	if got := balanceOf(t, ctx, pool, bolt.ID); got != 8 {
		t.Errorf("balance = %d, want 8", got)
	}
}

func TestPrintRequestHandlerIgnoresUnknownMessageTypes(t *testing.T) {
	ctx, _, pool := newTestStore(t)

	message, err := messaging.NewMessage("something.else", "invoice-1", map[string]string{})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() returned error: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	handler := stock.PrintRequestHandler(discardLogger(), nil)
	if err := handler(ctx, tx, message); err != nil {
		t.Errorf("handler returned error for an unknown type: %v", err)
	}
}

func TestPrintRequestHandlerRejectsMalformedPayload(t *testing.T) {
	ctx, _, pool := newTestStore(t)

	message, err := messaging.NewMessage(contracts.InvoicePrintRequested, "invoice-1",
		map[string]string{"invoice_id": "not-a-uuid"})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() returned error: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	handler := stock.PrintRequestHandler(discardLogger(), nil)
	if err := handler(ctx, tx, message); err == nil {
		t.Error("handler accepted a malformed payload, want an error")
	}
}

// A redelivered print request must not debit twice, even though the message
// handler runs again from scratch.
func TestPrintRequestHandlerIsIdempotent(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	event := contracts.PrintRequested{
		InvoiceID: uuid.New(),
		Items:     []contracts.PrintItem{{ProductID: bolt.ID, Quantity: 3}},
	}

	for range 2 {
		if err := handlePrintRequest(t, ctx, pool, nil, event); err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	}

	if got := balanceOf(t, ctx, pool, bolt.ID); got != 7 {
		t.Errorf("balance = %d, want 7", got)
	}
}

func TestDebitTxRejectsNonPositiveQuantity(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	err := debit(ctx, pool, uuid.New(), []stock.DebitItem{{ProductID: bolt.ID, Quantity: 0}})
	if err == nil {
		t.Fatal("debit accepted a zero quantity, want an error")
	}
	if got := balanceOf(t, ctx, pool, bolt.ID); got != 10 {
		t.Errorf("balance = %d, want 10 untouched", got)
	}
}

// Billing tells the answers of two attempts apart by the attempt number, so it
// has to survive the round trip. Both outcomes carry it back.
func TestAnswersEchoTheAttemptOfTheRequest(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 1)

	debited := uuid.New()
	if err := handlePrintRequest(t, ctx, pool, nil, contracts.PrintRequested{
		InvoiceID: debited,
		Attempt:   3,
		Items:     []contracts.PrintItem{{ProductID: bolt.ID, Quantity: 1}},
	}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	rejected := uuid.New()
	if err := handlePrintRequest(t, ctx, pool, nil, contracts.PrintRequested{
		InvoiceID: rejected,
		Attempt:   7,
		Items:     []contracts.PrintItem{{ProductID: bolt.ID, Quantity: 99}},
	}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT type, payload FROM outbox_messages ORDER BY sequence`)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()

	attempts := map[string]int{}
	for rows.Next() {
		var messageType string
		var payload []byte
		if err := rows.Scan(&messageType, &payload); err != nil {
			t.Fatalf("scan outbox message: %v", err)
		}
		var answer struct {
			Attempt int `json:"attempt"`
		}
		if err := json.Unmarshal(payload, &answer); err != nil {
			t.Fatalf("decode answer: %v", err)
		}
		attempts[messageType] = answer.Attempt
	}

	if attempts[contracts.StockDebited] != 3 {
		t.Errorf("%s carried attempt %d, want 3", contracts.StockDebited, attempts[contracts.StockDebited])
	}
	if attempts[contracts.StockRejected] != 7 {
		t.Errorf("%s carried attempt %d, want 7", contracts.StockRejected, attempts[contracts.StockRejected])
	}
}
