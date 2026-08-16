package billing_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/billing"
	"github.com/thiagodias/korp-invoices/internal/billing/stockclient"
	"github.com/thiagodias/korp-invoices/internal/platform/authn/authntest"
)

// signer issues the access tokens the endpoints require.
var signer *authntest.Signer

// memoryInvoices is an in-memory InvoiceRepository for handler tests.
type memoryInvoices struct {
	invoices map[uuid.UUID]billing.Invoice
	sequence int64
	failWith error
	// printRequests records the invoices a print was requested for.
	printRequests []uuid.UUID
}

func newMemoryInvoices() *memoryInvoices {
	return &memoryInvoices{invoices: map[uuid.UUID]billing.Invoice{}}
}

func (r *memoryInvoices) Create(ctx context.Context, items []billing.Item) (billing.Invoice, error) {
	if r.failWith != nil {
		return billing.Invoice{}, r.failWith
	}
	r.sequence++
	for i := range items {
		items[i].ID = uuid.New()
	}
	invoice := billing.Invoice{
		ID:        uuid.New(),
		Number:    r.sequence,
		Status:    billing.StatusOpen,
		Items:     items,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	r.invoices[invoice.ID] = invoice
	return invoice, nil
}

func (r *memoryInvoices) GetByID(ctx context.Context, id uuid.UUID) (billing.Invoice, error) {
	if r.failWith != nil {
		return billing.Invoice{}, r.failWith
	}
	invoice, ok := r.invoices[id]
	if !ok {
		return billing.Invoice{}, billing.ErrInvoiceNotFound
	}
	return invoice, nil
}

// StartPrinting mirrors what the store does: the invoice moves to PRINTING and
// the print request is recorded, both or neither.
func (r *memoryInvoices) StartPrinting(ctx context.Context, id uuid.UUID) (billing.Invoice, error) {
	if r.failWith != nil {
		return billing.Invoice{}, r.failWith
	}
	invoice, ok := r.invoices[id]
	if !ok {
		return billing.Invoice{}, billing.ErrInvoiceNotFound
	}
	if err := invoice.StartPrinting(time.Now().UTC()); err != nil {
		return billing.Invoice{}, err
	}
	r.invoices[id] = invoice
	r.printRequests = append(r.printRequests, id)
	return invoice, nil
}

func (r *memoryInvoices) ReopenStalePrintings(ctx context.Context, timeout time.Duration, code, message string) (int, error) {
	if r.failWith != nil {
		return 0, r.failWith
	}
	reopened := 0
	for id, invoice := range r.invoices {
		if invoice.Status != billing.StatusPrinting || invoice.PrintingSince == nil {
			continue
		}
		if time.Since(*invoice.PrintingSince) < timeout {
			continue
		}
		if err := invoice.Reopen(code, message); err != nil {
			return reopened, err
		}
		r.invoices[id] = invoice
		reopened++
	}
	return reopened, nil
}

func (r *memoryInvoices) List(ctx context.Context, status string) ([]billing.Invoice, error) {
	if r.failWith != nil {
		return nil, r.failWith
	}
	invoices := make([]billing.Invoice, 0, len(r.invoices))
	for _, invoice := range r.invoices {
		if status == "" || string(invoice.Status) == status {
			invoices = append(invoices, invoice)
		}
	}
	return invoices, nil
}

// stubLookup answers product lookups without touching the network.
type stubLookup struct {
	products map[uuid.UUID]stockclient.Product
	failWith error
	calls    int
}

func newStubLookup(products ...stockclient.Product) *stubLookup {
	byID := make(map[uuid.UUID]stockclient.Product, len(products))
	for _, product := range products {
		byID[product.ID] = product
	}
	return &stubLookup{products: byID}
}

func (s *stubLookup) Lookup(ctx context.Context, ids []uuid.UUID) ([]stockclient.Product, error) {
	s.calls++
	if s.failWith != nil {
		return nil, s.failWith
	}
	found := make([]stockclient.Product, 0, len(ids))
	for _, id := range ids {
		product, ok := s.products[id]
		if !ok {
			return nil, billing.ErrInvalidInvoice.WithDetails(map[string]string{"product_id": id.String()})
		}
		found = append(found, product)
	}
	return found, nil
}

func sampleProduct(code, description string, balance int) stockclient.Product {
	return stockclient.Product{ID: uuid.New(), Code: code, Description: description, Balance: balance}
}

func newTestAPI(t *testing.T, lookup *stubLookup) (*memoryInvoices, http.Handler) {
	t.Helper()

	signer = authntest.New(t)
	invoices := newMemoryInvoices()
	mux := http.NewServeMux()
	billing.NewAPI(billing.NewService(invoices, lookup, invoices)).Routes(mux, signer.Verifier)
	return invoices, mux
}

func doRequest(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	signer.Authenticate(t, request)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v (%s)", err, recorder.Body.String())
	}
	return body
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v (%s)", err, recorder.Body.String())
	}
	return body.Error.Code
}

