// Package bulk holds the shape every bulk endpoint answers with.
//
// Applying one operation to many resources raises the same questions
// everywhere: how many at once, what happens when one of them fails, and how
// the caller tells which ones went through. Answering them the same way in
// every service is what makes the endpoints predictable.
package bulk

import (
	"net/http"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

// MaxItems is how many resources one call may touch.
//
// The bound keeps the work of a single request predictable and the answer
// small enough to read, and it matches the largest page a listing serves.
const MaxItems = 100

// Errors reported for the request as a whole, before any item is looked at.
var (
	// ErrNoItems reports an empty request.
	ErrNoItems = apperr.Invalid("no_items", "Send at least one item.")
	// ErrTooManyItems reports a request over the limit.
	ErrTooManyItems = apperr.Invalid("too_many_items", "Send at most 100 items per request.")
)

// Status is what happened to one item.
type Status string

const (
	// Succeeded means the item was applied.
	Succeeded Status = "succeeded"
	// Failed means the item was refused; the reason travels with it.
	Failed Status = "failed"
	// Skipped means the item was not attempted, because the whole operation
	// was rolled back or an earlier item made it pointless.
	Skipped Status = "skipped"
)

// Result is the outcome of a single item.
//
// It carries the position the item had in the request, so the caller can line
// results up with what it sent without matching on values, and only enough
// identity to act on: full entities would make the answer to a hundred items
// unreadable.
type Result struct {
	Index  int    `json:"index"`
	Status Status `json:"status"`
	// ID and Reference identify what was touched, when there is something to
	// identify. Reference is the natural key: a product code, an invoice number.
	ID        string `json:"id,omitempty"`
	Reference string `json:"reference,omitempty"`
	// Error explains a refusal in the same shape as any other error.
	Error *ItemError `json:"error,omitempty"`
}

// ItemError is why one item was refused.
type ItemError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// Summary is the count the caller reads first.
type Summary struct {
	Requested int `json:"requested"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

// Response is what a bulk endpoint answers with.
type Response struct {
	// Atomic says whether the items stand or fall together, so the caller
	// knows what a failure means without reading the documentation again.
	Atomic  bool     `json:"atomic"`
	Summary Summary  `json:"summary"`
	Results []Result `json:"results"`
}

// NewResponse builds the answer from the results of each item.
func NewResponse(atomic bool, results []Result) Response {
	summary := Summary{Requested: len(results)}
	for _, result := range results {
		switch result.Status {
		case Succeeded:
			summary.Succeeded++
		case Failed:
			summary.Failed++
		case Skipped:
			summary.Skipped++
		}
	}
	return Response{Atomic: atomic, Summary: summary, Results: results}
}

// Failure builds the result of a refused item from a domain error.
func Failure(index int, reference string, err error) Result {
	appErr := apperr.From(err)
	return Result{
		Index:     index,
		Status:    Failed,
		Reference: reference,
		Error:     &ItemError{Code: appErr.Code, Message: appErr.Message, Details: appErr.Details},
	}
}

// Write sends the answer with the status that matches the outcome:
// 200 when everything went through and 207 when the items had different fates,
// so a caller that only looks at the status still knows to read the results.
func Write(w http.ResponseWriter, r *http.Request, response Response) {
	status := http.StatusOK
	if response.Summary.Succeeded != response.Summary.Requested {
		status = http.StatusMultiStatus
	}
	httpx.WriteJSON(w, r, status, response)
}

// ValidateSize checks the size of a request before any work is done.
func ValidateSize(count int) error {
	switch {
	case count == 0:
		return ErrNoItems
	case count > MaxItems:
		return ErrTooManyItems.WithDetails(map[string]string{
			"items": "must contain at most 100 items",
		})
	}
	return nil
}
