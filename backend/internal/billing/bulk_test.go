package billing_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/billing"
	"github.com/thiagodias/korp-invoices/internal/platform/bulk"
)

func decodeBulk(t *testing.T, recorder *httptest.ResponseRecorder) bulk.Response {
	t.Helper()

	var response bulk.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("response body is not valid JSON: %v (%s)", err, recorder.Body.String())
	}
	return response
}

// createInvoices issues invoices through the API and returns their ids.
func createInvoices(t *testing.T, handler http.Handler, productID string, count int) []string {
	t.Helper()

	ids := make([]string, 0, count)
	for range count {
		created := decodeBody(t, doRequest(t, handler, http.MethodPost, "/invoices",
			`{"items":[{"product_id":"`+productID+`","quantity":1}]}`))
		ids = append(ids, created["id"].(string))
	}
	return ids
}

func TestBulkPrintStartsEveryInvoice(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	ids := createInvoices(t, handler, bolt.ID.String(), 3)

	recorder := doRequest(t, handler, http.MethodPost, "/invoices/print",
		`{"invoice_ids":["`+strings.Join(ids, `","`)+`"]}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	response := decodeBulk(t, recorder)
	if response.Summary.Succeeded != 3 {
		t.Fatalf("summary = %+v, want all three invoices printing", response.Summary)
	}
	if response.Atomic {
		t.Error("the answer says the invoices stand or fall together, want them independent")
	}
	for _, result := range response.Results {
		if result.Reference == "" {
			t.Errorf("result %+v carries no invoice number", result)
		}
	}
	if len(invoices.printRequests) != 3 {
		t.Errorf("%d print requests were made, want 3", len(invoices.printRequests))
	}
}

// An invoice that cannot be printed must not hold back the others: there is
// nothing to roll back, since the accepted ones are already on their way.
func TestBulkPrintKeepsGoingAfterAnInvoiceIsRefused(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	ids := createInvoices(t, handler, bolt.ID.String(), 3)

	// The middle one is already printing.
	doRequest(t, handler, http.MethodPost, "/invoices/"+ids[1]+"/print", "")

	recorder := doRequest(t, handler, http.MethodPost, "/invoices/print",
		`{"invoice_ids":["`+strings.Join(ids, `","`)+`"]}`)

	if recorder.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d for a mixed outcome", recorder.Code, http.StatusMultiStatus)
	}

	response := decodeBulk(t, recorder)
	if response.Summary.Succeeded != 2 || response.Summary.Failed != 1 {
		t.Fatalf("summary = %+v, want two started and one refused", response.Summary)
	}
	if response.Results[1].Error == nil || response.Results[1].Error.Code != "invoice_not_printable" {
		t.Errorf("result 1 = %+v, want the reason it was refused", response.Results[1])
	}
	if response.Results[2].Status != bulk.Succeeded {
		t.Errorf("result 2 = %+v, want the invoice after the refused one to start", response.Results[2])
	}
	if len(invoices.printRequests) != 3 {
		t.Errorf("%d print requests were made, want 3 (one before the batch and two in it)",
			len(invoices.printRequests))
	}
}

func TestBulkPrintReportsTheSameInvoiceTwice(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	ids := createInvoices(t, handler, bolt.ID.String(), 1)

	recorder := doRequest(t, handler, http.MethodPost, "/invoices/print",
		`{"invoice_ids":["`+ids[0]+`","`+ids[0]+`"]}`)

	response := decodeBulk(t, recorder)
	if response.Summary.Succeeded != 1 || response.Summary.Failed != 1 {
		t.Fatalf("summary = %+v, want it printed once and the repeat called out", response.Summary)
	}
	if response.Results[1].Error.Code != "duplicated_item" {
		t.Errorf("result 1 = %+v, want the repeat named as such", response.Results[1])
	}
	if len(invoices.printRequests) != 1 {
		t.Errorf("%d print requests were made, want 1", len(invoices.printRequests))
	}
}

func TestBulkPrintReportsUnknownInvoices(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 10)
	_, handler := newTestAPI(t, newStubLookup(bolt))
	ids := createInvoices(t, handler, bolt.ID.String(), 1)

	recorder := doRequest(t, handler, http.MethodPost, "/invoices/print",
		`{"invoice_ids":["`+ids[0]+`","`+uuid.New().String()+`"]}`)

	response := decodeBulk(t, recorder)
	if response.Results[1].Error == nil || response.Results[1].Error.Code != "invoice_not_found" {
		t.Errorf("result 1 = %+v, want the missing invoice reported", response.Results[1])
	}
}

func TestBulkPrintRefusesAnEmptyOrOversizedRequest(t *testing.T) {
	_, handler := newTestAPI(t, newStubLookup())

	empty := doRequest(t, handler, http.MethodPost, "/invoices/print", `{"invoice_ids":[]}`)
	if empty.Code != http.StatusBadRequest || errorCode(t, empty) != "no_items" {
		t.Errorf("empty request = %d %q, want 400 no_items", empty.Code, errorCode(t, empty))
	}

	ids := make([]string, 0, bulk.MaxItems+1)
	for range bulk.MaxItems + 1 {
		ids = append(ids, uuid.New().String())
	}
	oversized := doRequest(t, handler, http.MethodPost, "/invoices/print",
		`{"invoice_ids":["`+strings.Join(ids, `","`)+`"]}`)

	if oversized.Code != http.StatusBadRequest || errorCode(t, oversized) != "too_many_items" {
		t.Errorf("oversized request = %d %q, want 400 too_many_items", oversized.Code, errorCode(t, oversized))
	}
}

func TestBulkPrintAnswerStaysSmall(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 1000)
	_, handler := newTestAPI(t, newStubLookup(bolt))
	ids := createInvoices(t, handler, bolt.ID.String(), 60)

	recorder := doRequest(t, handler, http.MethodPost, "/invoices/print",
		`{"invoice_ids":["`+strings.Join(ids, `","`)+`"]}`)

	if size := recorder.Body.Len(); size > 20_000 {
		t.Errorf("the answer to %d invoices is %d bytes, want it compact", len(ids), size)
	}
	if strings.Contains(recorder.Body.String(), "product_description") {
		t.Error("the answer carries whole invoices, want only what identifies them")
	}
}

func TestBulkPrintRequiresASignedInUser(t *testing.T) {
	_, handler := newTestAPI(t, newStubLookup())

	request := httptest.NewRequest(http.MethodPost, "/invoices/print",
		strings.NewReader(`{"invoice_ids":["`+uuid.New().String()+`"]}`))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

// Two operators pressing print on the same selection must start each invoice
// once, not twice.
func TestConcurrentBulkPrintsStartEachInvoiceOnce(t *testing.T) {
	bolt := sampleProduct("P-1", "Steel bolt", 100)
	invoices, handler := newTestAPI(t, newStubLookup(bolt))
	ids := createInvoices(t, handler, bolt.ID.String(), 5)
	body := `{"invoice_ids":["` + strings.Join(ids, `","`) + `"]}`

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			doRequest(t, handler, http.MethodPost, "/invoices/print", body)
		}()
	}
	wg.Wait()

	if len(invoices.printRequests) != len(ids) {
		t.Errorf("%d print requests were made, want %d", len(invoices.printRequests), len(ids))
	}
}

func TestServiceRefusesOversizedPrintBatches(t *testing.T) {
	invoices := newMemoryInvoices()
	service := billing.NewService(invoices, newStubLookup(), invoices)

	ids := make([]uuid.UUID, bulk.MaxItems+1)
	if _, err := service.PrintInvoices(t.Context(), ids); !errors.Is(err, bulk.ErrTooManyItems) {
		t.Errorf("PrintInvoices() error = %v, want ErrTooManyItems", err)
	}
	if _, err := service.PrintInvoices(t.Context(), nil); !errors.Is(err, bulk.ErrNoItems) {
		t.Errorf("PrintInvoices() error = %v, want ErrNoItems", err)
	}
}

func TestBulkPrintAtDifferentVolumes(t *testing.T) {
	for _, count := range []int{1, 10, 50} {
		t.Run(fmt.Sprintf("%d invoices", count), func(t *testing.T) {
			bolt := sampleProduct("P-1", "Steel bolt", 1000)
			_, handler := newTestAPI(t, newStubLookup(bolt))
			ids := createInvoices(t, handler, bolt.ID.String(), count)

			recorder := doRequest(t, handler, http.MethodPost, "/invoices/print",
				`{"invoice_ids":["`+strings.Join(ids, `","`)+`"]}`)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if response := decodeBulk(t, recorder); response.Summary.Succeeded != count {
				t.Errorf("summary = %+v, want %d started", response.Summary, count)
			}
		})
	}
}