func TestCreateInvoiceEndpoint(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	hammer := sampleProduct("P-2", "Hammer", 3)
	_, handler := newTestAPI(t, newStubLookup(bolt, hammer))

	recorder := doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2},
		           {"product_id":"`+hammer.ID.String()+`","quantity":1}]}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusCreated, recorder.Body.String())
	}

	body := decodeBody(t, recorder)
	if body["status"] != string(billing.StatusOpen) {
		t.Errorf("status = %v, want %s", body["status"], billing.StatusOpen)
	}
	if body["number"] != float64(1) {
		t.Errorf("number = %v, want 1", body["number"])
	}
	if body["printed_at"] != nil {
		t.Errorf("printed_at = %v, want null", body["printed_at"])
	}

	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v, want 2 entries", body["items"])
	}
	first := items[0].(map[string]any)
	if first["product_code"] != "P-1" || first["quantity"] != float64(2) {
		t.Errorf("first item = %v, want P-1 with quantity 2", first)
	}
	if first["product_description"] != "Steel bolt" {
		t.Errorf("description = %v, want the snapshot from stock", first["product_description"])
	}
	if location := recorder.Header().Get("Location"); !strings.HasPrefix(location, "/invoices/") {
		t.Errorf("Location = %q, want the created resource path", location)
	}
}

func TestCreateInvoiceEndpointMergesRepeatedProducts(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	_, handler := newTestAPI(t, newStubLookup(bolt))

	recorder := doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2},
		           {"product_id":"`+bolt.ID.String()+`","quantity":3}]}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusCreated, recorder.Body.String())
	}

	items := decodeBody(t, recorder)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 merged item", len(items))
	}
	if items[0].(map[string]any)["quantity"] != float64(5) {
		t.Errorf("quantity = %v, want 5", items[0].(map[string]any)["quantity"])
	}
}

func TestCreateInvoiceEndpointValidatesInput(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "no items",
			body:       `{"items":[]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_invoice",
		},
		{
			name:       "zero quantity",
			body:       `{"items":[{"product_id":"` + uuid.New().String() + `","quantity":0}]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_invoice",
		},
		{
			name:       "malformed json",
			body:       `{"items":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "unknown field",
			body:       `{"items":[],"total":10}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "invalid product id",
			body:       `{"items":[{"product_id":"not-a-uuid","quantity":1}]}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, handler := newTestAPI(t, newStubLookup())

			recorder := doRequest(t, handler, http.MethodPost, "/invoices", tc.body)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if got := errorCode(t, recorder); got != tc.wantCode {
				t.Errorf("error code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

func TestCreateInvoiceEndpointReportsUnknownProduct(t *testing.T) {
	_, handler := newTestAPI(t, newStubLookup())

	recorder := doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+uuid.New().String()+`","quantity":1}]}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got := errorCode(t, recorder); got != "invalid_invoice" {
		t.Errorf("error code = %q, want %q", got, "invalid_invoice")
	}
}

func TestCreateInvoiceEndpointReportsStockUnavailable(t *testing.T) {
	lookup := newStubLookup()
	lookup.failWith = stockclient.ErrStockUnavailable
	invoices, handler := newTestAPI(t, lookup)

	recorder := doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+uuid.New().String()+`","quantity":1}]}`)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if got := errorCode(t, recorder); got != "stock_unavailable" {
		t.Errorf("error code = %q, want %q", got, "stock_unavailable")
	}
	if len(invoices.invoices) != 0 {
		t.Errorf("invoices = %d, want 0 (nothing is stored when stock is unreachable)", len(invoices.invoices))
	}
}

