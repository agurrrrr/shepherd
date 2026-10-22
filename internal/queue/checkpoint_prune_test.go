package queue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/db"
)

func taskCreate(repo string) *ent.TaskCreate {
	return db.Client().Task.Create().SetCheckpointDir(repo)
}

func takeTaskRef(t *testing.T, repo string, id int) {
	t.Helper()
	if _, err := checkpoint.Take(context.Background(), repo,
		checkpoint.TaskRef(id, "start"),
		fmt.Sprintf("task %d start", id)); err != nil {
		t.Fatalf("take ref for task %d: %v", id, err)
	}
	if _, err := checkpoint.Take(context.Background(), repo,
		checkpoint.TaskRef(id, "end"),
		fmt.Sprintf("task %d end", id)); err != nil {
		t.Fatalf("take end ref for task %d: %v", id, err)
	}
}

func taskRefIDs(t *testing.T, repo string) map[int]bool {
	t.Helper()
	refs, err := checkpoint.ListTaskRefs(context.Background(), repo)
	if err != nil {
		t.Fatalf("list refs: %v", err)
	}
	out := make(map[int]bool, len(refs))
	for id := range refs {
		out[id] = true
	}
	return out
}

func TestPruneTaskCheckpointsKeepsRecentAndReverted(t *testing.T) {
	withTestDB(t)
	repo := newCheckpointRepo(t)
	ctx := context.Background()
	now := time.Now()

	create := func(id int, completed, reverted time.Time) {
		creator := taskCreate(repo).
			SetPrompt(fmt.Sprintf("task %d", id)).
			SetStatus(task.StatusCompleted)
		if !completed.IsZero() {
			creator = creator.SetCompletedAt(completed)
		}
		if !reverted.IsZero() {
			creator = creator.SetRevertedAt(reverted)
		}
		creator.SaveX(ctx)
	}

	// Old completed task: prune.
	create(1, now.AddDate(0, 0, -40), time.Time{})
	// Recent completed task: keep.
	create(2, now.AddDate(0, 0, -1), time.Time{})
	// Old task reverted recently: keep (reverted_at resets the clock).
	create(3, now.AddDate(0, 0, -100), now.AddDate(0, 0, -1))
	// Old task reverted long ago: prune.
	create(6, now.AddDate(0, 0, -100), now.AddDate(0, 0, -40))
	// Task still running (no completed_at): keep.
	taskCreate(repo).
		SetPrompt("still running").
		SetStatus(task.StatusRunning).
		SaveX(ctx)

	for _, id := range []int{1, 2, 3, 6, 4} {
		takeTaskRef(t, repo, id)
	}

	deleted, err := PruneTaskCheckpoints(ctx, 30)
	if err != nil {
		t.Fatalf("PruneTaskCheckpoints: %v", err)
	}
	if deleted != 6 {
		t.Fatalf("deleted %d refs, want 6", deleted)
	}

	remaining := taskRefIDs(t, repo)
	for _, id := range []int{2, 3} {
		if !remaining[id] {
			t.Errorf("task %d refs should be kept", id)
		}
	}
	for _, id := range []int{1, 4, 6} {
		if remaining[id] {
			t.Errorf("task %d refs should be pruned", id)
		}
	}
}

func TestPruneTaskCheckpointsDisabledWhenRetentionIsZero(t *testing.T) {
	withTestDB(t)
	repo := newCheckpointRepo(t)
	ctx := context.Background()

	taskCreate(repo).
		SetPrompt("old").
		SetStatus(task.StatusCompleted).
		SetCompletedAt(time.Now().AddDate(0, 0, -400)).
		SaveX(ctx)
	takeTaskRef(t, repo, 1)

	deleted, err := PruneTaskCheckpoints(ctx, 0)
	if err != nil {
		t.Fatalf("PruneTaskCheckpoints: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted %d refs, want 0", deleted)
	}
	if !taskRefIDs(t, repo)[1] {
		t.Error("refs should be kept when retention is 0")
	}
}

func TestDeleteTaskRemovesCheckpointRefs(t *testing.T) {
	client := withTestDB(t)
	repo := newCheckpointRepo(t)
	ctx := context.Background()

	created := taskCreate(repo).
		SetPrompt("delete me").
		SetStatus(task.StatusFailed).
		SetCompletedAt(time.Now()).
		SaveX(ctx)
	takeTaskRef(t, repo, created.ID)

	if err := DeleteTask(created.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if _, err := client.Task.Get(ctx, created.ID); err == nil {
		t.Fatal("task row should be deleted")
	}
	if taskRefIDs(t, repo)[created.ID] {
		t.Fatal("checkpoint refs should be deleted with the task")
	}
}
