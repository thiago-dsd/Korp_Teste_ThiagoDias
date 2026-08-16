package billing

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/aiclient"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/ratelimit"
)

// API exposes the billing use cases over HTTP.
type API struct {
	service   *Service
	assistant *DraftAssistant
}

// NewAPI builds the HTTP layer of the billing service. The assistant is
// optional: without it the drafting endpoint reports that it is not available
// and the screens keep working by hand.
func NewAPI(service *Service, assistant *DraftAssistant) *API {
	return &API{service: service, assistant: assistant}
}

// Routes registers the invoice endpoints on the given mux. They are only
// served to a signed in user.
//
// Throttling sits inside the guard so it counts against the person signed in,
// and each category gets its own allowance: reading an invoice while it prints
// must not be spent by the same budget as issuing invoices.
func (a *API) Routes(mux *http.ServeMux, verifier *authn.Verifier, limits Limits) {
	guard := authn.RequireUser(verifier)
	read := ratelimit.Middleware(limits.Limiter, limits.Read, ratelimit.ByUser)
	write := ratelimit.Middleware(limits.Limiter, limits.Write, ratelimit.ByUser)
	assistant := ratelimit.Middleware(limits.Limiter, limits.AI, ratelimit.ByUser)

	mux.Handle("POST /invoices", guard(write(http.HandlerFunc(a.createInvoice))))
	mux.Handle("GET /invoices", guard(read(http.HandlerFunc(a.listInvoices))))
	mux.Handle("GET /invoices/{id}", guard(read(http.HandlerFunc(a.getInvoice))))
	mux.Handle("POST /invoices/{id}/print", guard(write(http.HandlerFunc(a.printInvoice))))

	// Drafting is paid for on every call, so it has the tightest allowance.
	mux.Handle("POST /invoices/draft", guard(assistant(http.HandlerFunc(a.draftInvoice))))
	mux.Handle("GET /invoices/draft", guard(read(http.HandlerFunc(a.assistantStatus))))
}

// Limits are the policies the endpoints are served under.
type Limits struct {
	Limiter ratelimit.Limiter
	Read    ratelimit.Policy
	Write   ratelimit.Policy
	AI      ratelimit.Policy
}

type draftRequest struct {
	Text string `json:"text"`
}

type draftResponse struct {
	Items    []draftItemResponse `json:"items"`
	Warnings []string            `json:"warnings"`
	Model    string              `json:"model"`
}

type draftItemResponse struct {
	ProductID          uuid.UUID `json:"product_id"`
	ProductCode        string    `json:"product_code"`
	ProductDescription string    `json:"product_description"`
	Quantity           int       `json:"quantity"`
	Balance            int       `json:"balance"`
}

type assistantStatusResponse struct {
	Available bool `json:"available"`
}

// draftInvoice suggests invoice lines for a sentence. It only suggests: the
// invoice is created by the regular endpoint once the operator confirms.
func (a *API) draftInvoice(w http.ResponseWriter, r *http.Request) {
	if a.assistant == nil || !a.assistant.Available() {
		httpx.WriteError(w, r, aiclient.ErrNotConfigured)
		return
	}

	var request draftRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	draft, err := a.assistant.Draft(r.Context(), request.Text)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	items := make([]draftItemResponse, 0, len(draft.Lines))
	for _, line := range draft.Lines {
		items = append(items, draftItemResponse{
			ProductID:          line.ProductID,
			ProductCode:        line.ProductCode,
			ProductDescription: line.ProductDescription,
			Quantity:           line.Quantity,
			Balance:            line.Balance,
		})
	}
	httpx.WriteJSON(w, r, http.StatusOK, draftResponse{Items: items, Warnings: draft.Warnings, Model: draft.Model})
}

// assistantStatus lets the screen know whether to offer the assistant at all.
func (a *API) assistantStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, r, http.StatusOK, assistantStatusResponse{
		Available: a.assistant != nil && a.assistant.Available(),
	})
}

