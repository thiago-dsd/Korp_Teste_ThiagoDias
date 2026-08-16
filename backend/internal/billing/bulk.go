package billing

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/bulk"
)

// ErrDuplicatedItem reports the same invoice twice in one request.
var ErrDuplicatedItem = apperr.Invalid("duplicated_item", "This invoice appears more than once in the request.")

// PrintInvoices starts printing several invoices in one call.
//
// The items are deliberately independent. Printing is asynchronous: each
// invoice moves to PRINTING with its own event in the outbox, and the stock
// service answers for each one separately. Refusing the whole batch because
// one invoice was already printed would stop work that has nothing wrong with
// it, and there is nothing to roll back: an invoice that was accepted is
// already on its way.
//
// Each invoice is still all or nothing on its own, since the status change and
// the event that asks for the debit are written in one transaction.
func (s *Service) PrintInvoices(ctx context.Context, ids []uuid.UUID) ([]bulk.Result, error) {
	if err := bulk.ValidateSize(len(ids)); err != nil {
		return nil, err
	}

	results := make([]bulk.Result, 0, len(ids))
	seen := make(map[uuid.UUID]int, len(ids))

	for index, id := range ids {
		if id == uuid.Nil {
			results = append(results, bulk.Failure(index, "",
				ErrInvalidInvoice.WithDetails(map[string]string{"invoice_id": "must not be empty"})))
			continue
		}

		// Sending the same invoice twice would print it once and report a
		// confusing conflict for the other, so it is called out instead.
		if earlier, repeated := seen[id]; repeated {
			results = append(results, bulk.Failure(index, id.String(),
				ErrDuplicatedItem.WithDetails(map[string]string{
					"invoice_id": fmt.Sprintf("already sent at position %d", earlier),
				})))
			continue
		}
		seen[id] = index

		invoice, err := s.printing.StartPrinting(ctx, id)
		if err != nil {
			results = append(results, bulk.Failure(index, id.String(), err))
			continue
		}
		results = append(results, bulk.Result{
			Index:     index,
			Status:    bulk.Succeeded,
			ID:        invoice.ID.String(),
			Reference: strconv.FormatInt(invoice.Number, 10),
		})
	}
	return results, nil
}
