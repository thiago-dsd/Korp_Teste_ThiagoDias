package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thiagodias/korp-invoices/internal/platform/resilience"
)

// TxHandler applies a message inside a transaction. Whatever it writes is
// committed together with the record that the message was processed, so the
// effect happens exactly once even though delivery is at-least-once.
type TxHandler func(ctx context.Context, tx pgx.Tx, message Message) error

// Consumer runs message handlers with deduplication and bounded retries.
type Consumer struct {
	name    string
	pool    *pgxpool.Pool
	logger  *slog.Logger
	retry   resilience.RetryPolicy
	handler TxHandler
}

// NewConsumer builds a consumer identified by name, which is also the key used
// to deduplicate messages.
func NewConsumer(name string, pool *pgxpool.Pool, logger *slog.Logger, handler TxHandler) *Consumer {
	return &Consumer{
		name:    name,
		pool:    pool,
		logger:  logger,
		retry:   resilience.RetryPolicy{Attempts: 3, BaseDelay: 200 * time.Millisecond, MaxDelay: 2 * time.Second},
		handler: handler,
	}
}

// WithRetryPolicy replaces the retry policy, mainly to keep tests fast.
func (c *Consumer) WithRetryPolicy(policy resilience.RetryPolicy) *Consumer {
	c.retry = policy
	return c
}

// Handle processes a message, retrying transient failures. It returns an error
// only when the message could not be handled after every attempt, which tells
// the transport to dead letter it.
func (c *Consumer) Handle(ctx context.Context, message Message) error {
	err := resilience.Retry(ctx, c.retry, func(ctx context.Context) error {
		return c.handleOnce(ctx, message)
	})
	if err != nil {
		c.logger.ErrorContext(ctx, "giving up on message",
			"consumer", c.name,
			"message_id", message.ID,
			"type", message.Type,
			"error", err,
		)
		return err
	}
	return nil
}

func (c *Consumer) handleOnce(ctx context.Context, message Message) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin consumer transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fresh, err := MarkProcessedTx(ctx, tx, c.name, message.ID)
	if err != nil {
		return err
	}
	if !fresh {
		// Already applied: acknowledge without doing the work again.
		c.logger.DebugContext(ctx, "skipping message already processed",
			"consumer", c.name, "message_id", message.ID, "type", message.Type)
		return nil
	}

	if err := c.handler(ctx, tx, message); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit consumer transaction: %w", err)
	}
	return nil
}
