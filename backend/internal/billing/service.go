package billing

import (
	"context"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/billing/stockclient"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
)

// InvoiceRepository is the persistence the service depends on.
type InvoiceRepository interface {
	Create(ctx context.Context, items []Item, issuedBy Author) (Invoice, error)
	GetByID(ctx context.Context, id uuid.UUID) (Invoice, error)
	List(ctx context.Context, query Query) (Page, error)
}

// ProductLookup resolves product ids against the stock service.
type ProductLookup interface {
	Lookup(ctx context.Context, ids []uuid.UUID) ([]stockclient.Product, error)
}

// Service holds the billing use cases.
type Service struct {
	invoices InvoiceRepository
	products ProductLookup
	printing PrintStore
}

// NewService builds a billing service.
func NewService(invoices InvoiceRepository, products ProductLookup, printing PrintStore) *Service {
	return &Service{invoices: invoices, products: products, printing: printing}
}

// CreateInvoice validates the requested lines, resolves the products against
// the stock service and stores an open invoice. Balances are not reserved
// here: stock is only debited when the invoice is printed.
// CreateInvoice issues an invoice on behalf of the person making the request.
//
// Who issued it is taken from the authenticated context rather than from the
// body: an invoice is a fiscal document, and its author is not something the
// caller gets to claim.
func (s *Service) CreateInvoice(ctx context.Context, inputs []ItemInput) (Invoice, error) {
	merged, err := NewItemInputs(inputs)
	if err != nil {
		return Invoice{}, err
	}

	ids := make([]uuid.UUID, 0, len(merged))
	for _, input := range merged {
		ids = append(ids, input.ProductID)
	}

	products, err := s.products.Lookup(ctx, ids)
	if err != nil {
		return Invoice{}, err
	}

	byID := make(map[uuid.UUID]stockclient.Product, len(products))
	for _, product := range products {
		byID[product.ID] = product
	}

	items := make([]Item, 0, len(merged))
	for _, input := range merged {
		product, found := byID[input.ProductID]
		if !found {
			// The stock service answers with an error for unknown products,
			// so this only guards against an incomplete answer.
			return Invoice{}, ErrInvalidInvoice.WithDetails(map[string]string{
				"product_id": input.ProductID.String() + " was not found in stock",
			})
		}
		items = append(items, Item{
			ProductID:          product.ID,
			ProductCode:        product.Code,
			ProductDescription: product.Description,
			Quantity:           input.Quantity,
		})
	}

	return s.invoices.Create(ctx, items, authorFrom(ctx))
}

// GetInvoice returns a single invoice with its items.
func (s *Service) GetInvoice(ctx context.Context, id uuid.UUID) (Invoice, error) {
	return s.invoices.GetByID(ctx, id)
}

// ListInvoices returns a page of invoices for the given filters.
func (s *Service) ListInvoices(ctx context.Context, query Query) (Page, error) {
	return s.invoices.List(ctx, query)
}

// authorFrom reads who is making the request. An unauthenticated context has no
// author, which is how a call from a test or an internal path is recorded.
func authorFrom(ctx context.Context) Author {
	user, err := authn.UserFrom(ctx)
	if err != nil {
		return Author{}
	}
	return Author{ID: user.ID, Email: user.Email}
}
