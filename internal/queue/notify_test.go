package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

// collectHook installs a hook that records every event and signals a channel,
// so tests can assert "exactly one event per terminal path" without sleeping.
func collectHook(t *testing.T) (<-chan IssueTaskEvent, func()) {
	t.Helper()
	ch := make(chan IssueTaskEvent, 8)
	SetIssueTaskFinishedHook(func(ev IssueTaskEvent) { ch <- ev })
	return ch, func() { SetIssueTaskFinishedHook(nil) }
}

// recvOne waits for one event and asserts no second one follows.
func recvOne(t *testing.T, ch <-chan IssueTaskEvent) IssueTaskEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the task-finished hook")
	}
	return IssueTaskEvent{}
}

func assertNoEvent(t *testing.T, ch <-chan IssueTaskEvent) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected hook event: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

// wakeFixture seeds project + sheep + watched issue + one task of the given
// status, and returns the task. Each test drives one terminal queue path.
func wakeFixture(t *testing.T, status task.Status) (*ent.Client, *ent.Task) {
	t.Helper()
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	sh := client.Sheep.Create().SetName("양순이").SetStatus(sheep.StatusIdle).SaveX(ctx)
	iss := client.Issue.Create().
		SetTitle("watched work").
		SetStatus(entIssue.StatusInProgress).
		SetShepherdWatch(true).
		SetProject(p).
		SaveX(ctx)
	tsk := client.Task.Create().
		SetPrompt("do it").
		SetStatus(status).
		SetSheepID(sh.ID).
		SetProject(p).
		SetIssueID(iss.ID).
		SaveX(ctx)
	return client, tsk
}

// TestNotifyTaskFinished_FourTerminalPaths pins #597: CompleteTaskWithTokens,
// FailTaskWithOutput, StopTaskWithOutput and CancelPendingTask each fire the
// hook exactly once with the right state.
func TestNotifyTaskFinished_FourTerminalPaths(t *testing.T) {
	tests := []struct {
		name       string
		start      task.Status
		run        func(id int) error
		wantState  string
		wantReason string
		wantUser   bool
	}{
		{
			name:  "complete",
			start: task.StatusRunning,
			run: func(id int) error {
				return CompleteTaskWithTokens(id, "all good", nil, nil, 0, 0, 0)
			},
			wantState: "completed",
		},
		{
			name:  "fail",
			start: task.StatusRunning,
			run: func(id int) error {
				return FailTaskWithOutput(id, "boom", nil)
			},
			wantState:  "failed",
			wantReason: "boom",
		},
		{
			name:  "stop by user",
			start: task.StatusRunning,
			run: func(id int) error {
				return StopTaskWithOutput(id, "stopped by user", nil)
			},
			wantState:  "stopped",
			wantReason: "stopped by user",
			wantUser:   true,
		},
		{
			name:  "question stop stays wakeable",
			start: task.StatusRunning,
			run: func(id int) error {
				return StopTaskWithOutput(id, "question: which branch?", nil)
			},
			wantState:  "stopped",
			wantReason: "question: which branch?",
		},
		{
			name:  "cancel pending",
			start: task.StatusPending,
			run: func(id int) error {
				return CancelPendingTask(id)
			},
			wantState:  "stopped",
			wantReason: "cancelled by user",
			wantUser:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, tsk := wakeFixture(t, tt.start)
			ch, cleanup := collectHook(t)
			defer cleanup()

			if err := tt.run(tsk.ID); err != nil {
				t.Fatalf("queue path failed: %v", err)
			}

			ev := recvOne(t, ch)
			assertNoEvent(t, ch)

			if ev.TaskID != tsk.ID {
				t.Errorf("event task id = %d, want %d", ev.TaskID, tsk.ID)
			}
			if ev.IssueID == 0 {
				t.Error("event carries no issue id")
			}
			if ev.Project != "bori" {
				t.Errorf("event project = %q, want bori", ev.Project)
			}
			if ev.TaskState != tt.wantState {
				t.Errorf("state = %q, want %q", ev.TaskState, tt.wantState)
			}
			if ev.StopReason != tt.wantReason {
				t.Errorf("reason = %q, want %q", ev.StopReason, tt.wantReason)
			}
			if ev.UserStop != tt.wantUser {
				t.Errorf("UserStop = %v, want %v", ev.UserStop, tt.wantUser)
			}
			if ev.SheepName != "양순이" {
				t.Errorf("sheep = %q, want 양순이", ev.SheepName)
			}
		})
	}
}

