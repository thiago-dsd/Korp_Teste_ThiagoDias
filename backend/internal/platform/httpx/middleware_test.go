package httpx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequestIDGeneratesWhenMissing(t *testing.T) {
	var seen string
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	if seen == "" {
		t.Fatal("request id in context is empty, want a generated id")
	}
	if got := recorder.Header().Get(RequestIDHeader); got != seen {
		t.Errorf("response header = %q, want %q", got, seen)
	}
}

func TestRequestIDReusesSafeIncomingID(t *testing.T) {
	var seen string
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(RequestIDHeader, "trace-42")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	if seen != "trace-42" {
		t.Errorf("request id = %q, want %q", seen, "trace-42")
	}
}

func TestRequestIDRejectsUnsafeIncomingID(t *testing.T) {
	unsafeIDs := []string{
		"trace with spaces",
		"trace\nX-Injected: yes",
		strings.Repeat("a", 65),
		"trace;drop",
	}

	for _, unsafe := range unsafeIDs {
		var seen string
		handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = RequestIDFrom(r.Context())
		}))

		request := httptest.NewRequest(http.MethodGet, "/health", nil)
		// Set the header directly to bypass net/http header validation.
		request.Header[RequestIDHeader] = []string{unsafe}

		handler.ServeHTTP(httptest.NewRecorder(), request)

		if seen == unsafe {
			t.Errorf("request id = %q, want a generated id instead of the unsafe value", seen)
		}
		if seen == "" {
			t.Error("request id is empty, want a generated id")
		}
	}
}

func TestRecoverTurnsPanicIntoInternalError(t *testing.T) {
	handler := Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom: secret connection string")
		}),
		RequestID(),
		Recover(discardLogger()),
	)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/products", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Errorf("body = %q, want panic details hidden", recorder.Body.String())
	}
}

func TestTimeoutCancelsRequestContext(t *testing.T) {
	var deadlineSet bool
	handler := Timeout(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, deadlineSet = r.Context().Deadline()
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if !deadlineSet {
		t.Error("handler context has no deadline, want one")
	}
}

func TestCORSAllowsConfiguredOriginOnly(t *testing.T) {
	handler := CORS([]string{"http://localhost:4200"})(okHandler())

	tests := []struct {
		name         string
		origin       string
		wantAllowHdr string
	}{
		{"allowed origin", "http://localhost:4200", "http://localhost:4200"},
		{"other origin", "https://evil.example.com", ""},
		{"no origin", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/products", nil)
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != tc.wantAllowHdr {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantAllowHdr)
			}
			if got := recorder.Header().Get("Vary"); !strings.Contains(got, "Origin") {
				t.Errorf("Vary = %q, want it to contain Origin", got)
			}
		})
	}
}

// Without this header the browser blocks every authenticated request.
func TestCORSAllowsTheAuthorizationHeader(t *testing.T) {
	handler := CORS([]string{"http://localhost:4200"})(okHandler())

	request := httptest.NewRequest(http.MethodOptions, "/products", nil)
	request.Header.Set("Origin", "http://localhost:4200")
	request.Header.Set("Access-Control-Request-Method", "GET")
	request.Header.Set("Access-Control-Request-Headers", "authorization")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	allowed := recorder.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(allowed), "authorization") {
		t.Errorf("Access-Control-Allow-Headers = %q, want it to allow Authorization", allowed)
	}
}

func TestCORSPreflight(t *testing.T) {
	handler := CORS([]string{"http://localhost:4200"})(okHandler())

	tests := []struct {
		origin     string
		wantStatus int
	}{
		{"http://localhost:4200", http.StatusNoContent},
		{"https://evil.example.com", http.StatusForbidden},
	}

	for _, tc := range tests {
		request := httptest.NewRequest(http.MethodOptions, "/products", nil)
		request.Header.Set("Origin", tc.origin)

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != tc.wantStatus {
			t.Errorf("origin %s: status = %d, want %d", tc.origin, recorder.Code, tc.wantStatus)
		}
	}
}

func TestRequireServiceToken(t *testing.T) {
	handler := Chain(okHandler(), RequireServiceToken("s3cret"))

	tests := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{"valid token", "s3cret", http.StatusOK},
		{"wrong token", "wrong", http.StatusUnauthorized},
		{"missing token", "", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/internal/stock/debits", nil)
			if tc.token != "" {
				request.Header.Set(ServiceTokenHeader, tc.token)
			}

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
		})
	}
}

func TestRequireServiceTokenRejectsEmptyConfiguredToken(t *testing.T) {
	handler := Chain(okHandler(), RequireServiceToken(""))

	request := httptest.NewRequest(http.MethodPost, "/internal/stock/debits", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestChainRunsMiddlewaresInOrder(t *testing.T) {
	var order []string
	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "first")
			next.ServeHTTP(w, r)
		})
	}
	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "second")
			next.ServeHTTP(w, r)
		})
	}

	handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}), first, second)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first", "second", "handler"}
	for i, step := range want {
		if order[i] != step {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestLoggerRecordsStatus(t *testing.T) {
	var buffer strings.Builder
	logger := slog.New(slog.NewTextHandler(&buffer, nil))

	handler := Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}),
		RequestID(),
		Logger(logger),
	)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/brew", nil))

	logged := buffer.String()
	if !strings.Contains(logged, "status=418") {
		t.Errorf("log = %q, want it to record status 418", logged)
	}
	if !strings.Contains(logged, "/brew") {
		t.Errorf("log = %q, want it to record the path", logged)
	}
}
