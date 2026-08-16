package billing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
)

// Store persists invoices in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds an invoice store on top of the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const invoiceColumns = `id, number, status, created_at, updated_at, printed_at,
	failure_code, failure_message, printing_since`

// Create stores an invoice and its items in a single transaction: an invoice
// never exists without the items it was created with. The number is assigned
// by a database sequence, so concurrent creations never share one.
func (s *Store) Create(ctx context.Context, items []Item) (Invoice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invoice{}, fmt.Errorf("begin invoice transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	invoice, err := scanInvoice(tx.QueryRow(ctx, `
		INSERT INTO invoices DEFAULT VALUES
		RETURNING `+invoiceColumns))
	if err != nil {
		return Invoice{}, fmt.Errorf("insert invoice: %w", err)
	}

	for _, item := range items {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO invoice_items (invoice_id, product_id, product_code, product_description, quantity)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id`,
			invoice.ID, item.ProductID, item.ProductCode, item.ProductDescription, item.Quantity,
		).Scan(&id)
		if err != nil {
			return Invoice{}, fmt.Errorf("insert invoice item: %w", err)
		}
		item.ID = id
		invoice.Items = append(invoice.Items, item)
	}

	if err := tx.Commit(ctx); err != nil {
		return Invoice{}, fmt.Errorf("commit invoice: %w", err)
	}
	return invoice, nil
}

// GetByID returns an invoice with its items, or ErrInvoiceNotFound.
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (Invoice, error) {
	invoice, err := scanInvoice(s.pool.QueryRow(ctx,
		`SELECT `+invoiceColumns+` FROM invoices WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invoice{}, ErrInvoiceNotFound
		}
		return Invoice{}, fmt.Errorf("select invoice: %w", err)
	}

	items, err := s.itemsOf(ctx, invoice.ID)
	if err != nil {
		return Invoice{}, err
	}
	invoice.Items = items
	return invoice, nil
}

// Query describes a page of invoices.
type Query struct {
	// Status filters the listing when it is not empty.
	Status string
	// Limit is how many invoices to return.
	Limit int
	// Cursor points at the end of the previous page.
	Cursor string
}

// Page is a slice of the listing plus how to ask for the next one.
type Page struct {
	Items []Invoice
	// NextCursor is empty when the last page was reached.
	NextCursor string
}

// List returns a page of invoices, from the newest number to the oldest.
//
// Pages are cut by invoice number instead of by an offset, so invoices issued
// while someone is paging do not push items from one page to the next.
func (s *Store) List(ctx context.Context, query Query) (Page, error) {
	limit := pagination.NormalizeLimit(query.Limit)

	cursor, err := pagination.Decode(query.Cursor)
	if err != nil {
		return Page{}, err
	}

	// An empty cursor starts above the highest number in use.
	before := int64(math.MaxInt64)
	if cursor.Key != "" {
		before, err = strconv.ParseInt(cursor.Key, 10, 64)
		if err != nil {
			return Page{}, pagination.ErrInvalidCursor.WithCause(err)
		}
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+invoiceColumns+`
		FROM invoices
		WHERE ($1 = '' OR status = $1)
		  AND number < $2
		ORDER BY number DESC
		LIMIT $3`, query.Status, before, limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("select invoices: %w", err)
	}
	defer rows.Close()

	invoices := make([]Invoice, 0, limit)
	for rows.Next() {
		invoice, err := scanInvoice(rows)
		if err != nil {
			return Page{}, fmt.Errorf("scan invoice: %w", err)
		}
		invoices = append(invoices, invoice)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("read invoices: %w", err)
	}

	page := Page{Items: invoices}
	if len(invoices) > limit {
		page.Items = invoices[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{
			Key: strconv.FormatInt(last.Number, 10),
			ID:  last.ID.String(),
		})
	}
	if len(page.Items) == 0 {
		return page, nil
	}

	byID := make(map[uuid.UUID]int, len(page.Items))
	ids := make([]uuid.UUID, 0, len(page.Items))
	for index, invoice := range page.Items {
		byID[invoice.ID] = index
		ids = append(ids, invoice.ID)
	}

	// One extra query loads the items of every invoice on the page, instead of
	// one query per invoice.
	itemRows, err := s.pool.Query(ctx, `
		SELECT invoice_id, id, product_id, product_code, product_description, quantity
		FROM invoice_items
		WHERE invoice_id = ANY($1)
		ORDER BY product_code`, ids)
	if err != nil {
		return Page{}, fmt.Errorf("select invoice items: %w", err)
	}
	defer itemRows.Close()

	for itemRows.Next() {
		var invoiceID uuid.UUID
		var item Item
		if err := itemRows.Scan(&invoiceID, &item.ID, &item.ProductID,
			&item.ProductCode, &item.ProductDescription, &item.Quantity); err != nil {
			return Page{}, fmt.Errorf("scan invoice item: %w", err)
		}
		if position, ok := byID[invoiceID]; ok {
			page.Items[position].Items = append(page.Items[position].Items, item)
		}
	}
	if err := itemRows.Err(); err != nil {
		return Page{}, fmt.Errorf("read invoice items: %w", err)
	}
	return page, nil
}

func (s *Store) itemsOf(ctx context.Context, invoiceID uuid.UUID) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `
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

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanInvoice(row rowScanner) (Invoice, error) {
	var invoice Invoice
	var failureCode, failureMessage *string

	err := row.Scan(
		&invoice.ID,
		&invoice.Number,
		&invoice.Status,
		&invoice.CreatedAt,
		&invoice.UpdatedAt,
		&invoice.PrintedAt,
		&failureCode,
		&failureMessage,
		&invoice.PrintingSince,
	)
	if err != nil {
		return Invoice{}, err
	}
	if failureCode != nil {
		invoice.FailureCode = *failureCode
	}
	if failureMessage != nil {
		invoice.FailureMessage = *failureMessage
	}
	invoice.Items = make([]Item, 0)
	return invoice, nil
}
