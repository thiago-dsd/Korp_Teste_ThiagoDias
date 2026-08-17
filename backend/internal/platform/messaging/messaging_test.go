package messaging_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
	"github.com/thiagodias/korp-invoices/internal/platform/postgres/pgtest"
	"github.com/thiagodias/korp-invoices/internal/platform/resilience"
)

// schema mirrors the messaging migration shipped with each service, so the
// platform tests do not depend on any of them.
const schema = `
CREATE TABLE outbox_messages (
    id              UUID        PRIMARY KEY,
    sequence        BIGSERIAL   NOT NULL,
    type            TEXT        NOT NULL,
    aggregate_id    TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ,
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT
);
CREATE TABLE processed_messages (
    consumer     TEXT        NOT NULL,
    message_id   UUID        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, message_id)
);
CREATE TABLE applied_events (
    message_id UUID PRIMARY KEY
);`

func newTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	migrations := fstest.MapFS{"migrations/0001_messaging.sql": {Data: []byte(schema)}}
	return pgtest.Pool(t, "MESSAGING_TEST_DATABASE_URL", migrations, "migrations")
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func enqueue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, messageType string, payload any) messaging.Message {
	t.Helper()

	message, err := messaging.NewMessage(messageType, "invoice-1", payload)
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() returned error: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := messaging.EnqueueTx(ctx, tx, message); err != nil {
		t.Fatalf("EnqueueTx() returned error: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit() returned error: %v", err)
	}
	return message
}

func TestNewMessageEncodesPayload(t *testing.T) {
	type payload struct {
		InvoiceID string `json:"invoice_id"`
	}

	message, err := messaging.NewMessage("invoice.print_requested", "invoice-1", payload{InvoiceID: "abc"})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}
	if message.ID == uuid.Nil {
		t.Error("message id is empty")
	}
	if message.OccurredAt.IsZero() {
		t.Error("OccurredAt is empty")
	}

	var decoded payload
	if err := message.Decode(&decoded); err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if decoded.InvoiceID != "abc" {
		t.Errorf("decoded invoice id = %q, want %q", decoded.InvoiceID, "abc")
	}
}

func TestNewMessageRequiresType(t *testing.T) {
	if _, err := messaging.NewMessage("", "invoice-1", map[string]string{}); err == nil {
		t.Error("NewMessage() accepted an empty type, want an error")
	}
}

func TestDecodeReportsMismatchedPayload(t *testing.T) {
	message := messaging.Message{Type: "invoice.print_requested", Payload: json.RawMessage(`{"quantity":"two"}`)}

	var decoded struct {
		Quantity int `json:"quantity"`
	}
	if err := message.Decode(&decoded); err == nil {
		t.Error("Decode() accepted a mismatched payload, want an error")
	}
}

func TestEnqueueIsRolledBackWithItsTransaction(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)

	message, err := messaging.NewMessage("invoice.print_requested", "invoice-1", map[string]string{})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin() returned error: %v", err)
	}
	if err := messaging.EnqueueTx(ctx, tx, message); err != nil {
		t.Fatalf("EnqueueTx() returned error: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback() returned error: %v", err)
	}

	claimed, err := outbox.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim() returned error: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("claimed %d messages, want 0 (the transaction was rolled back)", len(claimed))
	}
}

func TestClaimReturnsPendingMessagesInOrder(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)

	first := enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 1})
	second := enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 2})

	claimed, err := outbox.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim() returned error: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d messages, want 2", len(claimed))
	}
	if claimed[0].ID != first.ID || claimed[1].ID != second.ID {
		t.Error("messages are not ordered by when they occurred")
	}
	if claimed[0].Attempts != 1 {
		t.Errorf("attempts = %d, want 1 after being claimed", claimed[0].Attempts)
	}
}

func TestClaimHidesMessagesFromOtherRelays(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)
	enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 1})

	if _, err := outbox.Claim(ctx, 10); err != nil {
		t.Fatalf("Claim() returned error: %v", err)
	}

	again, err := outbox.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("second Claim() returned error: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("claimed %d messages, want 0 while the lease is held", len(again))
	}
}

