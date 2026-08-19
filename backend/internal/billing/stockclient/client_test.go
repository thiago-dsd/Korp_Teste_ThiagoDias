package stockclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/billing/stockclient"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
	"github.com/thiagodias/korp-invoices/internal/platform/resilience"
)

// fastRetry keeps the retry behaviour but removes the waiting.
func fastRetry() resilience.RetryPolicy {
	return resilience.RetryPolicy{Attempts: 3, BaseDelay: time.Microsecond, MaxDelay: time.Microsecond}
}

func newClient(t *testing.T, handler http.HandlerFunc, options ...stockclient.Option) *stockclient.Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	options = append([]stockclient.Option{stockclient.WithRetryPolicy(fastRetry())}, options...)
	return stockclient.New(server.URL, "s3cret", options...)
}

func TestLookupReturnsProducts(t *testing.T) {
	productID := uuid.New()
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(httpx.ServiceTokenHeader) != "s3cret" {
			t.Errorf("service token = %q, want it sent", r.Header.Get(httpx.ServiceTokenHeader))
		}
		if r.URL.Path != "/internal/products/lookup" {
			t.Errorf("path = %q, want the lookup endpoint", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"` + productID.String() +
			`","code":"P-1","description":"Steel bolt","balance":10}]}`))
	})

	products, err := client.Lookup(context.Background(), []uuid.UUID{productID})
	if err != nil {
		t.Fatalf("Lookup() returned error: %v", err)
	}
	if len(products) != 1 {
		t.Fatalf("products = %d, want 1", len(products))
	}
	if products[0].ID != productID || products[0].Code != "P-1" || products[0].Balance != 10 {
		t.Errorf("product = %+v, want the decoded values", products[0])
	}
}

func TestLookupWithoutIDsDoesNotCallTheService(t *testing.T) {
	calls := 0
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
	})

	products, err := client.Lookup(context.Background(), nil)
	if err != nil {
		t.Fatalf("Lookup() returned error: %v", err)
	}
	if len(products) != 0 || calls != 0 {
		t.Errorf("products = %d and calls = %d, want 0 and 0", len(products), calls)
	}
}

func TestLookupPassesRejectionsThroughWithoutRetrying(t *testing.T) {
	calls := 0
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"product_not_found","message":"Product was not found.","details":{"product_id":"x"}}}`))
	})

	_, err := client.Lookup(context.Background(), []uuid.UUID{uuid.New()})
	if err == nil {
		t.Fatal("Lookup() returned no error, want the rejection")
	}

	appErr := apperr.From(err)
	if appErr.Kind != apperr.KindNotFound {
		t.Errorf("Kind = %q, want %q", appErr.Kind, apperr.KindNotFound)
	}
	if appErr.Code != "product_not_found" {
		t.Errorf("Code = %q, want %q", appErr.Code, "product_not_found")
	}
	if appErr.Details["product_id"] != "x" {
		t.Errorf("details = %v, want the details from the stock service", appErr.Details)
	}
	if calls != 1 {
		t.Errorf("service was called %d times, want 1 (rejections are not retried)", calls)
	}
}

func TestLookupRetriesServerErrorsAndRecovers(t *testing.T) {
	calls := 0
	productID := uuid.New()
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"` + productID.String() +
			`","code":"P-1","description":"Steel bolt","balance":10}]}`))
	})

	products, err := client.Lookup(context.Background(), []uuid.UUID{productID})
	if err != nil {
		t.Fatalf("Lookup() returned error: %v", err)
	}
	if len(products) != 1 {
		t.Errorf("products = %d, want 1", len(products))
	}
	if calls != 3 {
		t.Errorf("service was called %d times, want 3", calls)
	}
}

func TestLookupReportsUnavailableAfterRetriesAreExhausted(t *testing.T) {
	calls := 0
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := client.Lookup(context.Background(), []uuid.UUID{uuid.New()})

	if !errors.Is(err, stockclient.ErrStockUnavailable) {
		t.Fatalf("Lookup() error = %v, want ErrStockUnavailable", err)
	}
	if calls != 3 {
		t.Errorf("service was called %d times, want 3 attempts", calls)
	}
	if apperr.From(err).Kind != apperr.KindUnavailable {
		t.Errorf("Kind = %q, want %q", apperr.From(err).Kind, apperr.KindUnavailable)
	}
}

