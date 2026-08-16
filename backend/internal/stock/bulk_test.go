package stock_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/bulk"
	"github.com/thiagodias/korp-invoices/internal/stock"
)

func decodeBulk(t *testing.T, recorder *httptest.ResponseRecorder) bulk.Response {
	t.Helper()

	var response bulk.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("response body is not valid JSON: %v (%s)", err, recorder.Body.String())
	}
	return response
}

func TestBulkCreateRegistersEveryProduct(t *testing.T) {
	_, handler := newTestAPI(t)

	recorder := doRequest(t, handler, http.MethodPost, "/products/bulk", `{"items":[
		{"code":"P-1","description":"Steel bolt","balance":10},
		{"code":"P-2","description":"Hammer","balance":3}
	]}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	response := decodeBulk(t, recorder)
	if response.Summary.Requested != 2 || response.Summary.Succeeded != 2 || response.Summary.Failed != 0 {
		t.Errorf("summary = %+v, want two out of two", response.Summary)
	}
	if response.Atomic {
		t.Error("the answer claims the items stand or fall together, want them independent")
	}
	for index, result := range response.Results {
		if result.Index != index || result.Status != bulk.Succeeded || result.ID == "" {
			t.Errorf("result %d = %+v, want it succeeded with an id", index, result)
		}
	}
}

// A bad row must not stop the good ones: importing a catalogue with one
// mistake should bring the rest in and say which line to fix.
func TestBulkCreateAppliesTheGoodItemsAndReportsTheRest(t *testing.T) {
	repository, handler := newTestAPI(t)

	recorder := doRequest(t, handler, http.MethodPost, "/products/bulk", `{"items":[
		{"code":"P-1","description":"Steel bolt","balance":10},
		{"code":"","description":"Nameless","balance":1},
		{"code":"P-3","description":"Wrench","balance":-5},
		{"code":"P-4","description":"Saw","balance":2}
	]}`)

	if recorder.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d for a mixed outcome", recorder.Code, http.StatusMultiStatus)
	}

	response := decodeBulk(t, recorder)
	if response.Summary.Succeeded != 2 || response.Summary.Failed != 2 {
		t.Fatalf("summary = %+v, want two applied and two refused", response.Summary)
	}
	if response.Results[1].Error == nil || response.Results[1].Error.Code != "invalid_product" {
		t.Errorf("result 1 = %+v, want the validation error", response.Results[1])
	}
	if response.Results[3].Status != bulk.Succeeded {
		t.Errorf("result 3 = %+v, want the good row after a bad one to be applied", response.Results[3])
	}
	if len(repository.products) != 2 {
		t.Errorf("%d products were registered, want 2", len(repository.products))
	}
}

func TestBulkCreateReportsTheSameCodeTwiceInOneRequest(t *testing.T) {
	repository, handler := newTestAPI(t)

	recorder := doRequest(t, handler, http.MethodPost, "/products/bulk", `{"items":[
		{"code":"P-1","description":"Steel bolt","balance":10},
		{"code":"p-1","description":"The same one again","balance":5}
	]}`)

	response := decodeBulk(t, recorder)
	if response.Summary.Succeeded != 1 || response.Summary.Failed != 1 {
		t.Fatalf("summary = %+v, want one applied and the repeat refused", response.Summary)
	}
	if response.Results[1].Error.Code != "duplicated_item" {
		t.Errorf("result 1 = %+v, want it called out as a repeat", response.Results[1])
	}
	if !strings.Contains(response.Results[1].Error.Details["code"], "position 0") {
		t.Errorf("details = %v, want the earlier position named", response.Results[1].Error.Details)
	}
	if len(repository.products) != 1 {
		t.Errorf("%d products were registered, want 1", len(repository.products))
	}
}

func TestBulkCreateRefusesAnEmptyOrOversizedRequest(t *testing.T) {
	_, handler := newTestAPI(t)

	empty := doRequest(t, handler, http.MethodPost, "/products/bulk", `{"items":[]}`)
	if empty.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d for an empty request", empty.Code, http.StatusBadRequest)
	}
	if got := errorCode(t, empty); got != "no_items" {
		t.Errorf("error code = %q, want no_items", got)
	}

	items := make([]string, 0, bulk.MaxItems+1)
	for i := range bulk.MaxItems + 1 {
		items = append(items, fmt.Sprintf(`{"code":"P-%d","description":"Product","balance":1}`, i))
	}
	oversized := doRequest(t, handler, http.MethodPost, "/products/bulk",
		`{"items":[`+strings.Join(items, ",")+`]}`)

	if oversized.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d over the limit", oversized.Code, http.StatusBadRequest)
	}
	if got := errorCode(t, oversized); got != "too_many_items" {
		t.Errorf("error code = %q, want too_many_items", got)
	}
}

func TestBulkCreateAcceptsTheLargestAllowedRequest(t *testing.T) {
	repository, handler := newTestAPI(t)

	items := make([]string, 0, bulk.MaxItems)
	for i := range bulk.MaxItems {
		items = append(items, fmt.Sprintf(`{"code":"P-%03d","description":"Product %d","balance":%d}`, i, i, i))
	}

	recorder := doRequest(t, handler, http.MethodPost, "/products/bulk",
		`{"items":[`+strings.Join(items, ",")+`]}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if response := decodeBulk(t, recorder); response.Summary.Succeeded != bulk.MaxItems {
		t.Errorf("summary = %+v, want every item applied", response.Summary)
	}
	if len(repository.products) != bulk.MaxItems {
		t.Errorf("%d products were registered, want %d", len(repository.products), bulk.MaxItems)
	}
}

// The answer has to stay readable at the largest size: it carries an index, an
// identity and a reason, never whole products.
func TestBulkAnswerStaysSmall(t *testing.T) {
	_, handler := newTestAPI(t)

	items := make([]string, 0, bulk.MaxItems)
	for i := range bulk.MaxItems {
		items = append(items, fmt.Sprintf(`{"code":"P-%03d","description":"A rather long product description %d","balance":%d}`, i, i, i))
	}
	recorder := doRequest(t, handler, http.MethodPost, "/products/bulk",
		`{"items":[`+strings.Join(items, ",")+`]}`)

	if size := recorder.Body.Len(); size > 20_000 {
		t.Errorf("the answer to %d items is %d bytes, want it compact", bulk.MaxItems, size)
	}
	if strings.Contains(recorder.Body.String(), "A rather long product description") {
		t.Error("the answer echoes the products back, want only what identifies them")
	}
}

func TestBulkEndpointsRequireASignedInUser(t *testing.T) {
	_, handler := newTestAPI(t)

	for _, path := range []string{"/products/bulk", "/products/adjustments"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"items":[]}`))
		request.Header.Set("Content-Type", "application/json")

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s without a token = %d, want %d", path, recorder.Code, http.StatusUnauthorized)
		}
	}
}

func TestBulkAdjustmentsApplyTogether(t *testing.T) {
	repository, handler := newTestAPI(t)
	first := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-1","description":"Steel bolt","balance":10}`))
	second := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-2","description":"Hammer","balance":5}`))

	recorder := doRequest(t, handler, http.MethodPost, "/products/adjustments", `{"items":[
		{"product_id":"`+first["id"].(string)+`","delta":25},
		{"product_id":"`+second["id"].(string)+`","delta":-2}
	]}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	response := decodeBulk(t, recorder)
	if !response.Atomic {
		t.Error("the answer does not say the items stand or fall together")
	}
	if response.Summary.Succeeded != 2 {
		t.Fatalf("summary = %+v, want both movements applied", response.Summary)
	}

	balances := map[string]int{}
	for _, product := range repository.products {
		balances[product.Code] = product.Balance
	}
	if balances["P-1"] != 35 || balances["P-2"] != 3 {
		t.Errorf("balances = %v, want P-1 at 35 and P-2 at 3", balances)
	}
}

// A delivery note that cannot be applied in full must leave nothing behind, or
// sending it again would count the applied half twice.
func TestBulkAdjustmentsLeaveNothingBehindWhenOneItemFails(t *testing.T) {
	repository, handler := newTestAPI(t)
	product := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-1","description":"Steel bolt","balance":10}`))

	recorder := doRequest(t, handler, http.MethodPost, "/products/adjustments", `{"items":[
		{"product_id":"`+product["id"].(string)+`","delta":5},
		{"product_id":"`+uuid.New().String()+`","delta":3}
	]}`)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d when nothing was applied", recorder.Code, http.StatusConflict)
	}

	response := decodeBulk(t, recorder)
	if response.Summary.Succeeded != 1 || response.Summary.Failed != 1 {
		t.Errorf("summary = %+v, want the answer to show which item stopped it", response.Summary)
	}

	for _, stored := range repository.products {
		if stored.Balance != 10 {
			t.Errorf("balance = %d, want the original 10: nothing should have been applied", stored.Balance)
		}
	}
}

