package billing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
	failure_code, failure_message, printing_since, print_attempt,
	created_by_id, created_by_email, printed_by_id, printed_by_email`

// Create stores an invoice and its items in a single transaction: an invoice
// never exists without the items it was created with. The number is assigned
// by a database sequence, so concurrent creations never share one.
func (s *Store) Create(ctx context.Context, items []Item, issuedBy Author) (Invoice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invoice{}, fmt.Errorf("begin invoice transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	invoice, err := scanInvoice(tx.QueryRow(ctx, `
		INSERT INTO invoices (created_by_id, created_by_email)
		VALUES ($1, $2)
		RETURNING `+invoiceColumns, nullableAuthorID(issuedBy), nullableAuthorEmail(issuedBy)))
	if err != nil {
		return Invoice{}, fmt.Errorf("insert invoice: %w", err)
	}

	// The items go in with one statement rather than one per line. An invoice
	// may carry a hundred products, and a round trip each turned a write that
	// should cost one into a write that costs a hundred.
	//
	// The ids are generated here instead of by the database, so each stored row
	// can be matched to the item it came from without depending on the order
	// RETURNING happens to use.
	ids := make([]uuid.UUID, len(items))
	productIDs := make([]uuid.UUID, len(items))
	codes := make([]string, len(items))
	descriptions := make([]string, len(items))
	quantities := make([]int, len(items))

	for index, item := range items {
		ids[index] = uuid.New()
		productIDs[index] = item.ProductID
		codes[index] = item.ProductCode
		descriptions[index] = item.ProductDescription
		quantities[index] = item.Quantity
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO invoice_items (id, invoice_id, product_id, product_code, product_description, quantity)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::uuid[], $4::text[], $5::text[], $6::int[])`,
		ids, invoiceIDs(invoice.ID, len(items)), productIDs, codes, descriptions, quantities,
	); err != nil {
		return Invoice{}, fmt.Errorf("insert invoice items: %w", err)
	}

	for index, item := range items {
		item.ID = ids[index]
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

// Page is a slice of the listing plus how to ask for the next one.
type Page struct {
	Items []Invoice
	// NextCursor is empty when the last page was reached.
	NextCursor string
}

// List returns a page of invoices.
//
// Every filter is applied by the database in one parameterized query, and the
// page is cut by the invoice number, so filtering a year of invoices costs the
// same as filtering a week and nothing is read into memory beyond the page
// being served.
func (s *Store) List(ctx context.Context, query Query) (Page, error) {
	limit := pagination.NormalizeLimit(query.Limit)
	if query.Order == "" {
		query.Order = pagination.Descending
	}

	cursor, err := pagination.Decode(query.Cursor)
	if err != nil {
		return Page{}, err
	}

	conditions := []string{"TRUE"}
	arguments := []any{}

	appendCondition := func(format string, values ...any) {
		placeholders := make([]any, 0, len(values))
		for _, value := range values {
			arguments = append(arguments, value)
			placeholders = append(placeholders, len(arguments))
		}
		conditions = append(conditions, fmt.Sprintf(format, placeholders...))
	}

	if len(query.Statuses) > 0 {
		appendCondition("status = ANY($%d)", query.Statuses)
	}
	if query.Number != nil {
		appendCondition("number = $%d", *query.Number)
	}
	if query.CreatedFrom != nil {
		appendCondition("created_at >= $%d", *query.CreatedFrom)
	}
	if query.CreatedTo != nil {
		appendCondition("created_at <= $%d", *query.CreatedTo)
	}
	if query.HasFailure != nil {
		if *query.HasFailure {
			conditions = append(conditions, "failure_code IS NOT NULL")
		} else {
			conditions = append(conditions, "failure_code IS NULL")
		}
	}
	// Finding the invoices that used a product is a question about the items,
	// answered with an EXISTS so an invoice is never returned twice.
	if query.ProductID != nil {
		appendCondition(`EXISTS (
			SELECT 1 FROM invoice_items
			WHERE invoice_items.invoice_id = invoices.id AND invoice_items.product_id = $%d)`, *query.ProductID)
	}
	if query.ProductCode != "" {
		appendCondition(`EXISTS (
			SELECT 1 FROM invoice_items
			WHERE invoice_items.invoice_id = invoices.id
			  AND upper(invoice_items.product_code) = upper($%d))`, query.ProductCode)
	}

	if cursor.Key != "" {
		position, err := strconv.ParseInt(cursor.Key, 10, 64)
		if err != nil {
			return Page{}, pagination.ErrInvalidCursor.WithCause(err)
		}
		appendCondition("number "+query.Order.Comparison()+" $%d", position)
	}

	arguments = append(arguments, limit+1)
	statement := fmt.Sprintf(`
		SELECT %s
		FROM invoices
		WHERE %s
		ORDER BY number %s
		LIMIT $%d`, invoiceColumns, strings.Join(conditions, " AND "), query.Order.SQL(), len(arguments))

	rows, err := s.pool.Query(ctx, statement, arguments...)
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
	var createdByID, printedByID *uuid.UUID
	var createdByEmail, printedByEmail *string

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
		&invoice.PrintAttempt,
		&createdByID,
		&createdByEmail,
		&printedByID,
		&printedByEmail,
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
	if createdByID != nil {
		invoice.IssuedBy = Author{ID: *createdByID}
	}
	if createdByEmail != nil {
		invoice.IssuedBy.Email = *createdByEmail
	}
	if printedByID != nil {
		invoice.PrintedBy = Author{ID: *printedByID}
	}
	if printedByEmail != nil {
		invoice.PrintedBy.Email = *printedByEmail
	}
	invoice.Items = make([]Item, 0)
	return invoice, nil
}

// invoiceIDs repeats one invoice id for every item, so the whole batch can be
// passed as parallel arrays to a single insert.
func invoiceIDs(id uuid.UUID, count int) []uuid.UUID {
	ids := make([]uuid.UUID, count)
	for index := range ids {
		ids[index] = id
	}
	return ids
}

// nullableAuthorID stores SQL NULL when the author is unknown, which is what an
// invoice created before authorship was recorded looks like.
func nullableAuthorID(author Author) any {
	if author.ID == uuid.Nil {
		return nil
	}
	return author.ID
}

func nullableAuthorEmail(author Author) any {
	if author.Email == "" {
		return nil
	}
	return author.Email
}
