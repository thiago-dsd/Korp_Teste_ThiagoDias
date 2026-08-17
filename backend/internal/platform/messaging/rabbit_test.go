package messaging_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/thiagodias/korp-invoices/internal/platform/messaging"
)

// requireRabbit connects to the broker in RABBITMQ_TEST_URL, skipping the test
// when none is configured.
func requireRabbit(t *testing.T) (context.Context, *messaging.Rabbit, string) {
	t.Helper()

	url := os.Getenv("RABBITMQ_TEST_URL")
	if url == "" {
		t.Skip("RABBITMQ_TEST_URL is not set; skipping RabbitMQ integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	rabbit, err := messaging.Connect(ctx, url)
	if err != nil {
		t.Fatalf("Connect() returned error: %v", err)
	}
	t.Cleanup(func() { _ = rabbit.Close() })

	// Each test owns a queue, so runs never interfere with each other.
	queue := "test." + uuid.NewString()
	t.Cleanup(func() { deleteQueues(t, url, queue, queue+".dlq") })

	return ctx, rabbit, queue
}

func deleteQueues(t *testing.T, url string, names ...string) {
	t.Helper()

	conn, err := amqp.Dial(url)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	channel, err := conn.Channel()
	if err != nil {
		return
	}
	defer func() { _ = channel.Close() }()

	for _, name := range names {
		_, _ = channel.QueueDelete(name, false, false, false)
	}
}

func TestPublishAndConsumeRoundTrip(t *testing.T) {
	ctx, rabbit, queue := requireRabbit(t)

	spec := messaging.QueueSpec{Name: queue, RoutingKeys: []string{"invoice.print_requested"}}
	if err := rabbit.DeclareQueue(spec); err != nil {
		t.Fatalf("DeclareQueue() returned error: %v", err)
	}

	received := make(chan messaging.Message, 1)
	consumeCtx, stopConsuming := context.WithCancel(ctx)
	defer stopConsuming()

	go func() {
		_ = rabbit.Consume(consumeCtx, spec, 1, func(ctx context.Context, message messaging.Message) error {
			received <- message
			return nil
		})
	}()

	sent, err := messaging.NewMessage("invoice.print_requested", "invoice-1", map[string]string{"invoice_id": "abc"})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}
	if err := rabbit.Publish(ctx, sent); err != nil {
		t.Fatalf("Publish() returned error: %v", err)
	}

	select {
	case got := <-received:
		if got.ID != sent.ID {
			t.Errorf("message id = %v, want %v", got.ID, sent.ID)
		}
		if got.Type != sent.Type {
			t.Errorf("type = %q, want %q", got.Type, sent.Type)
		}
		if got.AggregateID != "invoice-1" {
			t.Errorf("aggregate id = %q, want %q", got.AggregateID, "invoice-1")
		}

		var payload map[string]string
		if err := got.Decode(&payload); err != nil {
			t.Fatalf("Decode() returned error: %v", err)
		}
		if payload["invoice_id"] != "abc" {
			t.Errorf("payload = %v, want the published body", payload)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no message was received within 10s")
	}
}

func TestConsumeIgnoresUnboundMessageTypes(t *testing.T) {
	ctx, rabbit, queue := requireRabbit(t)

	spec := messaging.QueueSpec{Name: queue, RoutingKeys: []string{"invoice.print_requested"}}
	if err := rabbit.DeclareQueue(spec); err != nil {
		t.Fatalf("DeclareQueue() returned error: %v", err)
	}

	received := make(chan messaging.Message, 1)
	consumeCtx, stopConsuming := context.WithCancel(ctx)
	defer stopConsuming()

	go func() {
		_ = rabbit.Consume(consumeCtx, spec, 1, func(ctx context.Context, message messaging.Message) error {
			received <- message
			return nil
		})
	}()

	other, err := messaging.NewMessage("stock.debited", "invoice-1", map[string]string{})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}
	if err := rabbit.Publish(ctx, other); err != nil {
		t.Fatalf("Publish() returned error: %v", err)
	}

	select {
	case got := <-received:
		t.Errorf("received message of type %q, want none", got.Type)
	case <-time.After(2 * time.Second):
		// Nothing arrived, which is the expected outcome.
	}
}

func TestRejectedMessagesGoToTheDeadLetterQueue(t *testing.T) {
	ctx, rabbit, queue := requireRabbit(t)

	spec := messaging.QueueSpec{Name: queue, RoutingKeys: []string{"invoice.print_requested"}}
	if err := rabbit.DeclareQueue(spec); err != nil {
		t.Fatalf("DeclareQueue() returned error: %v", err)
	}

	handled := make(chan struct{}, 1)
	consumeCtx, stopConsuming := context.WithCancel(ctx)
	defer stopConsuming()

	go func() {
		_ = rabbit.Consume(consumeCtx, spec, 1, func(ctx context.Context, message messaging.Message) error {
			handled <- struct{}{}
			return errors.New("handler gave up")
		})
	}()

	sent, err := messaging.NewMessage("invoice.print_requested", "invoice-1", map[string]string{})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}
	if err := rabbit.Publish(ctx, sent); err != nil {
		t.Fatalf("Publish() returned error: %v", err)
	}

	select {
	case <-handled:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler was never called")
	}

	// The rejected message must be waiting in the dead letter queue.
	deadLettered := waitForDeadLetter(t, queue+".dlq", sent.ID.String())
	if !deadLettered {
		t.Error("the rejected message is not in the dead letter queue")
	}
}

