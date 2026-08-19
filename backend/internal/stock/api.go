package stock

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/bulk"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
	"github.com/thiagodias/korp-invoices/internal/platform/ratelimit"
)

// maxLookupIDs bounds a single internal lookup, matching the maximum number of
// distinct products an invoice may hold.
const maxLookupIDs = 100

// API exposes the stock use cases over HTTP.
type API struct {
	service  *Service
	failures *FailureSwitch
}

// NewAPI builds the HTTP layer of the stock service. The failure switch is
// exposed on an internal endpoint so the failure scenario can be demonstrated.
func NewAPI(service *Service, failures *FailureSwitch) *API {
	return &API{service: service, failures: failures}
}

// Routes registers the product endpoints on the given mux. They are only
// served to a signed in user: the catalogue and its balances are not public.
//
// Throttling sits inside the guard so it counts against the person signed in
// rather than against their address: a whole office behind one address must
// not share a single allowance.
func (a *API) Routes(mux *http.ServeMux, verifier *authn.Verifier, limits Limits) {
	guard := authn.RequireUser(verifier)
	read := ratelimit.Middleware(limits.Limiter, limits.Read, ratelimit.ByUser)
	write := ratelimit.Middleware(limits.Limiter, limits.Write, ratelimit.ByUser)

	// Reading the catalogue is part of issuing an invoice, so everybody signed
	// in may do it. Changing it rewrites what invoices are made of — a balance
	// corrected here changes what the next print may take — so it is kept to
	// administrators.
	admin := authn.RequireRole(authn.RoleAdmin)

	mux.Handle("POST /products", guard(admin(write(http.HandlerFunc(a.createProduct)))))
	mux.Handle("GET /products", guard(read(http.HandlerFunc(a.listProducts))))
	mux.Handle("GET /products/{id}", guard(read(http.HandlerFunc(a.getProduct))))
	// Reading why a balance is what it is belongs with reading the balance
	// itself, so an operator investigating a mismatch does not need an
	// administrator to look for them.
	mux.Handle("GET /products/{id}/movements", guard(read(http.HandlerFunc(a.listMovements))))
	mux.Handle("PUT /products/{id}", guard(admin(write(http.HandlerFunc(a.updateProduct)))))

	// One bulk call does the work of up to a hundred, so it has its own
	// allowance instead of spending the ordinary write budget.
	batch := ratelimit.Middleware(limits.Limiter, limits.Bulk, ratelimit.ByUser)
	mux.Handle("POST /products/bulk", guard(admin(batch(http.HandlerFunc(a.createProducts)))))
	mux.Handle("POST /products/adjustments", guard(admin(batch(http.HandlerFunc(a.adjustBalances)))))
}

type bulkProductsRequest struct {
	Items []productRequest `json:"items"`
}

type adjustmentsRequest struct {
	Items []adjustmentRequest `json:"items"`
}

type adjustmentRequest struct {
	ProductID   *uuid.UUID `json:"product_id,omitempty"`
	ProductCode string     `json:"product_code,omitempty"`
	Delta       int        `json:"delta"`
	Reason      string     `json:"reason,omitempty"`
}

// createProducts registers several products. Items are independent: a bad row
// does not stop the good ones, and the answer says which is which.
func (a *API) createProducts(w http.ResponseWriter, r *http.Request) {
	var request bulkProductsRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	inputs := make([]ProductInput, 0, len(request.Items))
	for _, item := range request.Items {
		inputs = append(inputs, ProductInput{Code: item.Code, Description: item.Description, Balance: item.Balance})
	}

	results, err := a.service.CreateProducts(r.Context(), inputs)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	bulk.Write(w, r, bulk.NewResponse(false, results))
}

