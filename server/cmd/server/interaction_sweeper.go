package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
)

func runInteractionSweeper(ctx context.Context, tasks *service.TaskService) {
	runPeriodicSweep(ctx, 30*time.Second, func() {
		if _, err := tasks.ExpireTaskInteractions(ctx); err != nil {
			slog.Warn("expire task interactions", "error", err)
		}
		if err := tasks.ReconcileInteractionSuccessors(ctx); err != nil {
			slog.Warn("reconcile task interaction successors", "error", err)
		}
	})
}
