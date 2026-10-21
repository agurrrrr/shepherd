package queue

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
)

func newCheckpointRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "initial")
	return dir
}

func setCheckpointEnabled(t *testing.T, enabled bool) {
	t.Helper()
	prev := viper.GetBool("task_checkpoint_enabled")
	viper.Set("task_checkpoint_enabled", enabled)
	t.Cleanup(func() { viper.Set("task_checkpoint_enabled", prev) })
}

func TestCheckpointLifecycleAcrossTerminalStates(t *testing.T) {
	cases := []struct {
		name   string
		finish func(t *testing.T, id int)
		status task.Status
	}{
		{
			name: "completed",
			finish: func(t *testing.T, id int) {
				if err := CompleteTaskWithTokens(id, "done", nil, nil, 0, 0, 0); err != nil {
					t.Fatalf("complete: %v", err)
				}
			},
			status: task.StatusCompleted,
		},
		{
			name: "failed",
			finish: func(t *testing.T, id int) {
				if err := FailTaskWithOutput(id, "boom", nil); err != nil {
					t.Fatalf("fail: %v", err)
				}
			},
			status: task.StatusFailed,
		},
		{
			name: "stopped",
			finish: func(t *testing.T, id int) {
				if err := StopTaskWithOutput(id, "cancelled", nil); err != nil {
					t.Fatalf("stop: %v", err)
				}
			},
			status: task.StatusStopped,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setCheckpointEnabled(t, true)
			client := withTestDB(t)
			ctx := context.Background()
			dir := newCheckpointRepo(t)

			created := client.Task.Create().
				SetPrompt("checkpointed work").
				SetStatus(task.StatusPending).
				SaveX(ctx)

			var lines []string
			emit := func(line string) { lines = append(lines, line) }

			started, err := beginCheckpoint(ctx, created.ID, dir, emit)
			if err != nil {
				t.Fatalf("beginCheckpoint: %v", err)
			}
			if !started {
				t.Fatal("expected a start checkpoint")
			}

			if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0644); err != nil {
				t.Fatalf("modify file: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0644); err != nil {
				t.Fatalf("add file: %v", err)
			}

			if err := finishCheckpoint(ctx, created.ID, dir, started, emit); err != nil {
				t.Fatalf("finishCheckpoint: %v", err)
			}
			tc.finish(t, created.ID)

			got := client.Task.GetX(ctx, created.ID)
			if got.Status != tc.status {
				t.Errorf("status = %s, want %s", got.Status, tc.status)
			}
			if got.CheckpointDir != dir {
				t.Errorf("checkpoint_dir = %q, want %q", got.CheckpointDir, dir)
			}
			if got.CheckpointStart == "" || got.CheckpointEnd == "" {
				t.Fatalf("start/end = %q/%q, want both set", got.CheckpointStart, got.CheckpointEnd)
			}
			if got.CheckpointStart == got.CheckpointEnd {
				t.Error("start and end snapshots should differ after changes")
			}
			if got.CheckpointError != "" {
				t.Errorf("checkpoint_error = %q, want empty", got.CheckpointError)
			}

			if !checkpoint.CommitExists(ctx, dir, got.CheckpointStart) {
				t.Fatal("start commit does not exist")
			}
			if !checkpoint.CommitExists(ctx, dir, got.CheckpointEnd) {
				t.Fatal("end commit does not exist")
			}

			refs, err := checkpoint.ListTaskRefs(ctx, dir)
			if err != nil {
				t.Fatalf("ListTaskRefs: %v", err)
			}
			if len(refs[created.ID]) != 2 {
				t.Errorf("refs for task = %v, want start and end", refs[created.ID])
			}

			changes, err := checkpoint.Changes(ctx, dir, got.CheckpointStart, got.CheckpointEnd)
			if err != nil {
				t.Fatalf("Changes: %v", err)
			}
			if len(changes) != 2 {
				t.Errorf("changes = %+v, want modified f.txt and added new.txt", changes)
			}

			if len(lines) < 2 || !strings.Contains(lines[0], "📸 checkpoint start") || !strings.Contains(lines[1], "📸 checkpoint end") {
				t.Errorf("log lines = %v, want start and end messages", lines)
			}
		})
	}
}

func TestCheckpointSkipsNonGitDirectory(t *testing.T) {
	setCheckpointEnabled(t, true)
	client := withTestDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	created := client.Task.Create().
		SetPrompt("not git").
		SetStatus(task.StatusPending).
		SaveX(ctx)

	var lines []string
	started, err := beginCheckpoint(ctx, created.ID, dir, func(line string) { lines = append(lines, line) })
	if err != nil {
		t.Fatalf("beginCheckpoint: %v", err)
	}
	if started {
		t.Fatal("non-git directory must not start a checkpoint")
	}

	if err := finishCheckpoint(ctx, created.ID, dir, started, nil); err != nil {
		t.Fatalf("finishCheckpoint: %v", err)
	}
	if err := FailTaskWithOutput(created.ID, "boom", nil); err != nil {
		t.Fatalf("fail: %v", err)
	}

	got := client.Task.GetX(ctx, created.ID)
	if got.CheckpointStart != "" || got.CheckpointEnd != "" {
		t.Errorf("start/end = %q/%q, want empty", got.CheckpointStart, got.CheckpointEnd)
	}
	if !strings.Contains(got.CheckpointError, "not a git repository") {
		t.Errorf("checkpoint_error = %q, want not a git repository", got.CheckpointError)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "not a git repository") {
		t.Errorf("log lines = %v, want a skip message", lines)
	}
}

