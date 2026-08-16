package stock_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"slices"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/authn/authntest"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
	"github.com/thiagodias/korp-invoices/internal/stock"
)

// signer issues the access tokens the endpoints require.
var signer *authntest.Signer

// memoryRepository is an in-memory ProductRepository for handler tests.
type memoryRepository struct {
	products map[uuid.UUID]stock.Product
	failWith error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{products: map[uuid.UUID]stock.Product{}}
}

func (r *memoryRepository) Create(ctx context.Context, product stock.Product) (stock.Product, error) {
	if r.failWith != nil {
		return stock.Product{}, r.failWith
	}
	for _, existing := range r.products {
		if strings.EqualFold(existing.Code, product.Code) {
			return stock.Product{}, stock.ErrDuplicatedCode
		}
	}
	product.ID = uuid.New()
	product.CreatedAt = time.Now().UTC()
	product.UpdatedAt = product.CreatedAt
	r.products[product.ID] = product
	return product, nil
}

func (r *memoryRepository) Update(ctx context.Context, product stock.Product) (stock.Product, error) {
	if r.failWith != nil {
		return stock.Product{}, r.failWith
	}
	if _, ok := r.products[product.ID]; !ok {
		return stock.Product{}, stock.ErrProductNotFound
	}
	product.UpdatedAt = time.Now().UTC()
	r.products[product.ID] = product
	return product, nil
}

func (r *memoryRepository) GetByID(ctx context.Context, id uuid.UUID) (stock.Product, error) {
	if r.failWith != nil {
		return stock.Product{}, r.failWith
	}
	product, ok := r.products[id]
	if !ok {
		return stock.Product{}, stock.ErrProductNotFound
	}
	return product, nil
}

func (r *memoryRepository) GetByCode(ctx context.Context, code string) (stock.Product, error) {
	if r.failWith != nil {
		return stock.Product{}, r.failWith
	}
	for _, product := range r.products {
		if strings.EqualFold(product.Code, code) {
			return product, nil
		}
	}
	return stock.Product{}, stock.ErrProductNotFound
}

func (r *memoryRepository) List(ctx context.Context, query stock.Query) (stock.Page, error) {
	if r.failWith != nil {
		return stock.Page{}, r.failWith
	}

	products := make([]stock.Product, 0, len(r.products))
	for _, product := range r.products {
		matches := query.Search == "" ||
			strings.Contains(strings.ToLower(product.Code), strings.ToLower(query.Search)) ||
			strings.Contains(strings.ToLower(product.Description), strings.ToLower(query.Search))
		if matches {
			products = append(products, product)
		}
	}
	slices.SortFunc(products, func(a, b stock.Product) int {
		return strings.Compare(strings.ToUpper(a.Code), strings.ToUpper(b.Code))
	})

	// The fake pages the same way the store does: cut by the last code seen.
	cursor, err := pagination.Decode(query.Cursor)
	if err != nil {
		return stock.Page{}, err
	}
	if cursor.Key != "" {
		products = slices.DeleteFunc(products, func(product stock.Product) bool {
			return strings.ToUpper(product.Code) <= cursor.Key
		})
	}

	limit := pagination.NormalizeLimit(query.Limit)
	page := stock.Page{Items: products}
	if len(products) > limit {
		page.Items = products[:limit]
		page.NextCursor = pagination.Encode(pagination.Cursor{
			Key: strings.ToUpper(page.Items[len(page.Items)-1].Code),
		})
	}
	return page, nil
}

func (r *memoryRepository) FindByIDs(ctx context.Context, ids []uuid.UUID) ([]stock.Product, error) {
	if r.failWith != nil {
		return nil, r.failWith
	}
	products := make([]stock.Product, 0, len(ids))
	for _, id := range ids {
		if product, ok := r.products[id]; ok {
			products = append(products, product)
		}
	}
	return products, nil
}

func newTestAPI(t *testing.T) (*memoryRepository, http.Handler) {
	t.Helper()

	signer = authntest.New(t)
	repository := newMemoryRepository()
	mux := http.NewServeMux()
	stock.NewAPI(stock.NewService(repository), &stock.FailureSwitch{}).Routes(mux, signer.Verifier)
	return repository, mux
}

func doRequest(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	signer.Authenticate(t, request)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeProduct(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
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
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v (%s)", err, recorder.Body.String())
	}
	return body.Error.Code
}

func TestCreateProductEndpoint(t *testing.T) {
	_, handler := newTestAPI(t)

	recorder := doRequest(t, handler, http.MethodPost, "/products",
		`{"code":" P-1 ","description":"  Steel  bolt ","balance":10}`)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusCreated, recorder.Body.String())
	}

	body := decodeProduct(t, recorder)
	if body["code"] != "P-1" {
		t.Errorf("code = %v, want P-1", body["code"])
	}
	if body["description"] != "Steel bolt" {
		t.Errorf("description = %v, want %q", body["description"], "Steel bolt")
	}
	if body["balance"] != float64(10) {
		t.Errorf("balance = %v, want 10", body["balance"])
	}
	if location := recorder.Header().Get("Location"); !strings.HasPrefix(location, "/products/") {
		t.Errorf("Location = %q, want the created resource path", location)
	}
}

