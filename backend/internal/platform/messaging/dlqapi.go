package messaging

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/thiagodias/korp-invoices/internal/platform/apperr"
	"github.com/thiagodias/korp-invoices/internal/platform/httpx"
)

// Inspector is what the dead letter endpoints need from the broker.
type Inspector interface {
	QueueDepth(queue string) (int, error)
	Replay(ctx context.Context, deadLetterQueue string, limit int) (int, error)
}

// DeadLetterAPI exposes what is stuck and lets an operator send it back.
//
// A dead lettered message is work the system accepted and then failed to
// finish, and until now the only way to know it existed was to open the broker
// UI. It is served on the internal routes: it is an operations tool, protected
// by the service token, and never throttled.
type DeadLetterAPI struct {
	broker Inspector
	queues []string
	logger *slog.Logger
}

// NewDeadLetterAPI builds the endpoints for the queues this service consumes.
func NewDeadLetterAPI(broker Inspector, logger *slog.Logger, queues ...string) *DeadLetterAPI {
	return &DeadLetterAPI{broker: broker, queues: queues, logger: logger}
}

// Routes registers the endpoints on the internal mux.
func (a *DeadLetterAPI) Routes(mux *http.ServeMux, serviceToken string) {
	guard := httpx.RequireServiceToken(serviceToken)
	mux.Handle("GET /internal/dead-letters", guard(http.HandlerFunc(a.list)))
	mux.Handle("POST /internal/dead-letters/replay", guard(http.HandlerFunc(a.replay)))
}

type deadLetterQueueState struct {
	Queue    string `json:"queue"`
	Messages int    `json:"messages"`
	Error    string `json:"error,omitempty"`
}

type deadLetterListResponse struct {
	Queues []deadLetterQueueState `json:"queues"`
	// Total is the number a monitor would alert on.
	Total int `json:"total"`
}

// list reports how much is stuck, per queue.
func (a *DeadLetterAPI) list(w http.ResponseWriter, r *http.Request) {
	response := deadLetterListResponse{Queues: make([]deadLetterQueueState, 0, len(a.queues))}

	for _, queue := range a.queues {
		name := DeadLetterQueue(queue)
		depth, err := a.broker.QueueDepth(name)
		if err != nil {
			// One unreachable queue must not hide the others.
			response.Queues = append(response.Queues, deadLetterQueueState{
				Queue: name, Error: "could not be inspected",
			})
			a.logger.ErrorContext(r.Context(), "failed to inspect dead letter queue",
				"queue", name, "error", err)
			continue
		}
		response.Queues = append(response.Queues, deadLetterQueueState{Queue: name, Messages: depth})
		response.Total += depth
	}
	httpx.WriteJSON(w, r, http.StatusOK, response)
}

type replayRequest struct {
	// Queue is the ordinary queue name; the dead letter one is derived from it.
	Queue string `json:"queue"`
	// Limit bounds how many messages are sent back in one go.
	Limit int `json:"limit"`
}

type replayResponse struct {
	Queue    string `json:"queue"`
	Replayed int    `json:"replayed"`
}

// replay sends dead lettered messages back to the exchange they came from.
func (a *DeadLetterAPI) replay(w http.ResponseWriter, r *http.Request) {
	var request replayRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}

	if !a.knows(request.Queue) {
		httpx.WriteError(w, r, apperr.Invalid("unknown_queue",
			"This service does not consume that queue.").
			WithDetails(map[string]string{"queue": "must be one of the queues this service consumes"}))
		return
	}

	name := DeadLetterQueue(request.Queue)
	replayed, err := a.broker.Replay(r.Context(), name, request.Limit)
	if err != nil {
		a.logger.ErrorContext(r.Context(), "failed to replay dead letters",
			"queue", name, "replayed", replayed, "error", err)
		httpx.WriteError(w, r, apperr.Unavailable("replay_failed",
			"The messages could not be sent back right now.").WithCause(err))
		return
	}

	a.logger.InfoContext(r.Context(), "replayed dead letters", "queue", name, "count", replayed)
	httpx.WriteJSON(w, r, http.StatusOK, replayResponse{Queue: name, Replayed: replayed})
}

// knows reports whether the queue is one this service consumes, so an operator
// cannot ask a service to replay somebody else's messages.
func (a *DeadLetterAPI) knows(queue string) bool {
	for _, known := range a.queues {
		if known == queue {
			return true
		}
	}
	return false
}
