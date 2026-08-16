package bulk_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/bulk"
)

func TestValidateSizeRefusesAnEmptyRequest(t *testing.T) {
	if err := bulk.ValidateSize(0); !errors.Is(err, bulk.ErrNoItems) {
		t.Fatalf("expected an empty request to be refused, got %v", err)
	}
}

func TestValidateSizeAcceptsUpToTheLimit(t *testing.T) {
	if err := bulk.ValidateSize(bulk.MaxItems); err != nil {
		t.Fatalf("expected %d items to be accepted, got %v", bulk.MaxItems, err)
	}
}

func TestValidateSizeRefusesOneItemOverTheLimit(t *testing.T) {
	err := bulk.ValidateSize(bulk.MaxItems + 1)
	if !errors.Is(err, bulk.ErrTooManyItems) {
		t.Fatalf("expected the limit to be enforced, got %v", err)
	}
	// The caller should be told what to change, not only that it was refused.
	if apperr.From(err).Details["items"] == "" {
		t.Fatal("expected the answer to say what the limit is")
	}
}

func TestNewResponseCountsEveryOutcome(t *testing.T) {
	response := bulk.NewResponse(false, []bulk.Result{
		{Index: 0, Status: bulk.Succeeded},
		{Index: 1, Status: bulk.Failed},
		{Index: 2, Status: bulk.Skipped},
		{Index: 3, Status: bulk.Succeeded},
	})

	want := bulk.Summary{Requested: 4, Succeeded: 2, Failed: 1, Skipped: 1}
	if response.Summary != want {
		t.Fatalf("summary = %+v, want %+v", response.Summary, want)
	}
}

func TestFailureCarriesTheDomainError(t *testing.T) {
	cause := apperr.Invalid("bad_code", "The code is not valid.").
		WithDetails(map[string]string{"code": "must not be empty"})

	result := bulk.Failure(2, "ABC", cause)

	if result.Index != 2 || result.Status != bulk.Failed || result.Reference != "ABC" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Error == nil || result.Error.Code != "bad_code" {
		t.Fatalf("expected the domain code to survive, got %+v", result.Error)
	}
	if result.Error.Details["code"] != "must not be empty" {
		t.Fatalf("expected the details to survive, got %+v", result.Error.Details)
	}
}

func TestWriteAnswersOKWhenEveryItemSucceeded(t *testing.T) {
	recorder := write(t, []bulk.Result{
		{Index: 0, Status: bulk.Succeeded},
		{Index: 1, Status: bulk.Succeeded},
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestWriteAnswersMultiStatusWhenAnItemFailed(t *testing.T) {
	recorder := write(t, []bulk.Result{
		{Index: 0, Status: bulk.Succeeded},
		{Index: 1, Status: bulk.Failed},
	})

	// A caller that only reads the status code still learns it must look at
	// the results, which is the whole point of not answering 200 here.
	if recorder.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMultiStatus)
	}
}

func TestWriteAnswersMultiStatusWhenEverythingWasRolledBack(t *testing.T) {
	recorder := write(t, []bulk.Result{
		{Index: 0, Status: bulk.Skipped},
		{Index: 1, Status: bulk.Skipped},
	})

	if recorder.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMultiStatus)
	}
}

func TestWriteKeepsThePositionOfEveryItem(t *testing.T) {
	recorder := write(t, []bulk.Result{
		{Index: 0, Status: bulk.Succeeded, Reference: "A"},
		{Index: 1, Status: bulk.Failed, Reference: "B"},
	})

	var body bulk.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	for position, result := range body.Results {
		if result.Index != position {
			t.Fatalf("result at %d reports index %d", position, result.Index)
		}
	}
}

func write(t *testing.T, results []bulk.Result) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/products/bulk", nil)
	bulk.Write(recorder, request, bulk.NewResponse(false, results))
	return recorder
}
