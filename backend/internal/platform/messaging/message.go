// Package messaging carries events between the services: a transactional
// outbox, a relay that publishes to RabbitMQ and consumers that process each
// message at most once.
package messaging

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Message is an event moving between services.
type Message struct {
	// ID identifies the message and is what consumers deduplicate on.
	ID uuid.UUID
	// Type is the routing key, such as "invoice.print_requested".
	Type string
	// AggregateID is the entity the event is about, used for logs and tracing.
	AggregateID string
	// Payload is the event body as JSON.
	Payload json.RawMessage
	// OccurredAt is when the event happened.
	OccurredAt time.Time
	// Attempts is how many times publishing this message was tried.
	Attempts int
}

// NewMessage builds a message with a fresh id, encoding payload as JSON.
func NewMessage(messageType, aggregateID string, payload any) (Message, error) {
	if messageType == "" {
		return Message{}, fmt.Errorf("messaging: message type is required")
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("encode message payload: %w", err)
	}
	return Message{
		ID:          uuid.New(),
		Type:        messageType,
		AggregateID: aggregateID,
		Payload:     encoded,
		OccurredAt:  time.Now().UTC(),
	}, nil
}

// Decode unmarshals the payload into target.
func (m Message) Decode(target any) error {
	if err := json.Unmarshal(m.Payload, target); err != nil {
		return fmt.Errorf("decode payload of message %s: %w", m.Type, err)
	}
	return nil
}
