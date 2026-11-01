package mcp

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/queue"
)

// taskFinishFixture replays the #722 incident layout: an embedded sheep's
// running task on one project (the caller) and another project's tasks in
// every state. The finished rows carry a fixed summary, error, and
// completed_at so a test can prove a refused call left them untouched.
//
// Call it ONCE per test: withMCPTestDB's in-memory DSN is keyed by the test
// name, so a second call would collide with the first fixture's rows.
type taskFinishFixture struct {
	client    *ent.Client
	caller    int // running, 양56 / tereo
	otherRun  int // running, 겜돌이 / synagogue
	pending   int // pending, 겜돌이 / synagogue
	completed int
	failed    int
	stopped   int
	notices   chan int // task ids passed to the (stubbed) completion notifiers
}

var finishedAt = time.Date(2026, 11, 1, 11, 30, 0, 0, time.UTC)

func newTaskFinishFixture(t *testing.T) *taskFinishFixture {
	t.Helper()
	ctx := context.Background()
	client := withMCPTestDB(t)

	// Caller 0 unless a case sets it: the test process may itself run inside
	// a shepherd task and inherit SHEPHERD_TASK_ID.
	t.Setenv("SHEPHERD_TASK_ID", "")

	// Synchronous stand-ins for the Discord / Web Push notifiers: the real
	// ones query the DB from a goroutine after the handler returns.
	notices := make(chan int, 16)
	prevComplete, prevFail := announceTaskComplete, announceTaskFail
	announceTaskComplete = func(taskID int, _, _, _ string) { notices <- taskID }
	announceTaskFail = func(taskID int, _ string) { notices <- taskID }
	t.Cleanup(func() { announceTaskComplete, announceTaskFail = prevComplete, prevFail })

	a := client.Sheep.Create().SetName("양56").SetProvider(sheep.ProviderEmbedded).
		SetStatus(sheep.StatusWorking).SaveX(ctx)
	b := client.Sheep.Create().SetName("겜돌이").SetProvider(sheep.ProviderClaude).
		SetStatus(sheep.StatusWorking).SaveX(ctx)
	tereo := client.Project.Create().SetName("tereo").SetPath("/ws/tereo").SetSheep(a).SaveX(ctx)
	synagogue := client.Project.Create().SetName("synagogue").SetPath("/ws/synagogue").SetSheep(b).SaveX(ctx)

	mk := func(sh *ent.Sheep, p *ent.Project, status entTask.Status) int {
		c := client.Task.Create().SetPrompt("work").SetStatus(status).
			SetSheepID(sh.ID).SetProjectID(p.ID)
		switch status {
		case entTask.StatusCompleted, entTask.StatusFailed, entTask.StatusStopped:
			c = c.SetSummary("원래 요약").SetError("원래 오류").SetCompletedAt(finishedAt)
		}
		return c.SaveX(ctx).ID
	}

	return &taskFinishFixture{
		client:    client,
		caller:    mk(a, tereo, entTask.StatusRunning),
		otherRun:  mk(b, synagogue, entTask.StatusRunning),
		pending:   mk(b, synagogue, entTask.StatusPending),
		completed: mk(b, synagogue, entTask.StatusCompleted),
		failed:    mk(b, synagogue, entTask.StatusFailed),
		stopped:   mk(b, synagogue, entTask.StatusStopped),
		notices:   notices,
	}
}

// finishCall runs task_complete or task_error against target as caller
// (0 = no caller identity in args).
func finishCall(tool string, caller, target int) (string, error) {
	args := map[string]interface{}{"task_id": float64(target)}
	if tool == "task_complete" {
		args["summary"] = "PB-03 보고"
		return handleTaskComplete(SetCallerTaskID(args, caller))
	}
	args["error"] = "PB-03 오류"
	return handleTaskError(SetCallerTaskID(args, caller))
}