func TestCreateInvoiceEndpointDoesNotCallStockForInvalidInput(t *testing.T) {
	lookup := newStubLookup()
	_, handler := newTestAPI(t, lookup)

	doRequest(t, handler, http.MethodPost, "/invoices", `{"items":[]}`)

	if lookup.calls != 0 {
		t.Errorf("stock was called %d times, want 0", lookup.calls)
	}
}

func TestGetInvoiceEndpoint(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	_, handler := newTestAPI(t, newStubLookup(bolt))
	created := decodeBody(t, doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2}]}`))

	recorder := doRequest(t, handler, http.MethodGet, "/invoices/"+created["id"].(string), "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := decodeBody(t, recorder); body["id"] != created["id"] {
		t.Errorf("id = %v, want %v", body["id"], created["id"])
	}
}

func TestGetInvoiceEndpointReportsMissingAndInvalidIDs(t *testing.T) {
	_, handler := newTestAPI(t, newStubLookup())

	missing := doRequest(t, handler, http.MethodGet, "/invoices/"+uuid.New().String(), "")
	if missing.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", missing.Code, http.StatusNotFound)
	}
	if got := errorCode(t, missing); got != "invoice_not_found" {
		t.Errorf("error code = %q, want %q", got, "invoice_not_found")
	}

	invalid := doRequest(t, handler, http.MethodGet, "/invoices/not-a-uuid", "")
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", invalid.Code, http.StatusBadRequest)
	}
	if got := errorCode(t, invalid); got != "invalid_invoice_id" {
		t.Errorf("error code = %q, want %q", got, "invalid_invoice_id")
	}
}

func TestListInvoicesEndpoint(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	_, handler := newTestAPI(t, newStubLookup(bolt))
	doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":1}]}`)

	recorder := doRequest(t, handler, http.MethodGet, "/invoices", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if len(body.Items) != 1 {
		t.Errorf("items = %d, want 1", len(body.Items))
	}

	open := doRequest(t, handler, http.MethodGet, "/invoices?status=open", "")
	if err := json.Unmarshal(open.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if len(body.Items) != 1 {
		t.Errorf("open invoices = %d, want 1 (the filter is case insensitive)", len(body.Items))
	}

	closed := doRequest(t, handler, http.MethodGet, "/invoices?status=CLOSED", "")
	if err := json.Unmarshal(closed.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if len(body.Items) != 0 {
		t.Errorf("closed invoices = %d, want 0", len(body.Items))
	}
}

func TestListInvoicesEndpointRejectsUnknownStatus(t *testing.T) {
	_, handler := newTestAPI(t, newStubLookup())

	recorder := doRequest(t, handler, http.MethodGet, "/invoices?status=PAID", "")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got := errorCode(t, recorder); got != "invalid_invoice" {
		t.Errorf("error code = %q, want %q", got, "invalid_invoice")
	}
}

func TestEndpointsHideRepositoryFailures(t *testing.T) {
	invoices, handler := newTestAPI(t, newStubLookup())
	invoices.failWith = errors.New("connection refused to 10.0.0.9:5432")

	recorder := doRequest(t, handler, http.MethodGet, "/invoices", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "10.0.0.9") {
		t.Errorf("body = %q, want internal details hidden", recorder.Body.String())
	}
}

func TestPrintInvoiceEndpoint(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	created := decodeBody(t, doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2}]}`))

	recorder := doRequest(t, handler, http.MethodPost, "/invoices/"+created["id"].(string)+"/print", "")

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}

	body := decodeBody(t, recorder)
	if body["status"] != string(billing.StatusPrinting) {
		t.Errorf("status = %v, want %s", body["status"], billing.StatusPrinting)
	}
	if len(invoices.printRequests) != 1 {
		t.Errorf("print requests = %d, want 1", len(invoices.printRequests))
	}
}

func TestPrintInvoiceEndpointRejectsInvoicesThatAreNotOpen(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	created := decodeBody(t, doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2}]}`))
	target := "/invoices/" + created["id"].(string) + "/print"

	doRequest(t, handler, http.MethodPost, target, "")
	recorder := doRequest(t, handler, http.MethodPost, target, "")

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if got := errorCode(t, recorder); got != "invoice_not_printable" {
		t.Errorf("error code = %q, want %q", got, "invoice_not_printable")
	}
	if len(invoices.printRequests) != 1 {
		t.Errorf("print requests = %d, want 1 (the second attempt must not ask for another debit)", len(invoices.printRequests))
	}
}

