package stock

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/bulk"
)

// ProductInput is one product in a bulk registration.
type ProductInput struct {
	Code        string
	Description string
	Balance     int
}

// Adjustment moves the balance of a product by a signed amount: a delivery
// arriving adds, a loss or a correction subtracts.
//
// It is a movement rather than a new balance on purpose. Setting a balance
// would silently undo whatever happened between reading it and writing it,
// including an invoice being printed; adding to it never loses that.
type Adjustment struct {
	// ProductID identifies the product, or ProductCode does when the caller
	// works from a code, which is what a delivery note carries.
	ProductID   uuid.UUID
	ProductCode string
	// Delta is how much to add, and may be negative.
	Delta int
	// Reason is a short note kept in the answer, so a failed item can be told
	// apart in a long list.
	Reason string
}

// Errors reported by the bulk operations.
var (
	// ErrDuplicatedItem reports the same resource twice in one request, which
	// is almost always a mistake in what was sent.
	ErrDuplicatedItem = apperr.Invalid("duplicated_item", "This product appears more than once in the request.")
	// ErrInvalidAdjustment reports an adjustment that cannot be applied.
	ErrInvalidAdjustment = apperr.Invalid("invalid_adjustment", "The adjustment is not valid.")
	// ErrAdjustmentRejected reports a whole adjustment that was rolled back.
	ErrAdjustmentRejected = apperr.Conflict("adjustment_rejected",
		"No balance was changed: one of the items could not be applied.")
)

// CreateProducts registers several products in one call.
//
// Items are independent, so they are applied one by one and a bad row does not
// stop the good ones: importing a catalogue with one malformed line should
// bring the rest in and say which line to fix. Each product is still created
// in its own transaction, so no half written product exists.
func (s *Service) CreateProducts(ctx context.Context, inputs []ProductInput) ([]bulk.Result, error) {
	if err := bulk.ValidateSize(len(inputs)); err != nil {
		return nil, err
	}

	results := make([]bulk.Result, 0, len(inputs))
	seen := make(map[string]int, len(inputs))

	for index, input := range inputs {
		code := strings.ToUpper(strings.TrimSpace(input.Code))

		// A repeated code inside one request would otherwise fail on the
		// database with a message about the previous item, which reads as if
		// the product already existed.
		if earlier, repeated := seen[code]; repeated && code != "" {
			results = append(results, bulk.Failure(index, input.Code,
				ErrDuplicatedItem.WithDetails(map[string]string{
					"code": fmt.Sprintf("already sent at position %d", earlier),
				})))
			continue
		}
		if code != "" {
			seen[code] = index
		}

		product, err := s.CreateProduct(ctx, input.Code, input.Description, input.Balance)
		if err != nil {
			results = append(results, bulk.Failure(index, input.Code, err))
			continue
		}
		results = append(results, bulk.Result{
			Index:     index,
			Status:    bulk.Succeeded,
			ID:        product.ID.String(),
			Reference: product.Code,
		})
	}
	return results, nil
}

// AdjustBalances applies several balance movements together.
//
// Unlike registering products, these items belong to one document: a delivery
// note or a stock count. Applying half of it would leave the operator unsure
// what landed, and sending it again would count the applied half twice, so the
// whole thing is applied in a single transaction or not at all.
func (s *Service) AdjustBalances(ctx context.Context, adjustments []Adjustment) ([]bulk.Result, error) {
	if err := bulk.ValidateSize(len(adjustments)); err != nil {
		return nil, err
	}

	// What can be judged without the database is judged here, so an obviously
	// wrong request never opens a transaction, and so the rule holds whatever
	// the movements are applied against.
	if results, ok := validateAdjustments(adjustments); !ok {
		return results, ErrAdjustmentRejected
	}

	return s.products.Adjust(ctx, adjustments)
}

// validateAdjustments checks the request on its own terms, returning the
// per item answer and whether everything passed.
func validateAdjustments(adjustments []Adjustment) ([]bulk.Result, bool) {
	results := make([]bulk.Result, len(adjustments))
	for index, adjustment := range adjustments {
		results[index] = bulk.Result{Index: index, Status: bulk.Skipped, Reference: referenceOf(adjustment)}
	}

	seen := make(map[string]int, len(adjustments))
	valid := true

	for index, adjustment := range adjustments {
		key := adjustment.ProductID.String() + "|" + strings.ToUpper(adjustment.ProductCode)

		switch {
		case adjustment.ProductID == uuid.Nil && adjustment.ProductCode == "":
			results[index] = bulk.Failure(index, "", ErrInvalidAdjustment.WithDetails(map[string]string{
				"product": "give a product id or a product code",
			}))
			valid = false
		case adjustment.Delta == 0:
			results[index] = bulk.Failure(index, referenceOf(adjustment), ErrInvalidAdjustment.WithDetails(
				map[string]string{"delta": "must not be zero"}))
			valid = false
		case adjustment.Delta > MaxBalance || adjustment.Delta < -MaxBalance:
			results[index] = bulk.Failure(index, referenceOf(adjustment), ErrInvalidAdjustment.WithDetails(
				map[string]string{"delta": "is too large"}))
			valid = false
		default:
			if earlier, repeated := seen[key]; repeated {
				results[index] = bulk.Failure(index, referenceOf(adjustment),
					ErrDuplicatedItem.WithDetails(map[string]string{
						"product": fmt.Sprintf("already sent at position %d", earlier),
					}))
				valid = false
				continue
			}
			seen[key] = index
		}
	}
	return results, valid
}

