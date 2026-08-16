package idempotency

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// memoryStore is an in-memory Store used to exercise the middleware.
type memoryStore struct {
	mu      sync.Mutex
	records map[string]*entry
}

type entry struct {
	hash      string
	record    *Record
	completed bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{records: map[string]*entry{}}
}

func (s *memoryStore) Reserve(ctx context.Context, endpoint, key, requestHash string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.records[endpoint+key]
	if !ok {
		s.records[endpoint+key] = &entry{hash: requestHash}
		return nil, nil
	}
	if existing.hash != requestHash {
		return nil, ErrKeyReuse
	}
	if existing.completed {
		return existing.record, nil
	}
	return nil, ErrRequestInProgress
}

func (s *memoryStore) Complete(ctx context.Context, endpoint, key string, record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.records[endpoint+key]
	if !ok {
		return nil
	}
	current.record = &record
	current.completed = true
	return nil
}

func (s *memoryStore) Release(ctx context.Context, endpoint, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.records, endpoint+key)
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// countingHandler answers with an incrementing counter, so a replayed response
// is distinguishable from a second execution.
func countingHandler(calls *int, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		body, _ := io.ReadAll(r.Body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"calls":%d,"received":%q}`, *calls, string(body))
	})
}

func postWithKey(t *testing.T, handler http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/products", strings.NewReader(body))
	if key != "" {
		request.Header.Set(HeaderKey, key)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestMiddlewareReplaysStoredResponse(t *testing.T) {
	calls := 0
	handler := Middleware(newMemoryStore(), discardLogger())(countingHandler(&calls, http.StatusCreated))

	first := postWithKey(t, handler, "key-1", `{"code":"P-1"}`)
	second := postWithKey(t, handler, "key-1", `{"code":"P-1"}`)

	if calls != 1 {
		t.Errorf("handler ran %d times, want 1", calls)
	}
	if second.Code != first.Code {
		t.Errorf("replayed status = %d, want %d", second.Code, first.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("replayed body = %q, want %q", second.Body.String(), first.Body.String())
	}
	if second.Header().Get("Idempotent-Replay") != "true" {
		t.Error("replayed response is not flagged with Idempotent-Replay")
	}
}

func TestMiddlewareRejectsKeyReusedWithDifferentPayload(t *testing.T) {
	calls := 0
	handler := Middleware(newMemoryStore(), discardLogger())(countingHandler(&calls, http.StatusCreated))

	postWithKey(t, handler, "key-1", `{"code":"P-1"}`)
	recorder := postWithKey(t, handler, "key-1", `{"code":"P-2"}`)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if !strings.Contains(recorder.Body.String(), "idempotency_key_reuse") {
		t.Errorf("body = %q, want the key reuse error", recorder.Body.String())
	}
	if calls != 1 {
		t.Errorf("handler ran %d times, want 1", calls)
	}
}

func TestMiddlewareDoesNotCacheServerErrors(t *testing.T) {
	calls := 0
	handler := Middleware(newMemoryStore(), discardLogger())(countingHandler(&calls, http.StatusInternalServerError))

	postWithKey(t, handler, "key-1", `{"code":"P-1"}`)
	postWithKey(t, handler, "key-1", `{"code":"P-1"}`)

	if calls != 2 {
		t.Errorf("handler ran %d times, want 2 (failures must be retryable)", calls)
	}
}

func TestMiddlewareIgnoresRequestsWithoutKey(t *testing.T) {
	calls := 0
	handler := Middleware(newMemoryStore(), discardLogger())(countingHandler(&calls, http.StatusCreated))

	postWithKey(t, handler, "", `{"code":"P-1"}`)
	postWithKey(t, handler, "", `{"code":"P-1"}`)

	if calls != 2 {
		t.Errorf("handler ran %d times, want 2", calls)
	}
}

func TestMiddlewareIgnoresReads(t *testing.T) {
	calls := 0
	handler := Middleware(newMemoryStore(), discardLogger())(countingHandler(&calls, http.StatusOK))

	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/products", nil)
		request.Header.Set(HeaderKey, "key-1")
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}

	if calls != 2 {
		t.Errorf("handler ran %d times, want 2 (reads are never replayed)", calls)
	}
}

func TestMiddlewareRejectsMalformedKey(t *testing.T) {
	calls := 0
	handler := Middleware(newMemoryStore(), discardLogger())(countingHandler(&calls, http.StatusCreated))

	recorder := postWithKey(t, handler, strings.Repeat("k", MaxKeyLength+1), `{"code":"P-1"}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if calls != 0 {
		t.Errorf("handler ran %d times, want 0", calls)
	}
}

func TestMiddlewareReportsRequestInProgress(t *testing.T) {
	store := newMemoryStore()
	calls := 0
	handler := Middleware(store, discardLogger())(countingHandler(&calls, http.StatusCreated))

	// Simulate a first request that reserved the key and is still running.
	if _, err := store.Reserve(context.Background(), "POST /products", "key-1",
		RequestHash(http.MethodPost, "/products", []byte(`{"code":"P-1"}`))); err != nil {
		t.Fatalf("Reserve() returned error: %v", err)
	}

	recorder := postWithKey(t, handler, "key-1", `{"code":"P-1"}`)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if !strings.Contains(recorder.Body.String(), "request_in_progress") {
		t.Errorf("body = %q, want the in progress error", recorder.Body.String())
	}
	if calls != 0 {
		t.Errorf("handler ran %d times, want 0", calls)
	}
}

func TestMiddlewareKeepsRequestBodyReadableByTheHandler(t *testing.T) {
	var received string
	handler := Middleware(newMemoryStore(), discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		w.WriteHeader(http.StatusCreated)
	}))

	postWithKey(t, handler, "key-1", `{"code":"P-1"}`)

	if received != `{"code":"P-1"}` {
		t.Errorf("handler received %q, want the original body", received)
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key     string
		wantErr bool
	}{
		{key: "key-1"},
		{key: "0123456789abcdef"},
		{key: strings.Repeat("k", MaxKeyLength)},
		{key: "", wantErr: true},
		{key: strings.Repeat("k", MaxKeyLength+1), wantErr: true},
		{key: "key with space", wantErr: true},
		{key: "key\nnewline", wantErr: true},
	}

	for _, tc := range tests {
		err := ValidateKey(tc.key)
		if tc.wantErr && err == nil {
			t.Errorf("ValidateKey(%q) = nil, want an error", tc.key)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("ValidateKey(%q) = %v, want nil", tc.key, err)
		}
	}
}

func TestRequestHashDistinguishesRequests(t *testing.T) {
	base := RequestHash(http.MethodPost, "/products", []byte(`{"code":"P-1"}`))

	if base == RequestHash(http.MethodPost, "/products", []byte(`{"code":"P-2"}`)) {
		t.Error("different bodies produced the same hash")
	}
	if base == RequestHash(http.MethodPut, "/products", []byte(`{"code":"P-1"}`)) {
		t.Error("different methods produced the same hash")
	}
	if base == RequestHash(http.MethodPost, "/invoices", []byte(`{"code":"P-1"}`)) {
		t.Error("different paths produced the same hash")
	}
	if base != RequestHash(http.MethodPost, "/products", []byte(`{"code":"P-1"}`)) {
		t.Error("the same request produced different hashes")
	}
}