func TestCreateProductEndpointValidatesInput(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing fields",
			body:       `{"code":"","description":"","balance":-1}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_product",
		},
		{
			name:       "malformed json",
			body:       `{"code":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
		{
			name:       "unknown field",
			body:       `{"code":"P-1","description":"Bolt","balance":1,"price":10}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, handler := newTestAPI(t)

			recorder := doRequest(t, handler, http.MethodPost, "/products", tc.body)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			if got := errorCode(t, recorder); got != tc.wantCode {
				t.Errorf("error code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

func TestCreateProductEndpointRejectsDuplicatedCode(t *testing.T) {
	_, handler := newTestAPI(t)
	doRequest(t, handler, http.MethodPost, "/products", `{"code":"P-1","description":"Bolt","balance":1}`)

	recorder := doRequest(t, handler, http.MethodPost, "/products", `{"code":"p-1","description":"Other","balance":2}`)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if got := errorCode(t, recorder); got != "duplicated_product_code" {
		t.Errorf("error code = %q, want %q", got, "duplicated_product_code")
	}
}

func TestListProductsEndpoint(t *testing.T) {
	_, handler := newTestAPI(t)
	doRequest(t, handler, http.MethodPost, "/products", `{"code":"P-1","description":"Steel bolt","balance":10}`)
	doRequest(t, handler, http.MethodPost, "/products", `{"code":"P-2","description":"Hammer","balance":3}`)

	recorder := doRequest(t, handler, http.MethodGet, "/products", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if len(body.Items) != 2 {
		t.Errorf("items = %d, want 2", len(body.Items))
	}

	filtered := doRequest(t, handler, http.MethodGet, "/products?search=hammer", "")
	if err := json.Unmarshal(filtered.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0]["code"] != "P-2" {
		t.Errorf("filtered items = %v, want only P-2", body.Items)
	}
}

func TestListProductsEndpointRejectsLongSearch(t *testing.T) {
	_, handler := newTestAPI(t)

	recorder := doRequest(t, handler, http.MethodGet, "/products?search="+strings.Repeat("x", 101), "")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got := errorCode(t, recorder); got != "invalid_search" {
		t.Errorf("error code = %q, want %q", got, "invalid_search")
	}
}

func TestListProductsEndpointReturnsEmptyList(t *testing.T) {
	_, handler := newTestAPI(t)

	recorder := doRequest(t, handler, http.MethodGet, "/products", "")

	if !strings.Contains(recorder.Body.String(), `"items":[]`) {
		t.Errorf("body = %s, want an empty items array", recorder.Body.String())
	}
}

func TestGetProductEndpoint(t *testing.T) {
	_, handler := newTestAPI(t)
	created := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-1","description":"Steel bolt","balance":10}`))

	recorder := doRequest(t, handler, http.MethodGet, "/products/"+created["id"].(string), "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := decodeProduct(t, recorder); body["id"] != created["id"] {
		t.Errorf("id = %v, want %v", body["id"], created["id"])
	}
}

func TestGetProductEndpointReportsMissingAndInvalidIDs(t *testing.T) {
	_, handler := newTestAPI(t)

	missing := doRequest(t, handler, http.MethodGet, "/products/"+uuid.New().String(), "")
	if missing.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", missing.Code, http.StatusNotFound)
	}
	if got := errorCode(t, missing); got != "product_not_found" {
		t.Errorf("error code = %q, want %q", got, "product_not_found")
	}

	invalid := doRequest(t, handler, http.MethodGet, "/products/not-a-uuid", "")
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", invalid.Code, http.StatusBadRequest)
	}
	if got := errorCode(t, invalid); got != "invalid_product_id" {
		t.Errorf("error code = %q, want %q", got, "invalid_product_id")
	}
}