// TestNotifyTaskFinished_QuestionStopFlag pins the QuestionStop marker the
// wake rules use to keep question-stopped issues wakeable.
func TestNotifyTaskFinished_QuestionStopFlag(t *testing.T) {
	_, tsk := wakeFixture(t, task.StatusRunning)
	ch, cleanup := collectHook(t)
	defer cleanup()

	if err := StopTaskWithOutput(tsk.ID, "question: confirm the schema?", nil); err != nil {
		t.Fatalf("stop: %v", err)
	}
	ev := recvOne(t, ch)
	if !ev.QuestionStop || ev.UserStop {
		t.Fatalf("question stop must set QuestionStop without UserStop: %+v", ev)
	}
}

// TestNotifyTaskFinished_DeleteBeforeRemoval pins the DeleteTask ordering: the
// hook fires while the row still exists (delete notifies before removal).
func TestNotifyTaskFinished_DeleteBeforeRemoval(t *testing.T) {
	client, tsk := wakeFixture(t, task.StatusFailed)
	ch, cleanup := collectHook(t)
	defer cleanup()

	if err := DeleteTask(tsk.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	ev := recvOne(t, ch)
	if ev.TaskID != tsk.ID || ev.TaskState != "failed" {
		t.Fatalf("delete-path event wrong: %+v", ev)
	}

	// The row is gone now; a second notify must be silent.
	notifyTaskFinished(tsk.ID)
	assertNoEvent(t, ch)

	_ = client
}

// TestNotifyTaskFinished_NoHookIsNoop pins that CLI processes (no registered
// hook) stay silent even on terminal updates.
func TestNotifyTaskFinished_NoHookIsNoop(t *testing.T) {
	_, tsk := wakeFixture(t, task.StatusRunning)
	SetIssueTaskFinishedHook(nil)

	if err := CompleteTaskWithTokens(tsk.ID, "done", nil, nil, 0, 0, 0); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// Nothing to observe — the point is that this must not panic or block.
}

// TestNotifyTaskFinished_SkipsNonTerminal pins the terminal guard: a task that
// is still pending/running never fires the hook even if notify is called.
func TestNotifyTaskFinished_SkipsNonTerminal(t *testing.T) {
	_, tsk := wakeFixture(t, task.StatusPending)
	ch, cleanup := collectHook(t)
	defer cleanup()

	notifyTaskFinished(tsk.ID)
	assertNoEvent(t, ch)
}

// TestNotifyTaskFinished_UnlinkedTaskStaysSilent pins that tasks without an
// issue link never reach the hook.
func TestNotifyTaskFinished_UnlinkedTaskStaysSilent(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()
	sh := client.Sheep.Create().SetName("양순이").SetStatus(sheep.StatusIdle).SaveX(ctx)
	tsk := client.Task.Create().
		SetPrompt("no issue").SetStatus(task.StatusRunning).SetSheepID(sh.ID).SaveX(ctx)

	ch, cleanup := collectHook(t)
	defer cleanup()

	if err := CompleteTaskWithTokens(tsk.ID, "done", nil, nil, 0, 0, 0); err != nil {
		t.Fatalf("complete: %v", err)
	}
	assertNoEvent(t, ch)
}

// TestIsUserStopReason pins the exact user-stop markers (#597 rule 3).
func TestIsUserStopReason(t *testing.T) {
	cases := map[string]bool{
		"stopped by user":                             true,
		"cancelled by user":                           true,
		"task was cancelled":                          true,
		"cancelled: stale task from previous session": true,
		"question: which branch?":                     false,
		"interrupted due to abnormal termination":     false,
		"boom": false,
		"":     false,
	}
	for reason, want := range cases {
		if got := isUserStopReason(reason); got != want {
			t.Errorf("isUserStopReason(%q) = %v, want %v", reason, got, want)
		}
	}
}

// TestHookPanicRecovery pins that a panicking hook cannot kill the daemon —
// queue recovers it in its own goroutine.
func TestHookPanicRecovery(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	SetIssueTaskFinishedHook(func(ev IssueTaskEvent) {
		defer wg.Done()
		panic("hook exploded")
	})
	t.Cleanup(func() { SetIssueTaskFinishedHook(nil) })

	_, tsk := wakeFixture(t, task.StatusRunning)
	if err := CompleteTaskWithTokens(tsk.ID, "done", nil, nil, 0, 0, 0); err != nil {
		t.Fatalf("complete: %v", err)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("hook never ran or recovery failed")
	}

	_ = db.Client()
}
