package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Topology names shared by the services.
const (
	// Exchange carries every event of the system, routed by message type.
	Exchange = "invoices"
	// DeadLetterExchange receives messages a consumer could not handle.
	DeadLetterExchange = "invoices.dlx"
)

// Rabbit owns the connection and channels used to publish and consume.
type Rabbit struct {
	url string

	mu             sync.Mutex
	conn           *amqp.Connection
	publishChannel *amqp.Channel
	closed         bool
}

// Connect opens a connection and declares the exchanges.
func Connect(ctx context.Context, url string) (*Rabbit, error) {
	rabbit := &Rabbit{url: url}
	if _, err := rabbit.connection(); err != nil {
		return nil, err
	}
	return rabbit, nil
}

// Close releases the connection and stops reconnecting.
func (r *Rabbit) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
	if r.publishChannel != nil {
		_ = r.publishChannel.Close()
		r.publishChannel = nil
	}
	if r.conn != nil && !r.conn.IsClosed() {
		return r.conn.Close()
	}
	return nil
}

// Ping reports whether the broker is reachable, reconnecting if the previous
// connection was lost. The service recovers on its own once the broker is back.
func (r *Rabbit) Ping(ctx context.Context) error {
	_, err := r.connection()
	return err
}

// connection returns a live connection, dialling again when the previous one
// was lost. A broker restart therefore heals without restarting the service.
func (r *Rabbit) connection() (*amqp.Connection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, fmt.Errorf("rabbitmq client is closed")
	}
	if r.conn != nil && !r.conn.IsClosed() {
		return r.conn, nil
	}

	conn, err := amqp.DialConfig(r.url, amqp.Config{
		Heartbeat: 10 * time.Second,
		Dial:      amqp.DefaultDial(5 * time.Second),
	})
	if err != nil {
		return nil, fmt.Errorf("connect to rabbitmq: %w", err)
	}
	if err := declareExchanges(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// Channels of the old connection are dead with it.
	r.publishChannel = nil
	r.conn = conn
	return conn, nil
}

func declareExchanges(conn *amqp.Connection) error {
	channel, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer func() { _ = channel.Close() }()

	for _, exchange := range []string{Exchange, DeadLetterExchange} {
		if err := channel.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare exchange %s: %w", exchange, err)
		}
	}
	return nil
}

// Publish sends a message to the exchange, routed by its type. Publisher
// confirms are enabled, so Publish only returns nil once the broker has
// accepted and persisted the message.
func (r *Rabbit) Publish(ctx context.Context, message Message) error {
	channel, err := r.publisherChannel()
	if err != nil {
		return err
	}

	confirmation, err := channel.PublishWithDeferredConfirmWithContext(ctx, Exchange, message.Type, true, false,
		amqp.Publishing{
			MessageId:     message.ID.String(),
			CorrelationId: message.CorrelationID,
			Type:          message.Type,
			Timestamp:     message.OccurredAt,
			ContentType:   "application/json",
			DeliveryMode:  amqp.Persistent,
			Body:          message.Payload,
			Headers:       amqp.Table{"aggregate_id": message.AggregateID},
		})
	if err != nil {
		r.dropPublisherChannel()
		return fmt.Errorf("publish message %s: %w", message.Type, err)
	}

	acknowledged, err := confirmation.WaitContext(ctx)
	if err != nil {
		r.dropPublisherChannel()
		return fmt.Errorf("wait for publisher confirm: %w", err)
	}
	if !acknowledged {
		return fmt.Errorf("broker rejected message %s", message.Type)
	}
	return nil
}

func (r *Rabbit) publisherChannel() (*amqp.Channel, error) {
	conn, err := r.connection()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.publishChannel != nil && !r.publishChannel.IsClosed() {
		return r.publishChannel, nil
	}

	channel, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("open publisher channel: %w", err)
	}
	if err := channel.Confirm(false); err != nil {
		_ = channel.Close()
		return nil, fmt.Errorf("enable publisher confirms: %w", err)
	}
	r.publishChannel = channel
	return channel, nil
}

func (r *Rabbit) dropPublisherChannel() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.publishChannel != nil {
		_ = r.publishChannel.Close()
		r.publishChannel = nil
	}
}

// QueueSpec describes a queue and the message types it receives.
type QueueSpec struct {
	// Name of the durable queue.
	Name string
	// RoutingKeys bound to the queue, such as "stock.debited".
	RoutingKeys []string
}

// DeclareQueue creates a durable queue, its dead letter queue and the
// bindings. Messages a consumer rejects end up in <name>.dlq for inspection
// instead of being lost or looping forever.
func (r *Rabbit) DeclareQueue(spec QueueSpec) error {
	conn, err := r.connection()
	if err != nil {
		return err
	}

	channel, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer func() { _ = channel.Close() }()

	deadLetterQueue := spec.Name + ".dlq"
	if _, err := channel.QueueDeclare(deadLetterQueue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dead letter queue: %w", err)
	}
	if err := channel.QueueBind(deadLetterQueue, spec.Name, DeadLetterExchange, false, nil); err != nil {
		return fmt.Errorf("bind dead letter queue: %w", err)
	}

	if _, err := channel.QueueDeclare(spec.Name, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange":    DeadLetterExchange,
		"x-dead-letter-routing-key": spec.Name,
	}); err != nil {
		return fmt.Errorf("declare queue %s: %w", spec.Name, err)
	}
	for _, key := range spec.RoutingKeys {
		if err := channel.QueueBind(spec.Name, key, Exchange, false, nil); err != nil {
			return fmt.Errorf("bind queue %s to %s: %w", spec.Name, key, err)
		}
	}
	return nil
}

