package stock

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
)

// uniqueViolation is the PostgreSQL error code for a unique constraint.
const uniqueViolation = "23505"

// Store persists products in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a product store on top of the given pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const productColumns = `id, code, description, balance, version, created_at, updated_at`

// Create inserts a product and returns it with the values assigned by the
// database. A repeated code is reported as ErrDuplicatedCode.
//
// The opening balance is a movement like any other, so it is recorded in the
// same transaction: otherwise the history of a product would start with stock
// that appeared from nowhere.
func (s *Store) Create(ctx context.Context, product Product) (Product, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Product{}, fmt.Errorf("begin create transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		INSERT INTO products (code, description, balance)
		VALUES ($1, $2, $3)
		RETURNING `+productColumns,
		product.Code, product.Description, product.Balance)

	created, err := scanProduct(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Product{}, ErrDuplicatedCode.WithCause(err)
		}
		return Product{}, fmt.Errorf("insert product: %w", err)
	}

	if err := recordMovementTx(ctx, tx, Movement{
		ProductID:    created.ID,
		Delta:        created.Balance,
		BalanceAfter: created.Balance,
		Source:       SourceRegistration,
		ActorEmail:   actorFrom(ctx),
	}); err != nil {
		return Product{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Product{}, fmt.Errorf("commit create: %w", err)
	}
	return created, nil
}

// Update stores the current values of an existing product.
// Update writes a product only if it still looks the way the caller last saw
// it. The version it read has to match the one stored, so an edit made from a
// stale screen is refused instead of overwriting whatever happened since —
// a printed invoice that debited the balance, most of all.
func (s *Store) Update(ctx context.Context, product Product) (Product, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Product{}, fmt.Errorf("begin update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The balance before the write comes back with the row, so the movement
	// can say how far it moved. Reading it in a separate statement would be a
	// balance read and written back, which is exactly what the rest of this
	// service refuses to do.
	row := tx.QueryRow(ctx, `
		WITH previous AS (
			SELECT id, balance FROM products WHERE id = $1
		), updated AS (
			UPDATE products
			SET description = $2, balance = $3, version = version + 1, updated_at = now()
			WHERE id = $1 AND version = $4
			RETURNING `+productColumns+`
		)
		SELECT updated.*, previous.balance
		FROM updated JOIN previous ON previous.id = updated.id`,
		product.ID, product.Description, product.Balance, product.Version)

	var updated Product
	var balanceBefore int
	if err := row.Scan(&updated.ID, &updated.Code, &updated.Description, &updated.Balance,
		&updated.Version, &updated.CreatedAt, &updated.UpdatedAt, &balanceBefore); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nothing was written: either the product is gone or somebody
			// changed it first. The difference is what the caller acts on.
			return Product{}, s.explainFailedUpdate(ctx, product.ID)
		}
		return Product{}, fmt.Errorf("update product: %w", err)
	}

	// A correction typed on the form is a movement too; recordMovementTx
	// ignores the ones that only changed the description.
	if err := recordMovementTx(ctx, tx, Movement{
		ProductID:    updated.ID,
		Delta:        updated.Balance - balanceBefore,
		BalanceAfter: updated.Balance,
		Source:       SourceEdit,
		ActorEmail:   actorFrom(ctx),
	}); err != nil {
		return Product{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Product{}, fmt.Errorf("commit update: %w", err)
	}
	return updated, nil
}

// explainFailedUpdate tells a missing product from one that moved on.
func (s *Store) explainFailedUpdate(ctx context.Context, id uuid.UUID) error {
	var version int
	err := s.pool.QueryRow(ctx, `SELECT version FROM products WHERE id = $1`, id).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProductNotFound
	}
	if err != nil {
		return fmt.Errorf("read product version: %w", err)
	}
	return ErrProductChanged
}

// GetByID returns a single product, or ErrProductNotFound.
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (Product, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE id = $1`, id)

	product, err := scanProduct(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Product{}, ErrProductNotFound
		}
		return Product{}, fmt.Errorf("select product by id: %w", err)
	}
	return product, nil
}

// GetByCode returns a single product by code, ignoring case.
func (s *Store) GetByCode(ctx context.Context, code string) (Product, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+productColumns+` FROM products WHERE upper(code) = upper($1)`, code)

	product, err := scanProduct(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Product{}, ErrProductNotFound
		}
		return Product{}, fmt.Errorf("select product by code: %w", err)
	}
	return product, nil
}

