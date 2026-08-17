package messaging

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// claimLease is how long a claimed message stays invisible to other relays.
// If the relay crashes mid publish, the message becomes claimable again.
const claimLease = 30 * time.Second

// stalledAfterAttempts is when a message stops looking like a passing outage
// and starts looking like something that needs a person.
//
// It only decides what is reported. A message in the outbox is committed work:
// the state change that produced it is already durable, so the event is not
// optional and the relay keeps trying for as long as it takes. Giving up would
// turn at-least-once delivery into maybe-never and leave the two services
// disagreeing with nothing to detect it.
const stalledAfterAttempts = 10

// Outbox stores events in the same database as the data that produced them, so
// writing an entity and announcing it either both happen or neither does.
type Outbox struct {
	pool *pgxpool.Pool
}

// NewOutbox builds an outbox on top of the given pool.
func NewOutbox(pool *pgxpool.Pool) *Outbox {
	return &Outbox{pool: pool}
}

// EnqueueTx records a message inside the caller's transaction. This is the
// only way to enqueue: an event must never be visible without the state change
// that produced it.
func EnqueueTx(ctx context.Context, tx pgx.Tx, message Message) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (id, type, aggregate_id, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5)`,
		message.ID, message.Type, message.AggregateID, message.Payload, message.OccurredAt)
	if err != nil {
		return fmt.Errorf("enqueue outbox message: %w", err)
	}
	return nil
}

// Claim takes up to limit messages that are due, making them invisible to
// other relays for a while and counting the attempt.
func (o *Outbox) Claim(ctx context.Context, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := o.pool.Query(ctx, `
		UPDATE outbox_messages
		SET attempts = attempts + 1, next_attempt_at = now() + $2::interval
		WHERE id IN (
			SELECT id
			FROM outbox_messages
			WHERE published_at IS NULL
			  AND next_attempt_at <= now()
			ORDER BY sequence
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, type, aggregate_id, payload, occurred_at, attempts, sequence`,
		limit, claimLease.String())
	if err != nil {
		return nil, fmt.Errorf("claim outbox messages: %w", err)
	}
	defer rows.Close()

	type claimed struct {
		message  Message
		sequence int64
	}

	batch := make([]claimed, 0, limit)
	for rows.Next() {
		var entry claimed
		if err := rows.Scan(&entry.message.ID, &entry.message.Type, &entry.message.AggregateID,
			&entry.message.Payload, &entry.message.OccurredAt, &entry.message.Attempts, &entry.sequence); err != nil {
			return nil, fmt.Errorf("scan outbox message: %w", err)
		}
		batch = append(batch, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read outbox messages: %w", err)
	}

	// UPDATE ... RETURNING gives no ordering guarantee, so the batch is put
	// back into the order the events happened before publishing.
	slices.SortFunc(batch, func(a, b claimed) int { return cmp.Compare(a.sequence, b.sequence) })

	messages := make([]Message, 0, len(batch))
	for _, entry := range batch {
		messages = append(messages, entry.message)
	}
	return messages, nil
}

// MarkPublished records that a message reached the broker.
func (o *Outbox) MarkPublished(ctx context.Context, id uuid.UUID) error {
	_, err := o.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET published_at = now(), last_error = NULL
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark outbox message published: %w", err)
	}
	return nil
}

// MarkFailed records a publishing failure and schedules the next attempt with
// an exponential backoff.
func (o *Outbox) MarkFailed(ctx context.Context, id uuid.UUID, attempts int, cause error) error {
	delay := backoffFor(attempts)

	_, err := o.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET next_attempt_at = now() + $2::interval, last_error = $3
		WHERE id = $1`, id, delay.String(), truncateError(cause))
	if err != nil {
		return fmt.Errorf("mark outbox message failed: %w", err)
	}
	return nil
}

// PendingCount reports how many messages are still waiting and how many have
// been failing long enough to need a person. Nothing is ever given up on, so a
// stalled message is still being retried while it is reported.
func (o *Outbox) PendingCount(ctx context.Context) (pending int, stalled int, err error) {
	err = o.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE published_at IS NULL),
			count(*) FILTER (WHERE published_at IS NULL AND attempts >= $1)
		FROM outbox_messages`, stalledAfterAttempts).Scan(&pending, &stalled)
	if err != nil {
		return 0, 0, fmt.Errorf("count outbox messages: %w", err)
	}
	return pending, stalled, nil
}

// backoffFor grows the delay with the number of attempts, up to a minute.
func backoffFor(attempts int) time.Duration {
	delay := time.Second << min(max(attempts-1, 0), 6)
	return min(delay, time.Minute)
}

// truncateError keeps stored error messages short.
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	const limit = 500
	message := err.Error()
	if len(message) > limit {
		return message[:limit]
	}
	return message
}