func TestPrintInvoiceEndpointReportsMissingInvoice(t *testing.T) {
	_, handler := newTestAPI(t, newStubLookup())

	recorder := doRequest(t, handler, http.MethodPost, "/invoices/"+uuid.New().String()+"/print", "")

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestInvoiceResponseCarriesTheFailureReason(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	created := decodeBody(t, doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2}]}`))
	id := uuid.MustParse(created["id"].(string))

	// The invoice went through a failed print attempt.
	invoice := invoices.invoices[id]
	if err := invoice.StartPrinting(time.Now().UTC()); err != nil {
		t.Fatalf("StartPrinting() returned error: %v", err)
	}
	if err := invoice.Reopen("insufficient_balance", "Product balance is not enough."); err != nil {
		t.Fatalf("Reopen() returned error: %v", err)
	}
	invoices.invoices[id] = invoice

	body := decodeBody(t, doRequest(t, handler, http.MethodGet, "/invoices/"+id.String(), ""))

	failure, ok := body["failure"].(map[string]any)
	if !ok {
		t.Fatalf("failure = %v, want the reason of the failed attempt", body["failure"])
	}
	if failure["code"] != "insufficient_balance" {
		t.Errorf("failure code = %v, want insufficient_balance", failure["code"])
	}
	if failure["message"] == "" {
		t.Error("failure message is empty, want a message for the operator")
	}
}

func TestInvoiceResponseHasNoFailureWhenNothingFailed(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	_, handler := newTestAPI(t, newStubLookup(bolt))

	body := decodeBody(t, doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2}]}`))

	if body["failure"] != nil {
		t.Errorf("failure = %v, want null", body["failure"])
	}
}

func TestReconcilerReopensStuckInvoices(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	created := decodeBody(t, doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2}]}`))
	id := uuid.MustParse(created["id"].(string))
	doRequest(t, handler, http.MethodPost, "/invoices/"+id.String()+"/print", "")

	// No answer ever arrives from the stock service.
	reconciler := billing.NewReconciler(invoices, discardLogger()).
		WithTimings(time.Nanosecond, time.Hour)
	reconciler.RunOnce(context.Background())

	invoice := invoices.invoices[id]
	if invoice.Status != billing.StatusOpen {
		t.Errorf("status = %s, want %s", invoice.Status, billing.StatusOpen)
	}
	if invoice.FailureCode == "" {
		t.Error("the reopened invoice carries no reason, want the timeout explained")
	}
}

func TestReconcilerLeavesFreshPrintingsAlone(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	created := decodeBody(t, doRequest(t, handler, http.MethodPost, "/invoices",
		`{"items":[{"product_id":"`+bolt.ID.String()+`","quantity":2}]}`))
	id := uuid.MustParse(created["id"].(string))
	doRequest(t, handler, http.MethodPost, "/invoices/"+id.String()+"/print", "")

	billing.NewReconciler(invoices, discardLogger()).
		WithTimings(time.Hour, time.Hour).
		RunOnce(context.Background())

	if invoices.invoices[id].Status != billing.StatusPrinting {
		t.Errorf("status = %s, want %s (the attempt is still within the timeout)",
			invoices.invoices[id].Status, billing.StatusPrinting)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestInvoiceEndpointsRequireASignedInUser(t *testing.T) {
	_, handler := newTestAPI(t, newStubLookup())

	tests := []struct {
		method string
		target string
	}{
		{http.MethodGet, "/invoices"},
		{http.MethodPost, "/invoices"},
		{http.MethodGet, "/invoices/" + uuid.New().String()},
		{http.MethodPost, "/invoices/" + uuid.New().String() + "/print"},
	}

	for _, tc := range tests {
		request := httptest.NewRequest(tc.method, tc.target, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token = %d, want %d", tc.method, tc.target, recorder.Code, http.StatusUnauthorized)
		}
	}
}