type createInvoiceRequest struct {
	Items []invoiceItemRequest `json:"items"`
}

type invoiceItemRequest struct {
	ProductID uuid.UUID `json:"product_id"`
	Quantity  int       `json:"quantity"`
}

type invoiceResponse struct {
	ID     uuid.UUID             `json:"id"`
	Number int64                 `json:"number"`
	Status Status                `json:"status"`
	Items  []invoiceItemResponse `json:"items"`
	// Failure explains why the last print attempt did not go through, so the
	// screen can show the operator what happened.
	Failure   *invoiceFailure `json:"failure"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	PrintedAt *time.Time      `json:"printed_at"`
}

type invoiceFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type invoiceItemResponse struct {
	ID                 uuid.UUID `json:"id"`
	ProductID          uuid.UUID `json:"product_id"`
	ProductCode        string    `json:"product_code"`
	ProductDescription string    `json:"product_description"`
	Quantity           int       `json:"quantity"`
}

type invoiceListResponse struct {
	Items []invoiceResponse `json:"items"`
	// NextCursor is passed back to read the following page; it is empty on the
	// last one.
	NextCursor string `json:"next_cursor,omitempty"`
}

func (a *API) createInvoice(w http.ResponseWriter, r *http.Request) {
	var request createInvoiceRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	inputs := make([]ItemInput, 0, len(request.Items))
	for _, item := range request.Items {
		inputs = append(inputs, ItemInput{ProductID: item.ProductID, Quantity: item.Quantity})
	}

	invoice, err := a.service.CreateInvoice(r.Context(), inputs)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	w.Header().Set("Location", "/invoices/"+invoice.ID.String())
	httpx.WriteJSON(w, r, http.StatusCreated, toInvoiceResponse(invoice))
}

func (a *API) listInvoices(w http.ResponseWriter, r *http.Request) {
	query, err := ParseQuery(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	page, err := a.service.ListInvoices(r.Context(), query)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	items := make([]invoiceResponse, 0, len(page.Items))
	for _, invoice := range page.Items {
		items = append(items, toInvoiceResponse(invoice))
	}
	httpx.WriteJSON(w, r, http.StatusOK, invoiceListResponse{Items: items, NextCursor: page.NextCursor})
}

func (a *API) getInvoice(w http.ResponseWriter, r *http.Request) {
	id, err := invoiceID(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	invoice, err := a.service.GetInvoice(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toInvoiceResponse(invoice))
}

// printInvoice starts printing an invoice. It answers 202 Accepted with the
// invoice in PRINTING: the balances are debited asynchronously and the client
// follows the outcome by reading the invoice.
func (a *API) printInvoice(w http.ResponseWriter, r *http.Request) {
	id, err := invoiceID(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	invoice, err := a.service.RequestPrint(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusAccepted, toInvoiceResponse(invoice))
}

func invoiceID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.Invalid("invalid_invoice_id", "Invoice id is not a valid identifier.").WithCause(err)
	}
	return id, nil
}

func toInvoiceResponse(invoice Invoice) invoiceResponse {
	items := make([]invoiceItemResponse, 0, len(invoice.Items))
	for _, item := range invoice.Items {
		items = append(items, invoiceItemResponse{
			ID:                 item.ID,
			ProductID:          item.ProductID,
			ProductCode:        item.ProductCode,
			ProductDescription: item.ProductDescription,
			Quantity:           item.Quantity,
		})
	}
	var failure *invoiceFailure
	if invoice.FailureCode != "" {
		failure = &invoiceFailure{Code: invoice.FailureCode, Message: invoice.FailureMessage}
	}

	return invoiceResponse{
		ID:        invoice.ID,
		Number:    invoice.Number,
		Status:    invoice.Status,
		Items:     items,
		Failure:   failure,
		CreatedAt: invoice.CreatedAt,
		UpdatedAt: invoice.UpdatedAt,
		PrintedAt: invoice.PrintedAt,
	}
}