func TestLookupReportsUnavailableWhenTheServiceIsDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close() // nothing is listening anymore

	client := stockclient.New(url, "s3cret", stockclient.WithRetryPolicy(fastRetry()))

	_, err := client.Lookup(context.Background(), []uuid.UUID{uuid.New()})
	if !errors.Is(err, stockclient.ErrStockUnavailable) {
		t.Errorf("Lookup() error = %v, want ErrStockUnavailable", err)
	}
}

func TestLookupOpensTheCircuitAndFailsFast(t *testing.T) {
	calls := 0
	breaker := resilience.NewBreaker(1, time.Minute)
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}, stockclient.WithBreaker(breaker))

	if _, err := client.Lookup(context.Background(), []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("Lookup() returned no error, want a failure")
	}
	callsAfterFirst := calls

	if _, err := client.Lookup(context.Background(), []uuid.UUID{uuid.New()}); !errors.Is(err, stockclient.ErrStockUnavailable) {
		t.Fatalf("Lookup() error = %v, want ErrStockUnavailable", err)
	}
	if calls != callsAfterFirst {
		t.Errorf("service was called %d more times, want 0 while the circuit is open", calls-callsAfterFirst)
	}
	if state := client.BreakerState(); state != "open" {
		t.Errorf("breaker state = %q, want open", state)
	}
}

func TestLookupReportsAuthenticationFailureAsInternal(t *testing.T) {
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"Service token is missing or invalid."}}`))
	})

	_, err := client.Lookup(context.Background(), []uuid.UUID{uuid.New()})

	appErr := apperr.From(err)
	if appErr.Kind != apperr.KindInternal {
		t.Errorf("Kind = %q, want %q", appErr.Kind, apperr.KindInternal)
	}
	if appErr.Code != "stock_authentication_failed" {
		t.Errorf("Code = %q, want %q", appErr.Code, "stock_authentication_failed")
	}
}

func TestLookupReportsMalformedResponse(t *testing.T) {
	calls := 0
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`not json`))
	})

	_, err := client.Lookup(context.Background(), []uuid.UUID{uuid.New()})
	if err == nil {
		t.Fatal("Lookup() returned no error, want one")
	}
	if calls != 1 {
		t.Errorf("service was called %d times, want 1 (a malformed answer is not retried)", calls)
	}
}

func TestLookupHonoursContextCancellation(t *testing.T) {
	// The handler stays busy until the test is over, so the call can only end
	// through the context deadline.
	release := make(chan struct{})
	// Deferred here so the handler is released before the server is closed.
	defer close(release)

	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := client.Lookup(ctx, []uuid.UUID{uuid.New()}); err == nil {
		t.Fatal("Lookup() returned no error, want a timeout failure")
	}
}

func TestListAllReadsEveryPageOfTheCatalogue(t *testing.T) {
	// One page used to be the whole answer, which meant an assistant asked
	// about a real product past the first page said it did not exist.
	pages := 0
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		pages++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + uuid.New().String() +
				`","code":"AAA","description":"First page","balance":1}],"next_cursor":"page-2"}`))
		case "page-2":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + uuid.New().String() +
				`","code":"ZZZ","description":"Last page","balance":2}],"next_cursor":""}`))
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
		}
	})

	products, err := client.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if pages != 2 {
		t.Errorf("requests = %d, want 2", pages)
	}
	if len(products) != 2 {
		t.Fatalf("products = %d, want 2", len(products))
	}
	if products[1].Code != "ZZZ" {
		t.Errorf("second page missing: got %q", products[1].Code)
	}
}

func TestListAllStopsAtTheCatalogueCeiling(t *testing.T) {
	// A catalogue that never ends must not become an endless number of
	// requests: the assistants only put a couple of hundred products in front
	// of the model, and past the ceiling the answer is a search.
	requests := 0
	client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		items := make([]string, 0, 100)
		for i := 0; i < 100; i++ {
			items = append(items, `{"id":"`+uuid.New().String()+`","code":"P","description":"d","balance":1}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[` + strings.Join(items, ",") + `],"next_cursor":"more"}`))
	})

	products, err := client.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(products) > 2000 {
		t.Errorf("products = %d, want the ceiling to hold at 2000", len(products))
	}
	if requests > 25 {
		t.Errorf("requests = %d, want the ceiling to stop the walk", requests)
	}
}
