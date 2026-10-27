// Package taskrevert reverts a finished task's changes in the working tree
// with a 3-way merge and undoes that revert. It is shared by the REST API and
// the CLI so both paths enforce the same guards. Only the working tree is
// modified: commits, the index, branches, and HEAD are left untouched.
package taskrevert

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/taskchanges"
)

// Error is a user-facing revert failure with an HTTP status hint.
type Error struct {
	Status    int
	Message   string
	Conflicts []string
}

func (e *Error) Error() string { return e.Message }

// Result is the outcome of a revert or undo call.
type Result struct {
	Changed    bool               `json:"changed"`
	Files      []taskchanges.File `json:"files"`
	Conflicts  []string           `json:"conflicts"`
	Pre        string             `json:"-"`
	Post       string             `json:"-"`
	RevertedAt time.Time          `json:"-"`
}

// Revert applies the reverse of the task's start-to-end change to the current
// working tree. When dryRun is true it only reports what would change.
func Revert(ctx context.Context, t *ent.Task, dryRun bool) (*Result, error) {
	if t == nil {
		return nil, &Error{Status: 404, Message: "task not found"}
	}
	if t.Status != entTask.StatusCompleted && t.Status != entTask.StatusFailed && t.Status != entTask.StatusStopped {
		return nil, &Error{Status: 400, Message: i18n.T().Revert.NotFinished}
	}
	if !t.RevertedAt.IsZero() {
		return nil, &Error{Status: 409, Message: i18n.T().Revert.AlreadyReverted}
	}
	if t.CheckpointDir == "" || t.CheckpointStart == "" || t.CheckpointEnd == "" {
		return nil, &Error{Status: 400, Message: i18n.T().Revert.NoCheckpoint}
	}
	if !checkpoint.CommitExists(ctx, t.CheckpointDir, t.CheckpointStart) ||
		!checkpoint.CommitExists(ctx, t.CheckpointDir, t.CheckpointEnd) {
		return nil, &Error{Status: 400, Message: i18n.T().Revert.SnapshotPruned}
	}
	if err := ensureNoRunningTask(ctx, t); err != nil {
		return nil, err
	}

	preRef, postRef := "", ""
	if !dryRun {
		preRef = checkpoint.TaskRef(t.ID, "pre-revert")
		postRef = checkpoint.TaskRef(t.ID, "reverted")
	}

	tr, err := checkpoint.Transplant(ctx, t.CheckpointDir,
		t.CheckpointEnd, t.CheckpointStart, preRef, postRef, dryRun)
	if err != nil {
		return nil, mapTransplantError(t.CheckpointDir, err)
	}

	result := &Result{
		Changed:   tr.Changed,
		Files:     convertFiles(tr.Files),
		Conflicts: []string{},
	}
	if tr.Pre != nil {
		result.Pre = tr.Pre.Commit
	}
	if tr.Post != nil {
		result.Post = tr.Post.Commit
	}

	if dryRun {
		return result, nil
	}
	if !tr.Changed {
		// Transplant returned before taking the post snapshot, so there is
		// nothing to undo. Drop the pre snapshot ref it created.
		_ = checkpoint.DeleteRefs(ctx, t.CheckpointDir, preRef)
		return result, nil
	}

	now := time.Now()
	if _, err := db.Client().Task.UpdateOneID(t.ID).
		SetRevertedAt(now).
		SetRevertPre(result.Pre).
		Save(ctx); err != nil {
		return nil, &Error{Status: 500, Message: fmt.Sprintf(i18n.T().Revert.SaveResultFailedFmt, err)}
	}
	result.RevertedAt = now
	return result, nil
}