// adjustBalances applies several stock movements together. They belong to one
// document, so either all of them land or none does.
func (a *API) adjustBalances(w http.ResponseWriter, r *http.Request) {
	var request adjustmentsRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	adjustments := make([]Adjustment, 0, len(request.Items))
	for _, item := range request.Items {
		adjustment := Adjustment{ProductCode: item.ProductCode, Delta: item.Delta, Reason: item.Reason}
		if item.ProductID != nil {
			adjustment.ProductID = *item.ProductID
		}
		adjustments = append(adjustments, adjustment)
	}

	results, err := a.service.AdjustBalances(r.Context(), adjustments)
	if err != nil {
		// A rejected batch still carries the per item answer, so the caller
		// sees which one stopped it; anything else is an ordinary failure.
		if errors.Is(err, ErrAdjustmentRejected) {
			httpx.WriteJSON(w, r, http.StatusConflict, bulk.NewResponse(true, results))
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	bulk.Write(w, r, bulk.NewResponse(true, results))
}

// Limits are the policies the endpoints are served under.
type Limits struct {
	Limiter ratelimit.Limiter
	Read    ratelimit.Policy
	Write   ratelimit.Policy
	Bulk    ratelimit.Policy
}

// InternalRoutes registers the endpoints consumed by other services.
//
// They are not throttled: they carry the shared service token, they all arrive
// from the same address, and one of them is on the path that prints an invoice.
// Throttling them would mean the system limiting itself.
func (a *API) InternalRoutes(mux *http.ServeMux, serviceToken string) {
	guard := httpx.RequireServiceToken(serviceToken)
	mux.Handle("POST /internal/products/lookup", guard(http.HandlerFunc(a.lookupProducts)))
	mux.Handle("GET /internal/products", guard(http.HandlerFunc(a.listProducts)))
	mux.Handle("POST /internal/failure-simulation", guard(http.HandlerFunc(a.setFailureSimulation)))
	mux.Handle("GET /internal/failure-simulation", guard(http.HandlerFunc(a.getFailureSimulation)))
}

type failureSimulationRequest struct {
	Enabled bool `json:"enabled"`
}

type failureSimulationResponse struct {
	Enabled bool `json:"enabled"`
}

// setFailureSimulation makes the service reject print requests on purpose, so
// the recovery path can be shown without stopping anything.
func (a *API) setFailureSimulation(w http.ResponseWriter, r *http.Request) {
	var request failureSimulationRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if a.failures == nil {
		httpx.WriteError(w, r, apperr.Internal("failure_simulation_unavailable",
			"Failure simulation is not configured."))
		return
	}

	a.failures.Enable(request.Enabled)
	httpx.WriteJSON(w, r, http.StatusOK, failureSimulationResponse{Enabled: a.failures.Enabled()})
}

func (a *API) getFailureSimulation(w http.ResponseWriter, r *http.Request) {
	enabled := a.failures != nil && a.failures.Enabled()
	httpx.WriteJSON(w, r, http.StatusOK, failureSimulationResponse{Enabled: enabled})
}

type lookupRequest struct {
	ProductIDs []uuid.UUID `json:"product_ids"`
}

// lookupProducts resolves product ids into full products, reporting an error
// when any of them is unknown. Billing uses it to validate invoice items.
func (a *API) lookupProducts(w http.ResponseWriter, r *http.Request) {
	var request lookupRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if len(request.ProductIDs) == 0 {
		httpx.WriteError(w, r, apperr.Invalid("invalid_lookup", "At least one product id is required.").
			WithDetails(map[string]string{"product_ids": "must not be empty"}))
		return
	}
	if len(request.ProductIDs) > maxLookupIDs {
		httpx.WriteError(w, r, apperr.Invalid("invalid_lookup", "Too many product ids.").
			WithDetails(map[string]string{"product_ids": "must contain at most 100 ids"}))
		return
	}

	products, err := a.service.FindProducts(r.Context(), request.ProductIDs)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	items := make([]productResponse, 0, len(products))
	for _, product := range products {
		items = append(items, toProductResponse(product))
	}
	httpx.WriteJSON(w, r, http.StatusOK, productListResponse{Items: items})
}

type productRequest struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	Balance     int    `json:"balance"`
}

type updateProductRequest struct {
	Description string `json:"description"`
	Balance     int    `json:"balance"`
	// Version is the one the caller last read. It is what makes a concurrent
	// change visible instead of silently overwritten.
	Version int `json:"version"`
}

type productResponse struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Description string    `json:"description"`
	Balance     int       `json:"balance"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type productListResponse struct {
	Items []productResponse `json:"items"`
	// NextCursor is passed back to read the following page; it is empty on the
	// last one.
	NextCursor string `json:"next_cursor,omitempty"`
}

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) {
	var request productRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	product, err := a.service.CreateProduct(r.Context(), request.Code, request.Description, request.Balance)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	w.Header().Set("Location", "/products/"+product.ID.String())
	httpx.WriteJSON(w, r, http.StatusCreated, toProductResponse(product))
}

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	query, err := ParseQuery(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	page, err := a.service.ListProducts(r.Context(), query)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	items := make([]productResponse, 0, len(page.Items))
	for _, product := range page.Items {
		items = append(items, toProductResponse(product))
	}
	httpx.WriteJSON(w, r, http.StatusOK, productListResponse{Items: items, NextCursor: page.NextCursor})
}

func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := productID(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	product, err := a.service.GetProduct(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toProductResponse(product))
}

func (a *API) updateProduct(w http.ResponseWriter, r *http.Request) {
	id, err := productID(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	var request updateProductRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	product, err := a.service.UpdateProduct(r.Context(), id, request.Description, request.Balance, request.Version)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toProductResponse(product))
}

func productID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.Invalid("invalid_product_id", "Product id is not a valid identifier.").WithCause(err)
	}
	return id, nil
}

func toProductResponse(product Product) productResponse {
	return productResponse{
		ID:          product.ID,
		Code:        product.Code,
		Description: product.Description,
		Balance:     product.Balance,
		Version:     product.Version,
		CreatedAt:   product.CreatedAt,
		UpdatedAt:   product.UpdatedAt,
	}
}

// movementResponse is one change to a balance as it travels on the wire.
type movementResponse struct {
	ID           uuid.UUID `json:"id"`
	Delta        int       `json:"delta"`
	BalanceAfter int       `json:"balance_after"`
	Source       string    `json:"source"`
	Reason       string    `json:"reason,omitempty"`
	InvoiceID    string    `json:"invoice_id,omitempty"`
	ActorEmail   string    `json:"actor_email,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type movementListResponse struct {
	Items      []movementResponse `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

// listMovements answers why the balance of a product is what it is.
func (a *API) listMovements(w http.ResponseWriter, r *http.Request) {
	id, err := productID(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	limit, err := pagination.ParseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpx.WriteError(w, r, ErrInvalidFilter.WithDetails(map[string]string{
			"limit": fmt.Sprintf("must be a number between 1 and %d", pagination.MaxLimit),
		}))
		return
	}

	page, err := a.service.ListMovements(r.Context(), id, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	items := make([]movementResponse, 0, len(page.Items))
	for _, movement := range page.Items {
		item := movementResponse{
			ID:           movement.ID,
			Delta:        movement.Delta,
			BalanceAfter: movement.BalanceAfter,
			Source:       string(movement.Source),
			Reason:       movement.Reason,
			ActorEmail:   movement.ActorEmail,
			CreatedAt:    movement.CreatedAt,
		}
		if movement.InvoiceID != uuid.Nil {
			item.InvoiceID = movement.InvoiceID.String()
		}
		items = append(items, item)
	}
	httpx.WriteJSON(w, r, http.StatusOK, movementListResponse{Items: items, NextCursor: page.NextCursor})
}
