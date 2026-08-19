package stock_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/stock"
)

// The ledger is written inside the transaction that changes the balance, which
// is a store concern: these run against a real database because an in-memory
// fake would be asserting on itself.

func TestRegisteringAProductOpensItsHistory(t *testing.T) {
	ctx, store, _ := newTestStore(t)

	product := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	page, err := store.ListMovements(ctx, product.ID, 20, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("got %d movements, want the opening balance", len(page.Items))
	}
	movement := page.Items[0]
	if movement.Delta != 10 || movement.BalanceAfter != 10 {
		t.Errorf("movement = %+d ending at %d, want +10 ending at 10", movement.Delta, movement.BalanceAfter)
	}
	if movement.Source != stock.SourceRegistration {
		t.Errorf("source = %q, want %q", movement.Source, stock.SourceRegistration)
	}
}

func TestAProductRegisteredEmptyHasNoOpeningMovement(t *testing.T) {
	ctx, store, _ := newTestStore(t)

	product := createProduct(t, ctx, store, "P-1", "Steel bolt", 0)

	page, err := store.ListMovements(ctx, product.ID, 20, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("got %d movements, want none: nothing moved", len(page.Items))
	}
}

func TestEditingTheBalanceRecordsWhoDidItAndHowFarItMoved(t *testing.T) {
	ctx, store, _ := newTestStore(t)
	product := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	signedIn := authn.WithUser(ctx, authn.User{ID: uuid.New(), Email: "admin@example.com"})
	product.Description = "Steel bolt M8"
	product.Balance = 4
	if _, err := store.Update(signedIn, product); err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}

	page, err := store.ListMovements(ctx, product.ID, 20, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("got %d movements, want the registration and the edit", len(page.Items))
	}
	// Newest first.
	edit := page.Items[0]
	if edit.Delta != -6 || edit.BalanceAfter != 4 {
		t.Errorf("edit = %+d ending at %d, want -6 ending at 4", edit.Delta, edit.BalanceAfter)
	}
	if edit.Source != stock.SourceEdit {
		t.Errorf("source = %q, want %q", edit.Source, stock.SourceEdit)
	}
	if edit.ActorEmail != "admin@example.com" {
		t.Errorf("actor = %q, want the signed in administrator", edit.ActorEmail)
	}
}

func TestEditingOnlyTheDescriptionIsNotAMovement(t *testing.T) {
	ctx, store, _ := newTestStore(t)
	product := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	product.Description = "Steel bolt M8"
	if _, err := store.Update(ctx, product); err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}

	page, err := store.ListMovements(ctx, product.ID, 20, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("got %d movements, want only the opening balance: no stock moved", len(page.Items))
	}
}