func TestUpdateProductEndpoint(t *testing.T) {
	_, handler := newTestAPI(t)
	created := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-1","description":"Steel bolt","balance":10}`))

	recorder := doRequest(t, handler, http.MethodPut, "/products/"+created["id"].(string),
		`{"description":"Stainless bolt","balance":42}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	body := decodeProduct(t, recorder)
	if body["description"] != "Stainless bolt" {
		t.Errorf("description = %v, want %q", body["description"], "Stainless bolt")
	}
	if body["balance"] != float64(42) {
		t.Errorf("balance = %v, want 42", body["balance"])
	}
	if body["code"] != "P-1" {
		t.Errorf("code = %v, want it unchanged", body["code"])
	}
}

func TestUpdateProductEndpointValidatesBody(t *testing.T) {
	_, handler := newTestAPI(t)
	created := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-1","description":"Steel bolt","balance":10}`))

	recorder := doRequest(t, handler, http.MethodPut, "/products/"+created["id"].(string),
		`{"description":"","balance":-3}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got := errorCode(t, recorder); got != "invalid_product" {
		t.Errorf("error code = %q, want %q", got, "invalid_product")
	}
}

func TestEndpointsHideRepositoryFailures(t *testing.T) {
	repository, handler := newTestAPI(t)
	repository.failWith = errors.New("connection refused to 10.0.0.7:5432")

	recorder := doRequest(t, handler, http.MethodGet, "/products", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "10.0.0.7") {
		t.Errorf("body = %q, want internal details hidden", recorder.Body.String())
	}
}

func TestFindProductsReportsMissingProduct(t *testing.T) {
	repository := newMemoryRepository()
	service := stock.NewService(repository)
	ctx := context.Background()

	created, err := service.CreateProduct(ctx, "P-1", "Steel bolt", 10)
	if err != nil {
		t.Fatalf("CreateProduct() returned error: %v", err)
	}

	found, err := service.FindProducts(ctx, []uuid.UUID{created.ID})
	if err != nil {
		t.Fatalf("FindProducts() returned error: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("FindProducts() returned %d products, want 1", len(found))
	}

	_, err = service.FindProducts(ctx, []uuid.UUID{created.ID, uuid.New()})
	if !errors.Is(err, stock.ErrProductNotFound) {
		t.Errorf("FindProducts() error = %v, want ErrProductNotFound", err)
	}
}

func newTestAPIWithInternalRoutes(t *testing.T, token string) (*memoryRepository, http.Handler) {
	t.Helper()

	signer = authntest.New(t)
	repository := newMemoryRepository()
	mux := http.NewServeMux()
	api := stock.NewAPI(stock.NewService(repository), &stock.FailureSwitch{})
	api.Routes(mux, signer.Verifier)
	api.InternalRoutes(mux, token)
	return repository, mux
}

func TestLookupProductsRequiresServiceToken(t *testing.T) {
	_, handler := newTestAPIWithInternalRoutes(t, "s3cret")

	request := httptest.NewRequest(http.MethodPost, "/internal/products/lookup",
		strings.NewReader(`{"product_ids":["`+uuid.New().String()+`"]}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func lookupProducts(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/internal/products/lookup", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(httpx.ServiceTokenHeader, token)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestLookupProductsReturnsRequestedProducts(t *testing.T) {
	_, handler := newTestAPIWithInternalRoutes(t, "s3cret")
	first := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-1","description":"Steel bolt","balance":10}`))
	second := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-2","description":"Hammer","balance":3}`))

	recorder := lookupProducts(t, handler, "s3cret",
		`{"product_ids":["`+first["id"].(string)+`","`+second["id"].(string)+`"]}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if len(body.Items) != 2 {
		t.Errorf("items = %d, want 2", len(body.Items))
	}
}

func TestLookupProductsReportsUnknownProduct(t *testing.T) {
	_, handler := newTestAPIWithInternalRoutes(t, "s3cret")

	recorder := lookupProducts(t, handler, "s3cret", `{"product_ids":["`+uuid.New().String()+`"]}`)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if got := errorCode(t, recorder); got != "product_not_found" {
		t.Errorf("error code = %q, want %q", got, "product_not_found")
	}
}

func TestLookupProductsValidatesRequest(t *testing.T) {
	_, handler := newTestAPIWithInternalRoutes(t, "s3cret")

	empty := lookupProducts(t, handler, "s3cret", `{"product_ids":[]}`)
	if empty.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", empty.Code, http.StatusBadRequest)
	}

	ids := make([]string, 0, 101)
	for range 101 {
		ids = append(ids, `"`+uuid.New().String()+`"`)
	}
	tooMany := lookupProducts(t, handler, "s3cret", `{"product_ids":[`+strings.Join(ids, ",")+`]}`)
	if tooMany.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", tooMany.Code, http.StatusBadRequest)
	}
}

func TestProductEndpointsRequireASignedInUser(t *testing.T) {
	_, handler := newTestAPI(t)

	tests := []struct {
		method string
		target string
	}{
		{http.MethodGet, "/products"},
		{http.MethodPost, "/products"},
		{http.MethodGet, "/products/" + uuid.New().String()},
		{http.MethodPut, "/products/" + uuid.New().String()},
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

func TestProductEndpointsRefuseATokenSignedByAnotherKey(t *testing.T) {
	_, handler := newTestAPI(t)
	stranger := authntest.New(t)

	request := httptest.NewRequest(http.MethodGet, "/products", nil)
	request.Header.Set("Authorization", "Bearer "+stranger.Token(t))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestProductEndpointsRefuseAMalformedAuthorizationHeader(t *testing.T) {
	_, handler := newTestAPI(t)

	for _, header := range []string{"", "Bearer", "Bearer ", "Basic abc", signer.Token(t)} {
		request := httptest.NewRequest(http.MethodGet, "/products", nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("header %q = %d, want %d", header, recorder.Code, http.StatusUnauthorized)
		}
	}
}
