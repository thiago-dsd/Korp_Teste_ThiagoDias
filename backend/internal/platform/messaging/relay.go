package messaging

import (
	"context"
	"log/slog"
	"time"
)

// Publisher sends a message to the broker.
type Publisher interface {
	Publish(ctx context.Context, message Message) error
}

// RelayOptions tunes the relay loop.
type RelayOptions struct {
	// BatchSize is how many messages are claimed per round.
	BatchSize int
	// Interval is how long the relay waits when there is nothing to publish.
	Interval time.Duration
}

// DefaultRelayOptions returns settings suited to a small deployment.
func DefaultRelayOptions() RelayOptions {
	return RelayOptions{BatchSize: 50, Interval: time.Second}
}

// Relay moves messages from the outbox to the broker. It runs in the service
// process and keeps trying: a broker outage delays events, it never loses them.
type Relay struct {
	outbox    *Outbox
	publisher Publisher
	logger    *slog.Logger
	options   RelayOptions
}

// NewRelay builds a relay for the given outbox and publisher.
func NewRelay(outbox *Outbox, publisher Publisher, logger *slog.Logger, options RelayOptions) *Relay {
	if options.BatchSize <= 0 {
		options.BatchSize = 50
	}
	if options.Interval <= 0 {
		options.Interval = time.Second
	}
	return &Relay{outbox: outbox, publisher: publisher, logger: logger, options: options}
}

// Run publishes pending messages until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) error {
	r.logger.Info("outbox relay started", "batch_size", r.options.BatchSize, "interval", r.options.Interval)

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopped")
			return nil
		case <-timer.C:
		}

		published, err := r.RunOnce(ctx)
		if err != nil {
			r.logger.ErrorContext(ctx, "outbox relay round failed", "error", err)
		}

		// A full batch probably means there is more waiting, so the next round
		// starts immediately.
		wait := r.options.Interval
		if published >= r.options.BatchSize {
			wait = 0
		}
		timer.Reset(wait)
	}
}

// RunOnce claims a batch and publishes it, returning how many messages were
// published. Failures are recorded so the message is retried later.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	messages, err := r.outbox.Claim(ctx, r.options.BatchSize)
	if err != nil {
		return 0, err
	}

	published := 0
	for _, message := range messages {
		if err := r.publisher.Publish(ctx, message); err != nil {
			r.logger.WarnContext(ctx, "failed to publish outbox message",
				"message_id", message.ID,
				"type", message.Type,
				"attempts", message.Attempts,
				"error", err,
			)
			if markErr := r.outbox.MarkFailed(context.WithoutCancel(ctx), message.ID, message.Attempts, err); markErr != nil {
				r.logger.ErrorContext(ctx, "failed to record publishing failure", "error", markErr)
			}
			continue
		}
		if err := r.outbox.MarkPublished(context.WithoutCancel(ctx), message.ID); err != nil {
			// The message reached the broker; failing to record it only means
			// it may be published again, which consumers deduplicate.
			r.logger.ErrorContext(ctx, "failed to mark message as published",
				"message_id", message.ID, "error", err)
			continue
		}
		published++
	}
	return published, nil
}