// Handler processes one message. Returning nil acknowledges it; returning an
// error sends it to the dead letter queue, since the consumer already retried
// what was worth retrying.
type Handler func(ctx context.Context, message Message) error

// reconnectDelay is how long a consumer waits before trying the broker again.
const reconnectDelay = 2 * time.Second

// Consume processes the messages of a queue with handler until ctx is
// cancelled, reconnecting whenever the broker goes away. Messages are handled
// one at a time, so events about the same invoice never overlap.
func (r *Rabbit) Consume(ctx context.Context, spec QueueSpec, prefetch int, handler Handler) error {
	for {
		err := r.consumeOnce(ctx, spec, prefetch, handler)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			slog.WarnContext(ctx, "consumer stopped, retrying",
				"queue", spec.Name, "error", err, "retry_in", reconnectDelay)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectDelay):
		}
	}
}

func (r *Rabbit) consumeOnce(ctx context.Context, spec QueueSpec, prefetch int, handler Handler) error {
	if err := r.DeclareQueue(spec); err != nil {
		return err
	}

	conn, err := r.connection()
	if err != nil {
		return err
	}

	channel, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("open consumer channel: %w", err)
	}
	defer func() { _ = channel.Close() }()

	if prefetch <= 0 {
		prefetch = 10
	}
	if err := channel.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("set prefetch: %w", err)
	}

	deliveries, err := channel.Consume(spec.Name, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume queue %s: %w", spec.Name, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case raw, open := <-deliveries:
			if !open {
				return fmt.Errorf("consumer channel of queue %s was closed", spec.Name)
			}

			message, err := toMessage(raw)
			if err != nil {
				// A message that cannot even be read is dead lettered right
				// away: retrying would never help.
				_ = raw.Nack(false, false)
				continue
			}

			if err := handler(ctx, message); err != nil {
				_ = raw.Nack(false, false)
				continue
			}
			_ = raw.Ack(false)
		}
	}
}

func toMessage(raw amqp.Delivery) (Message, error) {
	id, err := uuid.Parse(raw.MessageId)
	if err != nil {
		return Message{}, fmt.Errorf("message id %q is not a uuid: %w", raw.MessageId, err)
	}

	messageType := raw.Type
	if messageType == "" {
		messageType = raw.RoutingKey
	}

	aggregateID, _ := raw.Headers["aggregate_id"].(string)
	return Message{
		ID:            id,
		Type:          messageType,
		AggregateID:   aggregateID,
		Payload:       raw.Body,
		OccurredAt:    raw.Timestamp,
		CorrelationID: raw.CorrelationId,
	}, nil
}

// DeadLetterSuffix is appended to a queue name to form its dead letter queue.
const DeadLetterSuffix = ".dlq"

// DeadLetterQueue is where messages a consumer gave up on end up.
func DeadLetterQueue(queue string) string { return queue + DeadLetterSuffix }

// QueueDepth reports how many messages are waiting in a queue.
//
// A dead lettered message is work the system accepted and then failed to do,
// so a depth above zero is not a curiosity: it is an invoice somewhere waiting
// for something that will never arrive on its own.
func (r *Rabbit) QueueDepth(queue string) (int, error) {
	conn, err := r.connection()
	if err != nil {
		return 0, err
	}
	channel, err := conn.Channel()
	if err != nil {
		return 0, fmt.Errorf("open channel to inspect %s: %w", queue, err)
	}
	defer func() { _ = channel.Close() }()

	// Passive: report what is there, never create it.
	state, err := channel.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		return 0, fmt.Errorf("inspect queue %s: %w", queue, err)
	}
	return state.Messages, nil
}

// Replay moves messages out of a dead letter queue and back to the exchange
// they came from, so the ordinary consumer gets another chance at them.
//
// It is deliberately manual and bounded. A message is dead lettered because
// retrying did not help, so replaying it only makes sense once somebody has
// fixed whatever it was waiting for, and only in batches small enough to watch.
// Messages keep their id, so anything that was in fact already applied is
// recognised and skipped by the consumer rather than done twice.
func (r *Rabbit) Replay(ctx context.Context, deadLetterQueue string, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}

	conn, err := r.connection()
	if err != nil {
		return 0, err
	}
	channel, err := conn.Channel()
	if err != nil {
		return 0, fmt.Errorf("open channel to replay %s: %w", deadLetterQueue, err)
	}
	defer func() { _ = channel.Close() }()

	replayed := 0
	for replayed < limit {
		if ctx.Err() != nil {
			return replayed, ctx.Err()
		}

		delivery, ok, err := channel.Get(deadLetterQueue, false)
		if err != nil {
			return replayed, fmt.Errorf("read from %s: %w", deadLetterQueue, err)
		}
		if !ok {
			break
		}

		message, err := toMessage(delivery)
		if err != nil {
			// It could not be read on the way in and will not be read now;
			// leaving it where it is keeps the evidence.
			_ = delivery.Nack(false, false)
			continue
		}

		if err := r.Publish(ctx, message); err != nil {
			// Put it back rather than lose it.
			_ = delivery.Nack(false, true)
			return replayed, fmt.Errorf("republish message %s: %w", message.ID, err)
		}
		if err := delivery.Ack(false); err != nil {
			return replayed, fmt.Errorf("acknowledge replayed message: %w", err)
		}
		replayed++
	}
	return replayed, nil
}
