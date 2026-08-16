package messaging

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These tests live inside the package because recovering from a lost
// connection can only be observed by closing the underlying one.
func connectForTest(t *testing.T) (context.Context, *Rabbit) {
	t.Helper()

	url := os.Getenv("RABBITMQ_TEST_URL")
	if url == "" {
		t.Skip("RABBITMQ_TEST_URL is not set; skipping RabbitMQ integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	rabbit, err := Connect(ctx, url)
	if err != nil {
		t.Fatalf("Connect() returned error: %v", err)
	}
	t.Cleanup(func() { _ = rabbit.Close() })

	return ctx, rabbit
}

func TestPublishReconnectsAfterTheConnectionIsLost(t *testing.T) {
	ctx, rabbit := connectForTest(t)

	message, err := NewMessage("invoice.print_requested", "invoice-1", map[string]string{})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}
	if err := rabbit.Publish(ctx, message); err != nil {
		t.Fatalf("Publish() returned error: %v", err)
	}

	// Simulate the broker dropping the connection.
	rabbit.mu.Lock()
	conn := rabbit.conn
	rabbit.mu.Unlock()
	if err := conn.Close(); err != nil {
		t.Fatalf("closing the connection failed: %v", err)
	}

	second, err := NewMessage("invoice.print_requested", "invoice-1", map[string]string{})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}
	if err := rabbit.Publish(ctx, second); err != nil {
		t.Fatalf("Publish() after a lost connection returned error: %v", err)
	}
}

func TestPingReconnectsAfterTheConnectionIsLost(t *testing.T) {
	ctx, rabbit := connectForTest(t)

	rabbit.mu.Lock()
	conn := rabbit.conn
	rabbit.mu.Unlock()
	if err := conn.Close(); err != nil {
		t.Fatalf("closing the connection failed: %v", err)
	}

	if err := rabbit.Ping(ctx); err != nil {
		t.Errorf("Ping() returned error after a lost connection: %v", err)
	}
}

func TestClosedClientDoesNotReconnect(t *testing.T) {
	ctx, rabbit := connectForTest(t)

	if err := rabbit.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}

	if err := rabbit.Ping(ctx); err == nil {
		t.Error("Ping() returned no error after Close(), want one")
	}

	message, err := NewMessage("invoice.print_requested", uuid.NewString(), map[string]string{})
	if err != nil {
		t.Fatalf("NewMessage() returned error: %v", err)
	}
	if err := rabbit.Publish(ctx, message); err == nil {
		t.Error("Publish() returned no error after Close(), want one")
	}
}
