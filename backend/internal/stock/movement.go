package stock

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
)

// MovementSource says what caused a balance to change.
type MovementSource string

const (
	// SourceRegistration is the opening balance a product was created with.
	SourceRegistration MovementSource = "registration"
	// SourceEdit is a balance corrected on the product form.
	SourceEdit MovementSource = "edit"
	// SourceAdjustment is a delivery, a loss or a stock count.
	SourceAdjustment MovementSource = "adjustment"
	// SourceInvoice is stock taken out by an invoice being printed.
	SourceInvoice MovementSource = "invoice"
)

// MaxMovementReasonLength bounds the note kept with a movement.
const MaxMovementReasonLength = 200

// Movement is one change to the balance of a product.
//
// It is a record of something that already happened, so nothing here is ever
// updated or deleted: a correction is another movement.
type Movement struct {
	ID        uuid.UUID
	ProductID uuid.UUID
	// Delta is how much the balance moved; negative took stock out.
	Delta int
	// BalanceAfter is what the balance became, so one row explains itself.
	BalanceAfter int
	Source       MovementSource
	Reason       string
	// InvoiceID is set when the cause was an invoice being printed.
	InvoiceID uuid.UUID
	// ActorEmail is who did it, when a person did. A movement caused by an
	// invoice has none: the invoice records who printed it.
	ActorEmail string
	CreatedAt  time.Time
}

// MovementPage is a slice of the history plus how to ask for the next one.
type MovementPage struct {
	Items []Movement
	// NextCursor is empty when the last page was reached.
	NextCursor string
}

// recordMovementTx writes one movement inside the transaction that changed the
// balance. Both land together or neither does, which is what keeps the history
// from disagreeing with the balance it explains.
func recordMovementTx(ctx context.Context, tx pgx.Tx, movement Movement) error {
	if movement.Delta == 0 {
		// Saving a product without touching its balance is not a movement.
		return nil
	}

	reason := movement.Reason
	if len([]rune(reason)) > MaxMovementReasonLength {
		reason = string([]rune(reason)[:MaxMovementReasonLength])
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO stock_movements (product_id, delta, balance_after, source, reason, invoice_id, actor_email)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		movement.ProductID, movement.Delta, movement.BalanceAfter, string(movement.Source),
		reason, nullableID(movement.InvoiceID), movement.ActorEmail)
	if err != nil {
		return fmt.Errorf("record stock movement: %w", err)
	}
	return nil
}

// actorFrom names whoever is behind the request, when there is one. Work
// driven by a message has no signed in user and leaves it empty rather than
// failing: the movement still has to be recorded.
func actorFrom(ctx context.Context) string {
	user, err := authn.UserFrom(ctx)
	if err != nil {
		return ""
	}
	return user.Email
}

// ListMovements returns a page of the history of one product, newest first.
//
// The page is cut by (created_at, id) rather than by an offset, so a movement
// recorded while someone is paging never repeats or hides another one.
func (s *Store) ListMovements(ctx context.Context, productID uuid.UUID, limit int, rawCursor string) (MovementPage, error) {
	limit = pagination.NormalizeLimit(limit)

	cursor, err := pagination.Decode(rawCursor)
	if err != nil {
		return MovementPage{}, err
	}

	conditions := []string{"product_id = $1"}
	arguments := []any{productID}

	if cursor.Key != "" {
		createdAt, err := time.Parse(time.RFC3339Nano, cursor.Key)
		if err != nil {
			return MovementPage{}, pagination.ErrInvalidCursor.WithCause(err)
		}
		id, err := uuid.Parse(cursor.ID)
		if err != nil {
			return MovementPage{}, pagination.ErrInvalidCursor.WithCause(err)
		}
		arguments = append(arguments, createdAt, id)
		conditions = append(conditions, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(arguments)-1, len(arguments)))
	}

	// One extra row tells us whether there is another page, without counting.
	arguments = append(arguments, limit+1)
	statement := fmt.Sprintf(`
		SELECT id, product_id, delta, balance_after, source, reason, invoice_id, actor_email, created_at
		FROM stock_movements
		WHERE %s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d`, strings.Join(conditions, " AND "), len(arguments))

	rows, err := s.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return MovementPage{}, fmt.Errorf("list stock movements: %w", err)
	}
	defer rows.Close()

	page := MovementPage{Items: make([]Movement, 0, limit)}
	for rows.Next() {
		var movement Movement
		var invoiceID *uuid.UUID
		var source string
		if err := rows.Scan(&movement.ID, &movement.ProductID, &movement.Delta, &movement.BalanceAfter,
			&source, &movement.Reason, &invoiceID, &movement.ActorEmail, &movement.CreatedAt); err != nil {
			return MovementPage{}, fmt.Errorf("scan stock movement: %w", err)
		}
		movement.Source = MovementSource(source)
		if invoiceID != nil {
			movement.InvoiceID = *invoiceID
		}
		page.Items = append(page.Items, movement)
	}
	if err := rows.Err(); err != nil {
		return MovementPage{}, fmt.Errorf("read stock movements: %w", err)
	}

	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{
			Key: last.CreatedAt.Format(time.RFC3339Nano),
			ID:  last.ID.String(),
		})
	}
	return page, nil
}