func TestMarkPublishedRemovesMessageFromTheQueue(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)
	message := enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 1})

	if _, err := outbox.Claim(ctx, 10); err != nil {
		t.Fatalf("Claim() returned error: %v", err)
	}
	if err := outbox.MarkPublished(ctx, message.ID); err != nil {
		t.Fatalf("MarkPublished() returned error: %v", err)
	}

	pending, exhausted, err := outbox.PendingCount(ctx)
	if err != nil {
		t.Fatalf("PendingCount() returned error: %v", err)
	}
	if pending != 0 || exhausted != 0 {
		t.Errorf("pending = %d and exhausted = %d, want 0 and 0", pending, exhausted)
	}
}

func TestMarkFailedSchedulesARetry(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)
	message := enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 1})

	claimed, err := outbox.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim() returned error: %v", err)
	}
	if err := outbox.MarkFailed(ctx, message.ID, claimed[0].Attempts, errors.New("broker refused")); err != nil {
		t.Fatalf("MarkFailed() returned error: %v", err)
	}

	pending, _, err := outbox.PendingCount(ctx)
	if err != nil {
		t.Fatalf("PendingCount() returned error: %v", err)
	}
	if pending != 1 {
		t.Errorf("pending = %d, want 1 (a failed message stays queued)", pending)
	}

	var lastError string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM outbox_messages WHERE id = $1`, message.ID).Scan(&lastError); err != nil {
		t.Fatalf("read last_error: %v", err)
	}
	if lastError != "broker refused" {
		t.Errorf("last_error = %q, want the failure cause", lastError)
	}
}

// A message that keeps failing is reported, not dropped. It was committed
// together with the state change that produced it, so the only correct outcome
// is that it eventually reaches the broker.
func TestClaimKeepsRetryingAndReportsStalledMessages(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)
	message := enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 1})

	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET attempts = 10, next_attempt_at = now() WHERE id = $1`, message.ID); err != nil {
		t.Fatalf("age the message: %v", err)
	}

	claimed, err := outbox.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim() returned error: %v", err)
	}
	if len(claimed) != 1 {
		t.Errorf("claimed %d messages, want the message to still be retried", len(claimed))
	}

	pending, stalled, err := outbox.PendingCount(ctx)
	if err != nil {
		t.Fatalf("PendingCount() returned error: %v", err)
	}
	// It counts as pending because it is still on its way, and as stalled so
	// somebody learns that the other service is waiting on it.
	if pending != 1 || stalled != 1 {
		t.Errorf("pending = %d and stalled = %d, want 1 and 1", pending, stalled)
	}
}

// stubPublisher records what the relay published and can be made to fail.
type stubPublisher struct {
	published []messaging.Message
	failWith  error
}

func (p *stubPublisher) Publish(ctx context.Context, message messaging.Message) error {
	if p.failWith != nil {
		return p.failWith
	}
	p.published = append(p.published, message)
	return nil
}

func TestRelayPublishesPendingMessages(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)
	enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 1})
	enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 2})

	publisher := &stubPublisher{}
	relay := messaging.NewRelay(outbox, publisher, discardLogger(), messaging.DefaultRelayOptions())

	published, err := relay.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() returned error: %v", err)
	}
	if published != 2 || len(publisher.published) != 2 {
		t.Errorf("published %d messages, want 2", len(publisher.published))
	}

	pending, _, err := outbox.PendingCount(ctx)
	if err != nil {
		t.Fatalf("PendingCount() returned error: %v", err)
	}
	if pending != 0 {
		t.Errorf("pending = %d, want 0", pending)
	}
}

func TestRelayKeepsMessagesWhenTheBrokerIsDown(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)
	enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 1})

	publisher := &stubPublisher{failWith: errors.New("broker unreachable")}
	relay := messaging.NewRelay(outbox, publisher, discardLogger(), messaging.DefaultRelayOptions())

	published, err := relay.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() returned error: %v", err)
	}
	if published != 0 {
		t.Errorf("published = %d, want 0", published)
	}

	pending, _, err := outbox.PendingCount(ctx)
	if err != nil {
		t.Fatalf("PendingCount() returned error: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending = %d, want 1 (nothing is lost while the broker is down)", pending)
	}

	// Once the broker recovers, the message is published without any manual
	// intervention.
	if _, err := pool.Exec(ctx, `UPDATE outbox_messages SET next_attempt_at = now()`); err != nil {
		t.Fatalf("make message due: %v", err)
	}
	publisher.failWith = nil

	if _, err := relay.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() returned error: %v", err)
	}
	if len(publisher.published) != 1 {
		t.Errorf("published %d messages after recovery, want 1", len(publisher.published))
	}
}

