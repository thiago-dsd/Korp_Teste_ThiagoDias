// Package retention removes rows that have done their job.
//
// The tables that make this system correct are the ones that grow without
// bound: every idempotency key ever presented, every message id ever consumed,
// every event ever published. Keeping them forever costs nothing on the first
// day and slows down the claim and the deduplication insert on the thousandth,
// which is the kind of decay nobody notices until it hurts.
package retention

import (
	"context"
	"log/slog"
	"time"
)

// Task is one thing worth cleaning up. It reports how many rows it removed.
type Task struct {
	Name string
	Run  func(ctx context.Context) (int, error)
}

// Runner executes the tasks on a schedule for as long as the service runs.
type Runner struct {
	interval time.Duration
	logger   *slog.Logger
	tasks    []Task
}

// NewRunner builds a runner. An interval of zero uses one hour, which is often
// enough for tables measured in days and rare enough to be invisible.
func NewRunner(interval time.Duration, logger *slog.Logger, tasks ...Task) *Runner {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Runner{interval: interval, logger: logger, tasks: tasks}
}

// Run cleans up until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	if len(r.tasks) == 0 {
		return
	}
	r.logger.Info("retention started", "interval", r.interval, "tasks", len(r.tasks))

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("retention stopped")
			return
		case <-ticker.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce executes every task, carrying on when one of them fails: a table
// that cannot be cleaned is a reason to complain, not a reason to stop
// cleaning the others.
func (r *Runner) RunOnce(ctx context.Context) {
	for _, task := range r.tasks {
		removed, err := task.Run(ctx)
		if err != nil {
			r.logger.ErrorContext(ctx, "retention task failed", "task", task.Name, "error", err)
			continue
		}
		if removed > 0 {
			r.logger.InfoContext(ctx, "retention removed rows", "task", task.Name, "count", removed)
		}
	}
}
