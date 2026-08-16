package messaging

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