// Page is a slice of the catalogue plus how to ask for the next one.
type Page struct {
	Items []Product
	// NextCursor is empty when the last page was reached.
	NextCursor string
}

// List returns a page of the catalogue.
//
// Filtering, ordering and paging are all done by the database in a single
// parameterized query: the service never reads more than one page into memory,
// no matter how large the catalogue is. The page is cut by the value of the
// column being ordered by, with the code as a tiebreaker, so a product
// registered while someone is paging never repeats or hides another one.
func (s *Store) List(ctx context.Context, query Query) (Page, error) {
	limit := pagination.NormalizeLimit(query.Limit)
	if query.Sort == "" {
		query.Sort = SortByCode
	}
	if query.Order == "" {
		query.Order = pagination.Ascending
	}

	cursor, err := pagination.Decode(query.Cursor)
	if err != nil {
		return Page{}, err
	}

	// Conditions are only added for the filters that were actually asked for.
	// Writing them as "$1 = '' OR code ILIKE ..." would read the same, but a
	// condition that is true for every row when the parameter is empty cannot
	// use an index, so the search would end up reading the whole table.
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

	if query.Search != "" {
		// The term is a bound parameter; only the wildcards are ours.
		appendCondition(`(code ILIKE '%%' || $%d || '%%' OR description ILIKE '%%' || $%[1]d || '%%')`, query.Search)
	}
	if query.MinBalance != nil {
		appendCondition("balance >= $%d", *query.MinBalance)
	}
	if query.MaxBalance != nil {
		appendCondition("balance <= $%d", *query.MaxBalance)
	}

	comparison := query.Order.Comparison()
	if cursor.Key != "" {
		switch query.Sort {
		case SortByBalance:
			// Balance repeats, so the cursor compares the pair (balance, code).
			sortValue, err := strconv.Atoi(cursor.Sort)
			if err != nil {
				return Page{}, pagination.ErrInvalidCursor.WithCause(err)
			}
			appendCondition("(balance, upper(code)) "+comparison+" ($%d, $%d)", sortValue, cursor.Key)
		default:
			appendCondition("upper(code) "+comparison+" $%d", cursor.Key)
		}
	}

	orderBy := fmt.Sprintf("upper(code) %s", query.Order.SQL())
	if query.Sort == SortByBalance {
		orderBy = fmt.Sprintf("balance %s, upper(code) %s", query.Order.SQL(), query.Order.SQL())
	}

	// One extra row tells us whether there is another page, without counting.
	arguments = append(arguments, limit+1)
	statement := fmt.Sprintf(`
		SELECT %s
		FROM products
		WHERE %s
		ORDER BY %s
		LIMIT $%d`, productColumns, strings.Join(conditions, " AND "), orderBy, len(arguments))

	rows, err := s.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return Page{}, fmt.Errorf("select products: %w", err)
	}
	defer rows.Close()

	products := make([]Product, 0, limit)
	for rows.Next() {
		product, err := scanProduct(rows)
		if err != nil {
			return Page{}, fmt.Errorf("scan product: %w", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("read products: %w", err)
	}

	page := Page{Items: products}
	if len(products) > limit {
		page.Items = products[:limit]
		last := page.Items[len(page.Items)-1]
		next := pagination.Cursor{Key: strings.ToUpper(last.Code), ID: last.ID.String()}
		if query.Sort == SortByBalance {
			next.Sort = strconv.Itoa(last.Balance)
		}
		page.NextCursor = pagination.Encode(next)
	}
	return page, nil
}

// FindByIDs returns the products matching the given ids, in code order.
// Missing ids are simply absent from the result: the caller decides whether
// that is an error.
func (s *Store) FindByIDs(ctx context.Context, ids []uuid.UUID) ([]Product, error) {
	if len(ids) == 0 {
		return []Product{}, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+productColumns+`
		FROM products
		WHERE id = ANY($1)
		ORDER BY upper(code)`, ids)
	if err != nil {
		return nil, fmt.Errorf("select products by id: %w", err)
	}
	defer rows.Close()

	products := make([]Product, 0, len(ids))
	for rows.Next() {
		product, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read products: %w", err)
	}
	return products, nil
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanProduct(row rowScanner) (Product, error) {
	var product Product
	err := row.Scan(
		&product.ID,
		&product.Code,
		&product.Description,
		&product.Balance,
		&product.Version,
		&product.CreatedAt,
		&product.UpdatedAt,
	)
	return product, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
