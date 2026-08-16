package stock

import (
	"context"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/bulk"
)

// ProductRepository is the persistence the service depends on.
type ProductRepository interface {
	Create(ctx context.Context, product Product) (Product, error)
	Update(ctx context.Context, product Product) (Product, error)
	GetByID(ctx context.Context, id uuid.UUID) (Product, error)
	GetByCode(ctx context.Context, code string) (Product, error)
	List(ctx context.Context, query Query) (Page, error)
	FindByIDs(ctx context.Context, ids []uuid.UUID) ([]Product, error)
	Adjust(ctx context.Context, adjustments []Adjustment) ([]bulk.Result, error)
}

// Service holds the stock use cases.
type Service struct {
	products ProductRepository
}

// NewService builds a stock service backed by the given repository.
func NewService(products ProductRepository) *Service {
	return &Service{products: products}
}

// CreateProduct validates and stores a new product.
func (s *Service) CreateProduct(ctx context.Context, code, description string, balance int) (Product, error) {
	product, err := NewProduct(code, description, balance)
	if err != nil {
		return Product{}, err
	}
	return s.products.Create(ctx, product)
}

// UpdateProduct changes the description and the balance of a product.
func (s *Service) UpdateProduct(ctx context.Context, id uuid.UUID, description string, balance int) (Product, error) {
	product, err := s.products.GetByID(ctx, id)
	if err != nil {
		return Product{}, err
	}
	if err := product.Update(description, balance); err != nil {
		return Product{}, err
	}
	return s.products.Update(ctx, product)
}

// GetProduct returns a single product by id.
func (s *Service) GetProduct(ctx context.Context, id uuid.UUID) (Product, error) {
	return s.products.GetByID(ctx, id)
}

// ListProducts returns a page of the catalogue.
func (s *Service) ListProducts(ctx context.Context, query Query) (Page, error) {
	return s.products.List(ctx, query)
}

// FindProducts returns the products with the given ids, reporting an error
// when any of them is missing. Billing uses it to validate invoice items.
func (s *Service) FindProducts(ctx context.Context, ids []uuid.UUID) ([]Product, error) {
	products, err := s.products.FindByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	found := make(map[uuid.UUID]bool, len(products))
	for _, product := range products {
		found[product.ID] = true
	}
	for _, id := range ids {
		if !found[id] {
			return nil, ErrProductNotFound.WithDetails(map[string]string{"product_id": id.String()})
		}
	}
	return products, nil
}