func fastRetry() resilience.RetryPolicy {
	return resilience.RetryPolicy{Attempts: 3, BaseDelay: time.Microsecond, MaxDelay: time.Microsecond}
}

func TestConsumerAppliesMessageOnce(t *testing.T) {
	ctx, pool := newTestPool(t)

	applied := 0
	consumer := messaging.NewConsumer("stock", pool, discardLogger(),
		func(ctx context.Context, tx pgx.Tx, message messaging.Message) error {
			applied++
			_, err := tx.Exec(ctx, `INSERT INTO applied_events (message_id) VALUES ($1)`, message.ID)
			return err
		}).WithRetryPolicy(fastRetry())

	message, err := messaging.NewMessage("invoice.print_requested", "invoice-1", map[string]int{"n": 1})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	// The same message is delivered twice, as an at-least-once broker may do.
	for range 2 {
		if err := consumer.Handle(ctx, message); err != nil {
			t.Fatalf("Handle() returned error: %v", err)
		}
	}

	if applied != 1 {
		t.Errorf("handler ran %d times, want 1", applied)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM applied_events`).Scan(&count); err != nil {
		t.Fatalf("count applied events: %v", err)
	}
	if count != 1 {
		t.Errorf("applied events = %d, want 1", count)
	}
}

func TestConsumerRollsBackFailedWork(t *testing.T) {
	ctx, pool := newTestPool(t)

	attempts := 0
	consumer := messaging.NewConsumer("stock", pool, discardLogger(),
		func(ctx context.Context, tx pgx.Tx, message messaging.Message) error {
			attempts++
			if _, err := tx.Exec(ctx, `INSERT INTO applied_events (message_id) VALUES ($1)`, message.ID); err != nil {
				return err
			}
			return errors.New("could not debit stock")
		}).WithRetryPolicy(fastRetry())

	message, err := messaging.NewMessage("invoice.print_requested", "invoice-1", map[string]int{"n": 1})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	if err := consumer.Handle(ctx, message); err == nil {
		t.Fatal("Handle() returned no error, want the failure so the message is dead lettered")
	}
	if attempts != 3 {
		t.Errorf("handler ran %d times, want 3 attempts", attempts)
	}

	var applied, processed int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM applied_events`).Scan(&applied); err != nil {
		t.Fatalf("count applied events: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM processed_messages`).Scan(&processed); err != nil {
		t.Fatalf("count processed messages: %v", err)
	}
	if applied != 0 {
		t.Errorf("applied events = %d, want 0 (the work must roll back)", applied)
	}
	if processed != 0 {
		t.Errorf("processed messages = %d, want 0 (a failed message must stay retryable)", processed)
	}
}

func TestConsumerRecoversFromTransientFailure(t *testing.T) {
	ctx, pool := newTestPool(t)

	attempts := 0
	consumer := messaging.NewConsumer("stock", pool, discardLogger(),
		func(ctx context.Context, tx pgx.Tx, message messaging.Message) error {
			attempts++
			if attempts < 2 {
				return errors.New("database is busy")
			}
			_, err := tx.Exec(ctx, `INSERT INTO applied_events (message_id) VALUES ($1)`, message.ID)
			return err
		}).WithRetryPolicy(fastRetry())

	message, err := messaging.NewMessage("invoice.print_requested", "invoice-1", map[string]int{"n": 1})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	if err := consumer.Handle(ctx, message); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("handler ran %d times, want 2", attempts)
	}
}

func TestConsumerStopsOnPermanentFailure(t *testing.T) {
	ctx, pool := newTestPool(t)

	attempts := 0
	consumer := messaging.NewConsumer("stock", pool, discardLogger(),
		func(ctx context.Context, tx pgx.Tx, message messaging.Message) error {
			attempts++
			return resilience.Permanent(errors.New("payload is not understood"))
		}).WithRetryPolicy(fastRetry())

	message, err := messaging.NewMessage("invoice.print_requested", "invoice-1", map[string]int{"n": 1})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	if err := consumer.Handle(ctx, message); err == nil {
		t.Fatal("Handle() returned no error, want the failure")
	}
	if attempts != 1 {
		t.Errorf("handler ran %d times, want 1 (permanent failures are not retried)", attempts)
	}
}

