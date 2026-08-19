package billing

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/aiclient"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/authn"
	"github.com/thiagodias/korp-invoices/internal/platform/bulk"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/ratelimit"
)

// API exposes the billing use cases over HTTP.
type API struct {
	service   *Service
	assistant *DraftAssistant
	search    *SearchAssistant
}

// NewAPI builds the HTTP layer of the billing service. The assistant is
// optional: without it the drafting endpoint reports that it is not available
// and the screens keep working by hand.
func NewAPI(service *Service, assistant *DraftAssistant, search *SearchAssistant) *API {
	return &API{service: service, assistant: assistant, search: search}
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

	// Searching costs a model call like drafting does, so it shares the same
	// allowance. It answers filters, never invoices: the listing endpoint above
	// is what reads them.
	mux.Handle("POST /invoices/search", guard(assistant(http.HandlerFunc(a.searchInvoices))))

	// One bulk call does the work of up to a hundred, so it has its own
	// allowance instead of spending the ordinary write budget.
	batch := ratelimit.Middleware(limits.Limiter, limits.Bulk, ratelimit.ByUser)
	mux.Handle("POST /invoices/print", guard(batch(http.HandlerFunc(a.printInvoices))))
}

type bulkPrintRequest struct {
	InvoiceIDs []uuid.UUID `json:"invoice_ids"`
}

// printInvoices starts printing several invoices, which is what closing a day
// of work looks like. Each invoice is independent: one that cannot be printed
// does not hold back the others, and the answer says which is which.
func (a *API) printInvoices(w http.ResponseWriter, r *http.Request) {
	var request bulkPrintRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	results, err := a.service.PrintInvoices(r.Context(), request.InvoiceIDs)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	bulk.Write(w, r, bulk.NewResponse(false, results))
}

// Limits are the policies the endpoints are served under.
type Limits struct {
	Limiter ratelimit.Limiter
	Read    ratelimit.Policy
	Write   ratelimit.Policy
	AI      ratelimit.Policy
	Bulk    ratelimit.Policy
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

// authorResponse is who did something, when it is known.
type authorResponse struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
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

type searchRequest struct {
	Text string `json:"text"`
}

type searchResponse struct {
	Filters  map[string]string `json:"filters"`
	Warnings []string          `json:"warnings"`
	Model    string            `json:"model"`
}

// searchInvoices reads a question such as "open invoices from August with
// bolts" and answers the filters that ask for it.
//
// It deliberately does not return invoices. The filters go back to the screen,
// which puts them in the URL where the listing filters already live, and the
// ordinary listing endpoint reads them — so what the assistant produces is
// visible, editable and bookmarkable rather than a black box answer.
func (a *API) searchInvoices(w http.ResponseWriter, r *http.Request) {
	if a.search == nil || !a.search.Available() {
		httpx.WriteError(w, r, aiclient.ErrNotConfigured)
		return
	}

	var request searchRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	search, err := a.search.Search(r.Context(), request.Text)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	httpx.WriteJSON(w, r, http.StatusOK, searchResponse{
		Filters:  search.Filters,
		Warnings: search.Warnings,
		Model:    search.Model,
	})
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
	// IssuedBy and PrintedBy are who did it, when it is known. Invoices issued
	// before authorship was recorded have neither.
	IssuedBy  *authorResponse `json:"issued_by,omitempty"`
	PrintedBy *authorResponse `json:"printed_by,omitempty"`
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

// toAuthorResponse omits an author that was never recorded, so an invoice
// issued before this existed simply has no field.
func toAuthorResponse(author Author) *authorResponse {
	if !author.Recorded() {
		return nil
	}
	return &authorResponse{ID: author.ID.String(), Email: author.Email}
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

	response := invoiceResponse{
		ID:        invoice.ID,
		Number:    invoice.Number,
		Status:    invoice.Status,
		Items:     items,
		Failure:   failure,
		CreatedAt: invoice.CreatedAt,
		UpdatedAt: invoice.UpdatedAt,
		PrintedAt: invoice.PrintedAt,
		IssuedBy:  toAuthorResponse(invoice.IssuedBy),
		PrintedBy: toAuthorResponse(invoice.PrintedBy),
	}
	return response
}