// Adjust applies every movement in one transaction. The request has already
// been validated on its own terms by the service.
func (s *Store) Adjust(ctx context.Context, adjustments []Adjustment) ([]bulk.Result, error) {
	results := make([]bulk.Result, len(adjustments))
	for index, adjustment := range adjustments {
		results[index] = bulk.Result{Index: index, Status: bulk.Skipped, Reference: referenceOf(adjustment)}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return results, fmt.Errorf("begin adjustment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Products are touched in a stable order, the same one the debit of an
	// invoice uses, so an adjustment and a print can never deadlock each other.
	ordered := slices.Clone(adjustments)
	slices.SortStableFunc(ordered, func(a, b Adjustment) int {
		return strings.Compare(a.ProductID.String()+a.ProductCode, b.ProductID.String()+b.ProductCode)
	})

	indexOf := map[string]int{}
	for index, adjustment := range adjustments {
		indexOf[adjustment.ProductID.String()+"|"+strings.ToUpper(adjustment.ProductCode)] = index
	}

	for _, adjustment := range ordered {
		index := indexOf[adjustment.ProductID.String()+"|"+strings.ToUpper(adjustment.ProductCode)]

		// The balance is moved by the database, never read and written back,
		// so an invoice printed at the same moment is not undone. The
		// condition is what keeps the balance from going negative.
		row := tx.QueryRow(ctx, `
			UPDATE products
			SET balance = balance + $2, version = version + 1, updated_at = now()
			WHERE (id = $1::uuid OR ($1::uuid IS NULL AND upper(code) = upper($3)))
			  AND balance + $2 >= 0
			RETURNING `+productColumns,
			nullableID(adjustment.ProductID), adjustment.Delta, adjustment.ProductCode)

		product, err := scanProduct(row)
		if err == nil {
			// The note the operator wrote on the delivery is what makes this
			// row readable months later, so it is kept with the movement.
			if err := recordMovementTx(ctx, tx, Movement{
				ProductID:    product.ID,
				Delta:        adjustment.Delta,
				BalanceAfter: product.Balance,
				Source:       SourceAdjustment,
				Reason:       adjustment.Reason,
				ActorEmail:   actorFrom(ctx),
			}); err != nil {
				return results, err
			}

			results[index] = bulk.Result{
				Index:     index,
				Status:    bulk.Succeeded,
				ID:        product.ID.String(),
				Reference: product.Code,
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return results, fmt.Errorf("apply adjustment: %w", err)
		}

		// Nothing was updated: the product is unknown, or the movement would
		// take its balance below zero. The difference matters to the operator.
		results[index] = bulk.Failure(index, referenceOf(adjustment), s.explainAdjustment(ctx, tx, adjustment))
		return results, ErrAdjustmentRejected
	}

	if err := tx.Commit(ctx); err != nil {
		return results, fmt.Errorf("commit adjustments: %w", err)
	}
	return results, nil
}

// explainAdjustment says why a movement could not be applied.
func (s *Store) explainAdjustment(ctx context.Context, tx pgx.Tx, adjustment Adjustment) error {
	var balance int
	err := tx.QueryRow(ctx, `
		SELECT balance FROM products
		WHERE id = $1::uuid OR ($1::uuid IS NULL AND upper(code) = upper($2))`,
		nullableID(adjustment.ProductID), adjustment.ProductCode).Scan(&balance)
	if err != nil {
		return ErrProductNotFound
	}
	return ErrInsufficientBalance.WithDetails(map[string]string{
		"available": fmt.Sprint(balance),
		"requested": fmt.Sprint(adjustment.Delta),
	})
}

func referenceOf(adjustment Adjustment) string {
	if adjustment.ProductCode != "" {
		return adjustment.ProductCode
	}
	if adjustment.ProductID != uuid.Nil {
		return adjustment.ProductID.String()
	}
	return ""
}

// nullableID turns an empty id into SQL NULL, so the statement can fall back
// to matching on the code.
func nullableID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