func TestConsumersDeduplicateIndependently(t *testing.T) {
	ctx, pool := newTestPool(t)

	runs := map[string]int{}
	handler := func(name string) messaging.TxHandler {
		return func(ctx context.Context, tx pgx.Tx, message messaging.Message) error {
			runs[name]++
			return nil
		}
	}
	first := messaging.NewConsumer("stock", pool, discardLogger(), handler("stock")).WithRetryPolicy(fastRetry())
	second := messaging.NewConsumer("billing", pool, discardLogger(), handler("billing")).WithRetryPolicy(fastRetry())

	message, err := messaging.NewMessage("invoice.print_requested", "invoice-1", map[string]int{"n": 1})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}

	if err := first.Handle(ctx, message); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}
	if err := second.Handle(ctx, message); err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if runs["stock"] != 1 || runs["billing"] != 1 {
		t.Errorf("runs = %v, want each consumer to handle the message once", runs)
	}
}

// A message in the outbox is committed work: the state change that produced it
// is already durable, so the event is not optional. A broker outage must delay
// it, never drop it — which is what the relay promises in its own doc comment.
func TestOutboxKeepsRetryingAfterALongBrokerOutage(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)
	enqueue(t, ctx, pool, "invoice.print_requested", map[string]string{"invoice_id": "abc"})

	// The broker is unreachable for a long stretch: every round claims the
	// message, fails to publish it and schedules another try.
	const rounds = 25
	for round := 1; round <= rounds; round++ {
		claimed, err := outbox.Claim(ctx, 10)
		if err != nil {
			t.Fatalf("Claim() returned error on round %d: %v", round, err)
		}
		if len(claimed) != 1 {
			t.Fatalf("round %d claimed %d messages, want the message to still be retried", round, len(claimed))
		}
		if err := outbox.MarkFailed(ctx, claimed[0].ID, claimed[0].Attempts, errors.New("broker is down")); err != nil {
			t.Fatalf("MarkFailed() returned error: %v", err)
		}
		// The backoff would otherwise hold the message back between rounds.
		if _, err := pool.Exec(ctx, `UPDATE outbox_messages SET next_attempt_at = now()`); err != nil {
			t.Fatalf("reset backoff: %v", err)
		}
	}

	// The broker comes back.
	claimed, err := outbox.Claim(ctx, 10)
	if err != nil {
		t.Fatalf("Claim() returned error: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d messages after the outage, want the message to survive and be delivered", len(claimed))
	}
}

func TestRetentionRemovesPublishedMessagesAndKeepsPendingOnes(t *testing.T) {
	ctx, pool := newTestPool(t)
	outbox := messaging.NewOutbox(pool)

	published := enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 1})
	pending := enqueue(t, ctx, pool, "invoice.print_requested", map[string]int{"n": 2})

	if err := outbox.MarkPublished(ctx, published.ID); err != nil {
		t.Fatalf("MarkPublished() returned error: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_messages SET published_at = now() - interval '30 days' WHERE id = $1`, published.ID); err != nil {
		t.Fatalf("age the published message: %v", err)
	}

	removed, err := outbox.DeletePublishedBefore(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DeletePublishedBefore() returned error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d messages, want 1", removed)
	}

	// An event still on its way must survive any cleanup: it is committed work.
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE id = $1`, pending.ID).Scan(&left); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if left != 1 {
		t.Error("retention removed an event that had not been published yet")
	}
}

func TestRetentionRemovesOldProcessedMessages(t *testing.T) {
	ctx, pool := newTestPool(t)

	old, recent := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{old, recent} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("Begin() returned error: %v", err)
		}
		if _, err := messaging.MarkProcessedTx(ctx, tx, "consumer", id); err != nil {
			t.Fatalf("MarkProcessedTx() returned error: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("Commit() returned error: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		UPDATE processed_messages SET processed_at = now() - interval '30 days' WHERE message_id = $1`, old); err != nil {
		t.Fatalf("age the record: %v", err)
	}

	removed, err := messaging.DeleteProcessedBefore(ctx, pool, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DeleteProcessedBefore() returned error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d records, want 1", removed)
	}

	var left int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM processed_messages WHERE message_id = $1`, recent).Scan(&left); err != nil {
		t.Fatalf("count recent: %v", err)
	}
	if left != 1 {
		t.Error("retention removed a record that could still recognise a redelivery")
	}
}