// Undo restores the working tree to the state right before Revert. Changes
// made after the revert on other lines are preserved; conflicts abort without
// touching the working tree.
func Undo(ctx context.Context, t *ent.Task) (*Result, error) {
	if t == nil {
		return nil, &Error{Status: 404, Message: "task not found"}
	}
	if t.RevertedAt.IsZero() {
		return nil, &Error{Status: 400, Message: i18n.T().Revert.NotReverted}
	}
	if t.RevertPre == "" || t.CheckpointDir == "" {
		return nil, &Error{Status: 400, Message: i18n.T().Revert.MissingRevertSnapshot}
	}
	if !checkpoint.CommitExists(ctx, t.CheckpointDir, t.RevertPre) {
		return nil, &Error{Status: 400, Message: i18n.T().Revert.RevertSnapshotPruned}
	}

	base, ok := checkpoint.ResolveRef(ctx, t.CheckpointDir, checkpoint.TaskRef(t.ID, "reverted"))
	if !ok {
		return nil, &Error{Status: 409, Message: i18n.T().Revert.RevertedSnapshotMissing}
	}

	if err := ensureNoRunningTask(ctx, t); err != nil {
		return nil, err
	}

	tr, err := checkpoint.Transplant(ctx, t.CheckpointDir, base, t.RevertPre, "", "", false)
	if err != nil {
		return nil, mapTransplantError(t.CheckpointDir, err)
	}

	if _, err := db.Client().Task.UpdateOneID(t.ID).
		ClearRevertedAt().
		ClearRevertPre().
		Save(ctx); err != nil {
		return nil, &Error{Status: 500, Message: fmt.Sprintf(i18n.T().Revert.UndoSaveFailedFmt, err)}
	}
	_ = checkpoint.DeleteRefs(ctx, t.CheckpointDir,
		checkpoint.TaskRef(t.ID, "pre-revert"),
		checkpoint.TaskRef(t.ID, "reverted"))

	result := &Result{
		Changed:   tr.Changed,
		Files:     convertFiles(tr.Files),
		Conflicts: []string{},
	}
	if tr.Pre != nil {
		result.Pre = tr.Pre.Commit
	}
	if tr.Post != nil {
		result.Post = tr.Post.Commit
	}
	return result, nil
}

// ensureNoRunningTask rejects the operation when another task is running in
// the same repository. Comparing repository top levels (not project paths)
// catches subdirectory projects that share a parent repository.
func ensureNoRunningTask(ctx context.Context, t *ent.Task) error {
	top, _, err := checkpoint.Probe(ctx, t.CheckpointDir)
	if err != nil {
		return &Error{Status: 400, Message: i18n.T().Revert.NotGitRepo}
	}

	running, err := queue.ListTasksByStatus(entTask.StatusRunning)
	if err != nil {
		return &Error{Status: 500, Message: fmt.Sprintf(i18n.T().Revert.RunningCheckFailedFmt, err)}
	}
	for _, other := range running {
		if other.ID == t.ID {
			continue
		}
		dir := other.CheckpointDir
		if dir == "" && other.Edges.Project != nil {
			dir = other.Edges.Project.Path
		}
		if dir == "" {
			continue
		}
		otherTop, _, probeErr := checkpoint.Probe(ctx, dir)
		if probeErr == nil && otherTop == top {
			return &Error{
				Status:  409,
				Message: fmt.Sprintf(i18n.T().Revert.OtherTaskRunningFmt, other.ID),
			}
		}
	}
	return nil
}

func mapTransplantError(dir string, err error) error {
	var conflict *checkpoint.ConflictError
	if errors.As(err, &conflict) {
		return &Error{
			Status:    409,
			Message:   i18n.T().Revert.Conflict,
			Conflicts: conflict.Paths,
		}
	}
	return &Error{Status: 400, Message: fmt.Sprintf(i18n.T().Revert.GenericFailFmt, err)}
}

func convertFiles(changes []checkpoint.FileChange) []taskchanges.File {
	files := make([]taskchanges.File, 0, len(changes))
	for _, c := range changes {
		files = append(files, taskchanges.File{
			Path:      c.Path,
			OldPath:   c.OldPath,
			Status:    c.Status,
			Additions: c.Additions,
			Deletions: c.Deletions,
			Binary:    c.Binary,
		})
	}
	return files
}
