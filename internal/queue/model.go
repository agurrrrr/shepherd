package queue

import (
	"context"
	"log"

	"entgo.io/ent/dialect/sql"
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

// SetTaskProvider persists an explicit per-task provider override on the task
// row (#675). Rate-limit handoff follow-ups use it so the continuation runs on
// the configured handoff provider instead of the sheep's own provider.
// Persisting on the row means the override survives daemon restarts and never
// touches the sheep record, so concurrent tasks on the same sheep keep their
// own provider. No-op for an empty provider.
func SetTaskProvider(taskID int, provider string) {
	if provider == "" {
		return
	}
	ctx := context.Background()
	_, _ = db.Client().Task.UpdateOneID(taskID).SetProviderOverride(provider).Save(ctx)
}

// GetTaskProvider returns the persisted per-task provider override, or empty
// string when none is set (the worker then falls back to the sheep's provider).
// Select keeps the query off the huge prompt/output columns.
func GetTaskProvider(taskID int) string {
	ctx := context.Background()
	t, err := db.Client().Task.Query().
		Where(task.ID(taskID)).
		Select(task.FieldProviderOverride).
		Only(ctx)
	if err != nil {
		return ""
	}
	return t.ProviderOverride
}

// GetTaskShepherdKind returns the persisted shepherd_kind of a task (#594).
// Empty string means the task is not a shepherd (목자) task.
func GetTaskShepherdKind(taskID int) string {
	ctx := context.Background()
	t, err := db.Client().Task.Query().Where(task.ID(taskID)).Only(ctx)
	if err != nil {
		return ""
	}
	return t.ShepherdKind
}

// GetTaskHandoffDepth returns the persisted handoff_depth of a task (#675).
// Zero means the task is original work, not a handoff continuation. A failed
// lookup also reads zero; the caller then treats the task as original, which
// can only ever allow one extra handoff hop — never a chain, because the
// follow-up itself carries depth > 0.
func GetTaskHandoffDepth(taskID int) int {
	ctx := context.Background()
	t, err := db.Client().Task.Query().
		Where(task.ID(taskID)).
		Select(task.FieldHandoffDepth).
		Only(ctx)
	if err != nil {
		return 0
	}
	return t.HandoffDepth
}

// AppendToPendingReviewTaskPrompt coalesces one wake event into the newest
// pending shepherd review task (#597): the block is appended to its prompt in
// SQL (`prompt = prompt || ?`) so the read-modify-write never races with other
// writers and the huge output column is never loaded. The UPDATE re-checks
// status='pending' in its WHERE clause: if the processor claimed the row
// between the lookup and the UPDATE, 0 rows change and this returns false, so
// the caller creates a fresh review task instead of appending to a running one.
func AppendToPendingReviewTaskPrompt(block string) bool {
	ctx := context.Background()

	// Newest pending review task. Select only the id — loading prompt/output
	// here would pull potentially large columns just to discard them.
	ids, err := db.Client().Task.Query().
		Where(
			task.ShepherdKindEQ("review"),
			task.StatusEQ(task.StatusPending),
		).
		Order(task.ByCreatedAt(sql.OrderDesc())).
		Limit(1).
		IDs(ctx)
	if err != nil || len(ids) == 0 {
		return false
	}

	rawDB := db.RawDB()
	if rawDB == nil {
		return false
	}
	res, err := rawDB.ExecContext(ctx,
		"UPDATE tasks SET prompt = prompt || ? WHERE id = ? AND status = 'pending'",
		"\n\n"+block, ids[0])
	if err != nil {
		log.Printf("[queue] append to pending review task #%d: %v", ids[0], err)
		return false
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false
	}
	return n == 1
}

// ResolveFollowUpModel picks the model a context-handoff continuation should
// persist. Prefer the parent task's explicit override (endpoint ID / unique
// label / unique model for embedded). If the parent ran on the provider
// default, pin fallback — typically the endpoint ID that actually served the
// parent — so the follow-up does not silently switch to whatever is globally
// active when it starts.
func ResolveFollowUpModel(parentOverride, fallback string) string {
	if parentOverride != "" {
		return parentOverride
	}
	return fallback
}
