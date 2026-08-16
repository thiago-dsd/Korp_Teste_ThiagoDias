package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
)

func TestWriteJSONSetsHeadersAndBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/products", nil)

	WriteJSON(recorder, request, http.StatusCreated, map[string]string{"code": "P-1"})

	if recorder.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if body["code"] != "P-1" {
		t.Errorf("body = %v, want code P-1", body)
	}
}

func TestWriteJSONSkipsBodyOnNoContent(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/products/1", nil)

	WriteJSON(recorder, request, http.StatusNoContent, map[string]string{"ignored": "value"})

	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", recorder.Body.String())
	}
}

func TestWriteErrorMapsKindAndKeepsRequestID(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"invalid", apperr.Invalid("validation_failed", "Invalid."), http.StatusBadRequest, "validation_failed"},
		{"not found", apperr.NotFound("product_not_found", "Missing."), http.StatusNotFound, "product_not_found"},
		{"conflict", apperr.Conflict("duplicate_code", "Taken."), http.StatusConflict, "duplicate_code"},
		{"unauthorized", apperr.Unauthorized("unauthorized", "Denied."), http.StatusUnauthorized, "unauthorized"},
		{"unavailable", apperr.Unavailable("stock_unavailable", "Try later."), http.StatusServiceUnavailable, "stock_unavailable"},
		{"unknown", errors.New("database exploded at 10.0.0.1"), http.StatusInternalServerError, "internal_error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/invoices", nil)
			request = request.WithContext(WithRequestID(request.Context(), "req-123"))

			WriteError(recorder, request, tc.err)

			if recorder.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}

			var body errorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("response body is not valid JSON: %v", err)
			}
			if body.Error.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			if body.Error.RequestID != "req-123" {
				t.Errorf("request_id = %q, want %q", body.Error.RequestID, "req-123")
			}
		})
	}
}

func TestWriteErrorHidesInternalDetails(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/products", nil)

	WriteError(recorder, request, errors.New("pq: password authentication failed for user \"stock\""))

	if strings.Contains(recorder.Body.String(), "password") {
		t.Errorf("body = %q, want internal details hidden", recorder.Body.String())
	}
}

func TestWriteErrorIncludesDetails(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/products", nil)
	err := apperr.Invalid("validation_failed", "Invalid.").
		WithDetails(map[string]string{"balance": "must not be negative"})

	WriteError(recorder, request, err)

	var body errorEnvelope
	if jsonErr := json.Unmarshal(recorder.Body.Bytes(), &body); jsonErr != nil {
		t.Fatalf("response body is not valid JSON: %v", jsonErr)
	}
	if body.Error.Details["balance"] != "must not be negative" {
		t.Errorf("details = %v, want the field message", body.Error.Details)
	}
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Code string `json:"code"`
	}

	tests := []struct {
		name     string
		body     string
		wantErr  bool
		wantCode string
	}{
		{name: "valid object", body: `{"code":"P-1"}`},
		{name: "malformed json", body: `{"code":`, wantErr: true, wantCode: "invalid_json"},
		{name: "unknown field", body: `{"code":"P-1","unexpected":1}`, wantErr: true, wantCode: "invalid_json"},
		{name: "trailing content", body: `{"code":"P-1"}{"code":"P-2"}`, wantErr: true, wantCode: "invalid_json"},
		{name: "empty body", body: ``, wantErr: true, wantCode: "invalid_json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/products", strings.NewReader(tc.body))

			var dst payload
			err := DecodeJSON(recorder, request, &dst)

			if tc.wantErr {
				if err == nil {
					t.Fatal("DecodeJSON() returned no error, want one")
				}
				if got := apperr.From(err).Code; got != tc.wantCode {
					t.Errorf("error code = %q, want %q", got, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeJSON() returned error: %v", err)
			}
			if dst.Code != "P-1" {
				t.Errorf("decoded code = %q, want %q", dst.Code, "P-1")
			}
		})
	}
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	huge := `{"code":"` + strings.Repeat("x", 2<<20) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/products", strings.NewReader(huge))

	var dst struct {
		Code string `json:"code"`
	}
	err := DecodeJSON(recorder, request, &dst)

	if err == nil {
		t.Fatal("DecodeJSON() returned no error, want one")
	}
	if got := apperr.From(err).Code; got != "request_too_large" {
		t.Errorf("error code = %q, want %q", got, "request_too_large")
	}
}