func TestBulkAdjustmentsValidateEachItem(t *testing.T) {
	_, handler := newTestAPI(t)
	product := decodeProduct(t, doRequest(t, handler, http.MethodPost, "/products",
		`{"code":"P-1","description":"Steel bolt","balance":10}`))
	id := product["id"].(string)

	tests := map[string]string{
		"no product": `{"items":[{"delta":5}]}`,
		"zero delta": `{"items":[{"product_id":"` + id + `","delta":0}]}`,
		"huge delta": `{"items":[{"product_id":"` + id + `","delta":9999999999}]}`,
		"same twice": `{"items":[{"product_id":"` + id + `","delta":1},{"product_id":"` + id + `","delta":2}]}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := doRequest(t, handler, http.MethodPost, "/products/adjustments", body)

			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
			}
			response := decodeBulk(t, recorder)
			if response.Summary.Failed == 0 {
				t.Errorf("summary = %+v, want the offending item reported", response.Summary)
			}
		})
	}
}

func TestBulkPrintStartsEveryOpenInvoice(t *testing.T) {
	// Covered end to end by the billing tests; this only guards the shape of
	// the request the stock service does not serve.
	_, handler := newTestAPI(t)

	recorder := doRequest(t, handler, http.MethodPost, "/products/bulk", `{"items":[{"code":"P-1","description":"x","balance":1}]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

// Two identical imports arriving at once must not both create the products.
func TestConcurrentBulkCreatesDoNotDuplicate(t *testing.T) {
	repository, handler := newTestAPI(t)

	body := `{"items":[{"code":"P-1","description":"Steel bolt","balance":10}]}`

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			doRequest(t, handler, http.MethodPost, "/products/bulk", body)
		}()
	}
	wg.Wait()

	if len(repository.products) != 1 {
		t.Errorf("%d products were registered, want the repeat refused", len(repository.products))
	}
}

func TestServiceRefusesOversizedBatches(t *testing.T) {
	repository := newMemoryRepository()
	service := stock.NewService(repository)

	inputs := make([]stock.ProductInput, bulk.MaxItems+1)
	if _, err := service.CreateProducts(context.Background(), inputs); !errors.Is(err, bulk.ErrTooManyItems) {
		t.Errorf("CreateProducts() error = %v, want ErrTooManyItems", err)
	}
	if _, err := service.AdjustBalances(context.Background(), make([]stock.Adjustment, bulk.MaxItems+1)); !errors.Is(err, bulk.ErrTooManyItems) {
		t.Errorf("AdjustBalances() error = %v, want ErrTooManyItems", err)
	}
	if _, err := service.CreateProducts(context.Background(), nil); !errors.Is(err, bulk.ErrNoItems) {
		t.Errorf("CreateProducts() error = %v, want ErrNoItems", err)
	}
}

func TestAdjustmentsAreAppliedAtomicallyInTheDatabase(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	service := stock.NewService(store)

	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)
	hammer := createProduct(t, ctx, store, "P-2", "Hammer", 5)

	// A movement that would take a balance below zero stops the whole batch.
	results, err := service.AdjustBalances(ctx, []stock.Adjustment{
		{ProductID: bolt.ID, Delta: 100},
		{ProductID: hammer.ID, Delta: -50},
	})
	if !errors.Is(err, stock.ErrAdjustmentRejected) {
		t.Fatalf("AdjustBalances() error = %v, want ErrAdjustmentRejected", err)
	}
	if results[1].Error == nil || results[1].Error.Code != "insufficient_balance" {
		t.Errorf("result 1 = %+v, want the balance explained", results[1])
	}

	if got := balanceOf(t, ctx, pool, bolt.ID); got != 10 {
		t.Errorf("balance = %d, want the original 10: nothing should have been applied", got)
	}
}

