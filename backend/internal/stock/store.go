package stock

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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

const productColumns = `id, code, description, balance, created_at, updated_at`

// Create inserts a product and returns it with the values assigned by the
// database. A repeated code is reported as ErrDuplicatedCode.
func (s *Store) Create(ctx context.Context, product Product) (Product, error) {
	row := s.pool.QueryRow(ctx, `
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
	return created, nil
}

// Update stores the current values of an existing product.
func (s *Store) Update(ctx context.Context, product Product) (Product, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE products
		SET description = $2, balance = $3, updated_at = now()
		WHERE id = $1
		RETURNING `+productColumns,
		product.ID, product.Description, product.Balance)

	updated, err := scanProduct(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Product{}, ErrProductNotFound
		}
		return Product{}, fmt.Errorf("update product: %w", err)
	}
	return updated, nil
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

// List returns every product ordered by code, optionally filtered by a search
// term matched against code and description.
func (s *Store) List(ctx context.Context, search string) ([]Product, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+productColumns+`
		FROM products
		WHERE $1 = '' OR code ILIKE '%' || $1 || '%' OR description ILIKE '%' || $1 || '%'
		ORDER BY upper(code)`, search)
	if err != nil {
		return nil, fmt.Errorf("select products: %w", err)
	}
	defer rows.Close()

	products := make([]Product, 0)
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
		&product.CreatedAt,
		&product.UpdatedAt,
	)
	return product, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