func waitForDeadLetter(t *testing.T, queue, messageID string) bool {
	t.Helper()

	conn, err := amqp.Dial(os.Getenv("RABBITMQ_TEST_URL"))
	if err != nil {
		t.Fatalf("dial rabbitmq: %v", err)
	}
	defer func() { _ = conn.Close() }()

	channel, err := conn.Channel()
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}
	defer func() { _ = channel.Close() }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		delivery, ok, err := channel.Get(queue, true)
		if err != nil {
			t.Fatalf("get from dead letter queue: %v", err)
		}
		if ok {
			return delivery.MessageId == messageID
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func TestPingReportsAClosedConnection(t *testing.T) {
	ctx, rabbit, _ := requireRabbit(t)

	if err := rabbit.Ping(ctx); err != nil {
		t.Fatalf("Ping() returned error on a live connection: %v", err)
	}
	if err := rabbit.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if err := rabbit.Ping(ctx); err == nil {
		t.Error("Ping() returned no error after closing, want one")
	}
}

// A message the consumer gave up on has to be findable and, once whatever it
// was waiting for is fixed, sendable back through the ordinary path.
func TestDeadLetteredMessagesCanBeCountedAndReplayed(t *testing.T) {
	ctx, rabbit, queue := requireRabbit(t)
	spec := messaging.QueueSpec{Name: queue, RoutingKeys: []string{queue}}

	if err := rabbit.DeclareQueue(spec); err != nil {
		t.Fatalf("DeclareQueue() returned error: %v", err)
	}

	message, err := messaging.NewMessage(queue, "invoice-1", map[string]int{"n": 1})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}
	if err := rabbit.Publish(ctx, message); err != nil {
		t.Fatalf("Publish() returned error: %v", err)
	}

	// A consumer that refuses the message sends it to the dead letter queue.
	refuse, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = rabbit.Consume(refuse, spec, 1, func(context.Context, messaging.Message) error {
		return errors.New("cannot handle this")
	})

	deadLetters := messaging.DeadLetterQueue(queue)
	depth, err := rabbit.QueueDepth(deadLetters)
	if err != nil {
		t.Fatalf("QueueDepth() returned error: %v", err)
	}
	if depth != 1 {
		t.Fatalf("dead letter depth = %d, want 1", depth)
	}

	// Replaying puts it back on the ordinary queue, keeping its id so a
	// consumer that already applied it recognises the repeat.
	replayed, err := rabbit.Replay(ctx, deadLetters, 10)
	if err != nil {
		t.Fatalf("Replay() returned error: %v", err)
	}
	if replayed != 1 {
		t.Errorf("replayed %d messages, want 1", replayed)
	}

	received := make(chan messaging.Message, 1)
	accept, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	_ = rabbit.Consume(accept, spec, 1, func(_ context.Context, delivered messaging.Message) error {
		received <- delivered
		stop()
		return nil
	})

	select {
	case delivered := <-received:
		if delivered.ID != message.ID {
			t.Errorf("replayed message id = %s, want %s", delivered.ID, message.ID)
		}
	default:
		t.Error("the replayed message never came back to the ordinary queue")
	}

	if depth, err := rabbit.QueueDepth(deadLetters); err != nil || depth != 0 {
		t.Errorf("dead letter depth = %d (err %v), want it emptied", depth, err)
	}
}