func TestAdjustmentsWorkFromTheProductCode(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	service := stock.NewService(store)
	bolt := createProduct(t, ctx, store, "BOLT-1", "Steel bolt", 10)

	// A delivery note carries codes, not identifiers, and casing varies.
	if _, err := service.AdjustBalances(ctx, []stock.Adjustment{
		{ProductCode: "bolt-1", Delta: 15, Reason: "delivery 4711"},
	}); err != nil {
		t.Fatalf("AdjustBalances() returned error: %v", err)
	}

	if got := balanceOf(t, ctx, pool, bolt.ID); got != 25 {
		t.Errorf("balance = %d, want 25", got)
	}
}

func TestAdjustmentsReportAnUnknownProductWithoutApplyingAnything(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	service := stock.NewService(store)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	results, err := service.AdjustBalances(ctx, []stock.Adjustment{
		{ProductID: bolt.ID, Delta: 5},
		{ProductCode: "GHOST-1", Delta: 5},
	})
	if !errors.Is(err, stock.ErrAdjustmentRejected) {
		t.Fatalf("AdjustBalances() error = %v, want ErrAdjustmentRejected", err)
	}
	if results[1].Error.Code != "product_not_found" {
		t.Errorf("result 1 = %+v, want the unknown product named", results[1])
	}
	if got := balanceOf(t, ctx, pool, bolt.ID); got != 10 {
		t.Errorf("balance = %d, want it untouched", got)
	}
}

