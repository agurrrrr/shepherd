package queue

import (
	"context"

	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

// SetTaskModel persists an explicit per-task model preference on the task row.
// Persisting (rather than holding it in memory) means the worker still uses it
// after a daemon restart AND the dispatcher can count running tasks per
// (provider+model) concurrency group by reading task.Model directly. No-op for
// an empty model so we never overwrite with a blank.
func SetTaskModel(taskID int, model string) {
	if model == "" {
		return
	}
	ctx := context.Background()
	_, _ = db.Client().Task.UpdateOneID(taskID).SetModel(model).Save(ctx)
}

// GetTaskModel returns the persisted per-task model override, or empty string
// when none is set (the worker then falls back to the global config default).
func GetTaskModel(taskID int) string {
	ctx := context.Background()
	t, err := db.Client().Task.Query().Where(task.ID(taskID)).Only(ctx)
	if err != nil {
		return ""
	}
	return t.Model
}
