package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MarkProcessedTx records that a consumer handled a message, inside the same
// transaction as the work itself. It reports false when the message was
// already processed, which is how at-least-once delivery is turned into an
// effect that happens exactly once.
func MarkProcessedTx(ctx context.Context, tx pgx.Tx, consumer string, messageID uuid.UUID) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO processed_messages (consumer, message_id)
		VALUES ($1, $2)
		ON CONFLICT (consumer, message_id) DO NOTHING`, consumer, messageID)
	if err != nil {
		return false, fmt.Errorf("record processed message: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DeleteProcessedBefore removes the record of messages consumed long ago.
//
// The record exists to recognise a redelivery. The broker gives up redelivering
// long before this window closes, so an older row can no longer prevent
// anything — it only makes the deduplication insert slower.
func DeleteProcessedBefore(ctx context.Context, pool *pgxpool.Pool, age time.Duration) (int, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM processed_messages
		WHERE processed_at < now() - $1::interval`, age.String())
	if err != nil {
		return 0, fmt.Errorf("delete processed messages: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
