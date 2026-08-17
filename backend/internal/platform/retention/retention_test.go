package retention_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/retention"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunOnceExecutesEveryTask(t *testing.T) {
	first, second := 0, 0

	runner := retention.NewRunner(0, discardLogger(),
		retention.Task{Name: "first", Run: func(context.Context) (int, error) { first++; return 3, nil }},
		retention.Task{Name: "second", Run: func(context.Context) (int, error) { second++; return 0, nil }},
	)
	runner.RunOnce(context.Background())

	if first != 1 || second != 1 {
		t.Errorf("tasks ran %d and %d times, want 1 each", first, second)
	}
}

// One table that cannot be cleaned is a reason to complain, not a reason to
// leave the other tables growing.
func TestOneFailingTaskDoesNotStopTheOthers(t *testing.T) {
	cleaned := false

	runner := retention.NewRunner(0, discardLogger(),
		retention.Task{Name: "broken", Run: func(context.Context) (int, error) { return 0, errors.New("boom") }},
		retention.Task{Name: "healthy", Run: func(context.Context) (int, error) { cleaned = true; return 1, nil }},
	)
	runner.RunOnce(context.Background())

	if !cleaned {
		t.Error("the healthy task was skipped after an earlier one failed")
	}
}