// assertUntouched fails when a refused call changed the target row.
func (f *taskFinishFixture) assertUntouched(t *testing.T, id int, wantStatus entTask.Status) {
	t.Helper()
	got := f.client.Task.GetX(context.Background(), id)
	if got.Status != wantStatus {
		t.Errorf("task #%d status = %s, want %s", id, got.Status, wantStatus)
	}
	if wantStatus == entTask.StatusPending || wantStatus == entTask.StatusRunning {
		if got.Summary != "" || !got.CompletedAt.IsZero() {
			t.Errorf("task #%d must stay unfinished: summary=%q completed_at=%v", id, got.Summary, got.CompletedAt)
		}
		return
	}
	if got.Summary != "원래 요약" || got.Error != "원래 오류" || !got.CompletedAt.Equal(finishedAt) {
		t.Errorf("task #%d was overwritten: summary=%q error=%q completed_at=%v",
			id, got.Summary, got.Error, got.CompletedAt)
	}
}

// expectNotice waits for the async notifier of a successful call.
func (f *taskFinishFixture) expectNotice(t *testing.T, want int) {
	t.Helper()
	select {
	case got := <-f.notices:
		if got != want {
			t.Errorf("notice sent for task #%d, want #%d", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("no notice sent for task #%d", want)
	}
}

// expectNoNotice: a refused call returns before the notifier goroutine
// starts, so the channel must be empty right after it.
func (f *taskFinishFixture) expectNoNotice(t *testing.T) {
	t.Helper()
	select {
	case got := <-f.notices:
		t.Errorf("refused call sent a notice for task #%d", got)
	default:
	}
}

// (a) A task_id other than the caller's own task is refused, and the error
// names the caller's id so the sheep can retry correctly. This is the #722
// incident: tereo #11420 completed synagogue's finished #11416.
func TestTaskFinishRejectsOtherTaskThanCaller(t *testing.T) {
	f := newTaskFinishFixture(t)

	for _, tool := range []string{"task_complete", "task_error"} {
		for _, tc := range []struct {
			target int
			status entTask.Status
		}{
			{f.completed, entTask.StatusCompleted},
			{f.otherRun, entTask.StatusRunning},
			{f.pending, entTask.StatusPending},
		} {
			_, err := finishCall(tool, f.caller, tc.target)
			if err == nil {
				t.Fatalf("%s(task_id=%d) from caller #%d must be refused", tool, tc.target, f.caller)
			}
			want := "작업 #" + strconv.Itoa(tc.target) + "은 이 호출의 작업(#" + strconv.Itoa(f.caller) +
				")이 아닙니다. task_id=" + strconv.Itoa(f.caller) + "을 쓰십시오"
			if err.Error() != want {
				t.Errorf("%s error = %q, want %q", tool, err.Error(), want)
			}
			f.assertUntouched(t, tc.target, tc.status)
			f.expectNoNotice(t)
		}
	}
	f.assertUntouched(t, f.caller, entTask.StatusRunning)

	// The stdio child path: no arg key, identity from SHEPHERD_TASK_ID.
	t.Setenv("SHEPHERD_TASK_ID", strconv.Itoa(f.caller))
	if _, err := finishCall("task_complete", 0, f.completed); err == nil ||
		!strings.Contains(err.Error(), "task_id="+strconv.Itoa(f.caller)) {
		t.Errorf("env-identified caller must be checked too: %v", err)
	}
	f.assertUntouched(t, f.completed, entTask.StatusCompleted)
	f.expectNoNotice(t)
}

// (b) A completed/failed/stopped target is refused even for its own caller
// (the sheep called task_complete twice), and summary, error, status, and
// completed_at stay as they were.
func TestTaskFinishRejectsFinishedTarget(t *testing.T) {
	f := newTaskFinishFixture(t)

	for _, tool := range []string{"task_complete", "task_error"} {
		for _, tc := range []struct {
			target int
			status entTask.Status
		}{
			{f.completed, entTask.StatusCompleted},
			{f.failed, entTask.StatusFailed},
			{f.stopped, entTask.StatusStopped},
		} {
			_, err := finishCall(tool, tc.target, tc.target)
			if err == nil || !strings.Contains(err.Error(), "이미 끝난 작업") ||
				!strings.Contains(err.Error(), string(tc.status)) {
				t.Errorf("%s on %s task #%d must be refused with its status, got %v", tool, tc.status, tc.target, err)
			}
			f.assertUntouched(t, tc.target, tc.status)
			f.expectNoNotice(t)
		}
	}
}

// (c) The caller's own running task completes, and the processor's final
// write (CompleteTaskWithTokens on the now-completed row) still works — the
// status guard is MCP-only.
func TestTaskCompleteOwnRunningTask(t *testing.T) {
	f := newTaskFinishFixture(t)
	ctx := context.Background()

	out, err := finishCall("task_complete", f.caller, f.caller)
	if err != nil {
		t.Fatalf("task_complete on own running task: %v", err)
	}
	if !strings.Contains(out, "#"+strconv.Itoa(f.caller)) {
		t.Errorf("result %q must name the task", out)
	}
	f.expectNotice(t, f.caller)

	got := f.client.Task.GetX(ctx, f.caller)
	if got.Status != entTask.StatusCompleted || got.Summary != "PB-03 보고" || got.CompletedAt.IsZero() {
		t.Errorf("own task not completed: status=%s summary=%q completed_at=%v", got.Status, got.Summary, got.CompletedAt)
	}

	// Worker finish path after the sheep's task_complete.
	if err := queue.CompleteTaskWithTokens(f.caller, "최종 결과", nil, []string{"line"}, 0.5, 10, 20); err != nil {
		t.Fatalf("processor final write must still succeed: %v", err)
	}
	got = f.client.Task.GetX(ctx, f.caller)
	if got.Summary != "최종 결과" || len(got.Output) != 1 || got.PromptTokens != 10 || got.CompletionTokens != 20 {
		t.Errorf("processor final write lost: summary=%q output=%v tokens=%d/%d",
			got.Summary, got.Output, got.PromptTokens, got.CompletionTokens)
	}

	// A second task_complete from the same task is now refused.
	if _, err := finishCall("task_complete", f.caller, f.caller); err == nil {
		t.Error("task_complete on an already completed own task must be refused")
	}
	f.expectNoNotice(t)

	// task_error on own running task records the failure.
	if _, err := finishCall("task_error", f.otherRun, f.otherRun); err != nil {
		t.Fatalf("task_error on own running task: %v", err)
	}
	f.expectNotice(t, f.otherRun)
	if got := f.client.Task.GetX(ctx, f.otherRun); got.Status != entTask.StatusFailed || got.Error != "PB-03 오류" {
		t.Errorf("own task not failed: status=%s error=%q", got.Status, got.Error)
	}
}

// (d) Caller 0 (CLI, user, providers that cannot pass the id) skips the
// caller check but still gets the status check.
func TestTaskFinishCallerZeroStatusOnly(t *testing.T) {
	f := newTaskFinishFixture(t)
	ctx := context.Background()

	if _, err := finishCall("task_complete", 0, f.otherRun); err != nil {
		t.Fatalf("caller 0 must complete a running task: %v", err)
	}
	f.expectNotice(t, f.otherRun)
	if got := f.client.Task.GetX(ctx, f.otherRun); got.Status != entTask.StatusCompleted {
		t.Errorf("running task status = %s, want completed", got.Status)
	}

	if _, err := finishCall("task_error", 0, f.pending); err != nil {
		t.Fatalf("caller 0 must fail a pending task: %v", err)
	}
	f.expectNotice(t, f.pending)
	if got := f.client.Task.GetX(ctx, f.pending); got.Status != entTask.StatusFailed {
		t.Errorf("pending task status = %s, want failed", got.Status)
	}

	for _, tool := range []string{"task_complete", "task_error"} {
		if _, err := finishCall(tool, 0, f.completed); err == nil || !strings.Contains(err.Error(), "이미 끝난 작업") {
			t.Errorf("caller 0 %s on a completed task must be refused, got %v", tool, err)
		}
		f.assertUntouched(t, f.completed, entTask.StatusCompleted)
		f.expectNoNotice(t)
	}

	if _, err := finishCall("task_complete", 0, 999999); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown task must report not found, got %v", err)
	}
}