func TestAnAdjustmentKeepsTheReasonItWasMadeFor(t *testing.T) {
	ctx, store, _ := newTestStore(t)
	product := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	signedIn := authn.WithUser(ctx, authn.User{ID: uuid.New(), Email: "admin@example.com"})
	if _, err := store.Adjust(signedIn, []stock.Adjustment{
		{ProductID: product.ID, Delta: 25, Reason: "delivery note 4711"},
	}); err != nil {
		t.Fatalf("Adjust() returned error: %v", err)
	}

	page, err := store.ListMovements(ctx, product.ID, 20, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	movement := page.Items[0]
	if movement.Delta != 25 || movement.BalanceAfter != 35 {
		t.Errorf("movement = %+d ending at %d, want +25 ending at 35", movement.Delta, movement.BalanceAfter)
	}
	if movement.Source != stock.SourceAdjustment {
		t.Errorf("source = %q, want %q", movement.Source, stock.SourceAdjustment)
	}
	if movement.Reason != "delivery note 4711" {
		t.Errorf("reason = %q, want the note the operator wrote", movement.Reason)
	}
}

// A refused adjustment must leave no trace: the history explains the balance,
// so a movement that did not happen would make it lie.
func TestARefusedAdjustmentRecordsNothing(t *testing.T) {
	ctx, store, _ := newTestStore(t)
	first := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	second := createProduct(t, ctx, store, "P-2", "Hammer", 1)

	if _, err := store.Adjust(ctx, []stock.Adjustment{
		{ProductID: first.ID, Delta: 5},
		{ProductID: second.ID, Delta: -50},
	}); err == nil {
		t.Fatal("Adjust() succeeded, want the whole document refused")
	}

	page, err := store.ListMovements(ctx, first.ID, 20, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("got %d movements, want only the opening balance: nothing was applied", len(page.Items))
	}
}

func TestPrintingAnInvoiceRecordsTheMovementAgainstIt(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	product := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	invoiceID := uuid.New()

	inTransaction(t, ctx, pool, func(tx pgx.Tx) error {
		return stock.DebitTx(ctx, tx, invoiceID, []stock.DebitItem{{ProductID: product.ID, Quantity: 3}})
	})

	page, err := store.ListMovements(ctx, product.ID, 20, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	movement := page.Items[0]
	if movement.Delta != -3 || movement.BalanceAfter != 7 {
		t.Errorf("movement = %+d ending at %d, want -3 ending at 7", movement.Delta, movement.BalanceAfter)
	}
	if movement.Source != stock.SourceInvoice {
		t.Errorf("source = %q, want %q", movement.Source, stock.SourceInvoice)
	}
	if movement.InvoiceID != invoiceID {
		t.Errorf("invoice = %s, want %s", movement.InvoiceID, invoiceID)
	}
	if movement.ActorEmail != "" {
		t.Errorf("actor = %q, want none: the invoice records who printed it", movement.ActorEmail)
	}
}

// A repeated delivery of the same print request must not add a second
// movement, for the same reason it must not debit twice.
func TestARepeatedDebitDoesNotRecordASecondMovement(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	product := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	invoiceID := uuid.New()

	for range 2 {
		inTransaction(t, ctx, pool, func(tx pgx.Tx) error {
			return stock.DebitTx(ctx, tx, invoiceID, []stock.DebitItem{{ProductID: product.ID, Quantity: 3}})
		})
	}

	page, err := store.ListMovements(ctx, product.ID, 20, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("got %d movements, want the opening balance and one debit", len(page.Items))
	}
	if page.Items[0].BalanceAfter != 7 {
		t.Errorf("balance after = %d, want 7: the second delivery changed nothing", page.Items[0].BalanceAfter)
	}
}

func TestTheHistoryIsPagedNewestFirst(t *testing.T) {
	ctx, store, _ := newTestStore(t)
	product := createProduct(t, ctx, store, "P-1", "Steel bolt", 0)

	for range 5 {
		if _, err := store.Adjust(ctx, []stock.Adjustment{{ProductID: product.ID, Delta: 1}}); err != nil {
			t.Fatalf("Adjust() returned error: %v", err)
		}
	}

	first, err := store.ListMovements(ctx, product.ID, 2, "")
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %d items, cursor %q, want 2 items and a cursor", len(first.Items), first.NextCursor)
	}
	if first.Items[0].BalanceAfter != 5 {
		t.Errorf("newest balance = %d, want 5: the page starts at the latest movement", first.Items[0].BalanceAfter)
	}

	second, err := store.ListMovements(ctx, product.ID, 2, first.NextCursor)
	if err != nil {
		t.Fatalf("ListMovements() returned error: %v", err)
	}
	if len(second.Items) != 2 {
		t.Fatalf("second page = %d items, want 2", len(second.Items))
	}
	for _, seen := range first.Items {
		for _, next := range second.Items {
			if seen.ID == next.ID {
				t.Errorf("movement %s repeated on the second page", seen.ID)
			}
		}
	}
}

// inTransaction runs the work in a committed transaction, the way the message
// handler does.
func inTransaction(t *testing.T, ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, work func(pgx.Tx) error) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() returned error: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := work(tx); err != nil {
		t.Fatalf("work in transaction returned error: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() returned error: %v", err)
	}
}