func TestCheckpointSkipsTooLargeRepository(t *testing.T) {
	setCheckpointEnabled(t, true)
	client := withTestDB(t)
	ctx := context.Background()
	dir := newCheckpointRepo(t)

	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatalf("create large file: %v", err)
	}
	if err := f.Truncate(51 << 20); err != nil {
		f.Close()
		t.Fatalf("truncate large file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close large file: %v", err)
	}

	created := client.Task.Create().
		SetPrompt("too large").
		SetStatus(task.StatusPending).
		SaveX(ctx)

	started, err := beginCheckpoint(ctx, created.ID, dir, nil)
	if err != nil {
		t.Fatalf("beginCheckpoint: %v", err)
	}
	if started {
		t.Fatal("oversized tree must not start a checkpoint")
	}

	got := client.Task.GetX(ctx, created.ID)
	if got.CheckpointStart != "" {
		t.Errorf("checkpoint_start = %q, want empty", got.CheckpointStart)
	}
	if !strings.Contains(got.CheckpointError, "too large") {
		t.Errorf("checkpoint_error = %q, want too large", got.CheckpointError)
	}

	refs, err := checkpoint.ListTaskRefs(ctx, dir)
	if err != nil {
		t.Fatalf("ListTaskRefs: %v", err)
	}
	if len(refs[created.ID]) != 0 {
		t.Errorf("refs for task = %v, want none", refs[created.ID])
	}

	// Temporary indexes must be cleaned up even on the error path.
	matches, err := filepath.Glob(filepath.Join(dir, ".git", "shepherd-ckpt-index-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("temporary indexes left behind: %v", matches)
	}
}

func TestCheckpointDisabledWritesNothing(t *testing.T) {
	setCheckpointEnabled(t, false)
	client := withTestDB(t)
	ctx := context.Background()
	dir := newCheckpointRepo(t)

	created := client.Task.Create().
		SetPrompt("disabled").
		SetStatus(task.StatusPending).
		SaveX(ctx)

	started, err := beginCheckpoint(ctx, created.ID, dir, nil)
	if err != nil {
		t.Fatalf("beginCheckpoint: %v", err)
	}
	if started {
		t.Fatal("checkpoint must be skipped when disabled")
	}
	if err := finishCheckpoint(ctx, created.ID, dir, started, nil); err != nil {
		t.Fatalf("finishCheckpoint: %v", err)
	}

	got := client.Task.GetX(ctx, created.ID)
	if got.CheckpointDir != "" || got.CheckpointStart != "" || got.CheckpointEnd != "" || got.CheckpointError != "" {
		t.Errorf("checkpoint fields should stay empty: %+v", got)
	}

	refs, err := checkpoint.ListTaskRefs(ctx, dir)
	if err != nil {
		t.Fatalf("ListTaskRefs: %v", err)
	}
	if len(refs[created.ID]) != 0 {
		t.Errorf("refs for task = %v, want none", refs[created.ID])
	}
}

func TestCheckpointOverwritesReusedTaskRefs(t *testing.T) {
	setCheckpointEnabled(t, true)
	client := withTestDB(t)
	ctx := context.Background()
	dir := newCheckpointRepo(t)

	created := client.Task.Create().
		SetPrompt("reused id").
		SetStatus(task.StatusPending).
		SaveX(ctx)

	started, err := beginCheckpoint(ctx, created.ID, dir, nil)
	if err != nil || !started {
		t.Fatalf("first beginCheckpoint: started=%v err=%v", started, err)
	}
	firstStart := client.Task.GetX(ctx, created.ID).CheckpointStart
	if err := finishCheckpoint(ctx, created.ID, dir, started, nil); err != nil {
		t.Fatalf("first finishCheckpoint: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	started, err = beginCheckpoint(ctx, created.ID, dir, nil)
	if err != nil || !started {
		t.Fatalf("second beginCheckpoint: started=%v err=%v", started, err)
	}
	secondStart := client.Task.GetX(ctx, created.ID).CheckpointStart
	if secondStart == "" || secondStart == firstStart {
		t.Errorf("second start = %q, want a new commit (first %q)", secondStart, firstStart)
	}

	refs, err := checkpoint.ListTaskRefs(ctx, dir)
	if err != nil {
		t.Fatalf("ListTaskRefs: %v", err)
	}
	if len(refs[created.ID]) != 1 {
		t.Errorf("refs after second begin = %v, want only start", refs[created.ID])
	}
}

func TestRecoverStuckTasksRecordsMissingEndSnapshot(t *testing.T) {
	setCheckpointEnabled(t, true)
	client := withTestDB(t)
	ctx := context.Background()
	dir := newCheckpointRepo(t)

	created := client.Task.Create().
		SetPrompt("crashed").
		SetStatus(task.StatusPending).
		SaveX(ctx)
	if err := StartTask(created.ID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	started, err := beginCheckpoint(ctx, created.ID, dir, nil)
	if err != nil || !started {
		t.Fatalf("beginCheckpoint: started=%v err=%v", started, err)
	}

	if _, err := RecoverStuckTasks(); err != nil {
		t.Fatalf("RecoverStuckTasks: %v", err)
	}

	got := client.Task.GetX(ctx, created.ID)
	if got.Status != task.StatusFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if !strings.Contains(got.CheckpointError, "no end snapshot (daemon restarted)") {
		t.Errorf("checkpoint_error = %q, want missing-end message", got.CheckpointError)
	}
	if got.CheckpointEnd != "" {
		t.Errorf("checkpoint_end = %q, want empty", got.CheckpointEnd)
	}
}