// An adjustment and a print touching the same products at the same time must
// not deadlock, and neither may lose the other's work.
func TestAdjustmentsAndPrintingDoNotFightOverTheSameProducts(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	service := stock.NewService(store)

	first := createProduct(t, ctx, store, "P-1", "Steel bolt", 100)
	second := createProduct(t, ctx, store, "P-2", "Hammer", 100)

	const rounds = 15
	var wg sync.WaitGroup
	failures := make(chan error, rounds*2)

	wg.Add(rounds * 2)
	for range rounds {
		// A delivery arriving, in one order.
		go func() {
			defer wg.Done()
			_, err := service.AdjustBalances(ctx, []stock.Adjustment{
				{ProductID: first.ID, Delta: 2},
				{ProductID: second.ID, Delta: 2},
			})
			failures <- err
		}()
		// An invoice being printed, in the other.
		go func() {
			defer wg.Done()
			failures <- debit(ctx, pool, uuid.New(), []stock.DebitItem{
				{ProductID: second.ID, Quantity: 1},
				{ProductID: first.ID, Quantity: 1},
			})
		}()
	}
	wg.Wait()
	close(failures)

	for err := range failures {
		if err != nil {
			t.Fatalf("an operation failed: %v", err)
		}
	}

	// Every movement landed exactly once: +2 fifteen times, -1 fifteen times.
	want := 100 + rounds*2 - rounds
	if got := balanceOf(t, ctx, pool, first.ID); got != want {
		t.Errorf("balance = %d, want %d", got, want)
	}
	if got := balanceOf(t, ctx, pool, second.ID); got != want {
		t.Errorf("balance = %d, want %d", got, want)
	}
}

// Sending the same delivery note twice with the same idempotency key must not
// count it twice; the key is what the HTTP layer replays on.
func TestRepeatedAdjustmentsCountTwiceWithoutAnIdempotencyKey(t *testing.T) {
	ctx, store, pool := newTestStore(t)
	service := stock.NewService(store)
	bolt := createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	for range 2 {
		if _, err := service.AdjustBalances(ctx, []stock.Adjustment{{ProductID: bolt.ID, Delta: 5}}); err != nil {
			t.Fatalf("AdjustBalances() returned error: %v", err)
		}
	}

	// This is the honest behaviour of a movement: applying it twice adds twice.
	// Not repeating it is the job of the Idempotency-Key on the endpoint.
	if got := balanceOf(t, ctx, pool, bolt.ID); got != 20 {
		t.Errorf("balance = %d, want 20: a movement applied twice adds twice", got)
	}
}

func TestBulkCreateAgainstTheDatabaseReportsDuplicates(t *testing.T) {
	ctx, store, _ := newTestStore(t)
	service := stock.NewService(store)
	createProduct(t, ctx, store, "P-1", "Steel bolt", 10)

	results, err := service.CreateProducts(ctx, []stock.ProductInput{
		{Code: "P-2", Description: "Hammer", Balance: 1},
		{Code: "p-1", Description: "Already registered", Balance: 1},
	})
	if err != nil {
		t.Fatalf("CreateProducts() returned error: %v", err)
	}

	if results[0].Status != bulk.Succeeded {
		t.Errorf("result 0 = %+v, want it applied", results[0])
	}
	if results[1].Error == nil || results[1].Error.Code != "duplicated_product_code" {
		t.Errorf("result 1 = %+v, want the existing code reported", results[1])
	}
}
