package queue

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

func newQueueTestClient(t *testing.T) *ent.Client {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", sqlDB)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func withTestDB(t *testing.T) *ent.Client {
	t.Helper()
	client := newQueueTestClient(t)
	prev := db.ReplaceClient(client)
	t.Cleanup(func() { db.ReplaceClient(prev) })
	return client
}

func TestStatusToKorean(t *testing.T) {
	tests := []struct {
		status   task.Status
		expected string
	}{
		{task.StatusPending, "pending"},
		{task.StatusRunning, "running"},
		{task.StatusCompleted, "completed"},
		{task.StatusFailed, "failed"},
		{task.Status("unknown"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := StatusToKorean(tt.status)
			if result != tt.expected {
				t.Errorf("StatusToKorean(%q) = %q, want %q", tt.status, result, tt.expected)
			}
		})
	}
}

func TestCancelPendingTask_MarksStopped(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	created := client.Task.Create().
		SetPrompt("queued work").
		SetStatus(task.StatusPending).
		SaveX(ctx)

	if err := CancelPendingTask(created.ID); err != nil {
		t.Fatalf("CancelPendingTask: %v", err)
	}

	got := client.Task.GetX(ctx, created.ID)
	if got.Status != task.StatusStopped {
		t.Errorf("status = %s, want stopped", got.Status)
	}
	if got.Error != "cancelled by user" {
		t.Errorf("error = %q, want cancelled by user", got.Error)
	}
	if got.CompletedAt.IsZero() {
		t.Error("completed_at should be set")
	}
}

func TestCancelPendingTask_RejectsNonPending(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		status task.Status
	}{
		{"running", task.StatusRunning},
		{"completed", task.StatusCompleted},
		{"failed", task.StatusFailed},
		{"stopped", task.StatusStopped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := client.Task.Create().
				SetPrompt("not pending").
				SetStatus(tt.status).
				SaveX(ctx)

			err := CancelPendingTask(created.ID)
			if err == nil {
				t.Fatal("expected error for non-pending task")
			}
			if !strings.Contains(err.Error(), "not pending") {
				t.Errorf("error = %q, want it to mention not pending", err)
			}

			got := client.Task.GetX(ctx, created.ID)
			if got.Status != tt.status {
				t.Errorf("status changed to %s, want %s", got.Status, tt.status)
			}
		})
	}
}

func TestCancelPendingTask_MissingID(t *testing.T) {
	withTestDB(t)

	err := CancelPendingTask(999999)
	if err == nil {
		t.Fatal("expected error for missing task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want not found", err)
	}
}

func TestCancelPendingTask_FailsLinkedIssue(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().
		SetName("demo").
		SetPath("/tmp/demo").
		SaveX(ctx)
	iss := client.Issue.Create().
		SetTitle("linked").
		SetProjectID(p.ID).
		SetStatus(entIssue.StatusInProgress).
		SaveX(ctx)
	created := client.Task.Create().
		SetPrompt("from issue").
		SetStatus(task.StatusPending).
		SetIssueID(iss.ID).
		SaveX(ctx)

	if err := CancelPendingTask(created.ID); err != nil {
		t.Fatalf("CancelPendingTask: %v", err)
	}

	got := client.Issue.GetX(ctx, iss.ID)
	if got.Status != entIssue.StatusFailed {
		t.Errorf("issue status = %s, want failed", got.Status)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("issue completed_at should be set")
	}
}

// TestCancelPendingTask_KeepsIssueWhileSiblingActive pins the #626 guard: one
// issue with two queued duplicates. Cancelling the first must NOT move the
// issue to failed — the surviving task still has to be able to report success.
// Only the last live cancellation transitions the issue.
func TestCancelPendingTask_KeepsIssueWhileSiblingActive(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().SetName("demo").SetPath("/tmp/demo").SaveX(ctx)
	iss := client.Issue.Create().
		SetTitle("duplicate queue").
		SetProjectID(p.ID).
		SetStatus(entIssue.StatusInProgress).
		SaveX(ctx)
	first := client.Task.Create().
		SetPrompt("duplicate to drop").
		SetStatus(task.StatusPending).
		SetIssueID(iss.ID).
		SaveX(ctx)
	second := client.Task.Create().
		SetPrompt("the one that will run").
		SetStatus(task.StatusPending).
		SetIssueID(iss.ID).
		SaveX(ctx)

	if err := CancelPendingTask(first.ID); err != nil {
		t.Fatalf("CancelPendingTask(first): %v", err)
	}

	got := client.Issue.GetX(ctx, iss.ID)
	if got.Status != entIssue.StatusInProgress {
		t.Errorf("issue status after cancelling one of two = %s, want in_progress", got.Status)
	}
	if got.CompletedAt != nil && !got.CompletedAt.IsZero() {
		t.Error("completed_at must stay unset while a live task remains — setting it wedges the issue at failed")
	}

	if err := CancelPendingTask(second.ID); err != nil {
		t.Fatalf("CancelPendingTask(second): %v", err)
	}

	got = client.Issue.GetX(ctx, iss.ID)
	if got.Status != entIssue.StatusFailed {
		t.Errorf("issue status after cancelling the last live task = %s, want failed", got.Status)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("completed_at should be set once no pending/running task remains")
	}
}

// TestCancelPendingTask_RunningSiblingKeepsIssue shows the running sibling
// counts as live work too (#626): the guard mirrors internal/issue.hasActiveTask.
func TestCancelPendingTask_RunningSiblingKeepsIssue(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().SetName("demo").SetPath("/tmp/demo").SaveX(ctx)
	iss := client.Issue.Create().
		SetTitle("one running, one queued").
		SetProjectID(p.ID).
		SetStatus(entIssue.StatusInProgress).
		SaveX(ctx)
	queued := client.Task.Create().
		SetPrompt("queued duplicate").
		SetStatus(task.StatusPending).
		SetIssueID(iss.ID).
		SaveX(ctx)
	client.Task.Create().
		SetPrompt("already running").
		SetStatus(task.StatusRunning).
		SetIssueID(iss.ID).
		SaveX(ctx)

	if err := CancelPendingTask(queued.ID); err != nil {
		t.Fatalf("CancelPendingTask: %v", err)
	}

	got := client.Issue.GetX(ctx, iss.ID)
	if got.Status != entIssue.StatusInProgress {
		t.Errorf("issue status = %s, want in_progress while a running task remains", got.Status)
	}
}

// TestCancelPendingTasks_BulkKeepsIssueUntilLastLive covers the bulk path:
// CancelPendingTasks → syncIssuesFailedForTasks must apply the same per-issue
// guard, so the issue lands at failed only after its last queued row is gone.
func TestCancelPendingTasks_BulkKeepsIssueUntilLastLive(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().SetName("demo").SetPath("/tmp/demo").SaveX(ctx)
	iss := client.Issue.Create().
		SetTitle("bulk duplicates").
		SetProjectID(p.ID).
		SetStatus(entIssue.StatusInProgress).
		SaveX(ctx)
	for i := 0; i < 2; i++ {
		client.Task.Create().
			SetPrompt("queued duplicate").
			SetStatus(task.StatusPending).
			SetIssueID(iss.ID).
			SaveX(ctx)
	}

	count, err := CancelPendingTasks()
	if err != nil {
		t.Fatalf("CancelPendingTasks: %v", err)
	}
	if count != 2 {
		t.Fatalf("cancelled = %d, want 2", count)
	}

	got := client.Issue.GetX(ctx, iss.ID)
	if got.Status != entIssue.StatusFailed {
		t.Errorf("issue status after the last live task was cancelled = %s, want failed", got.Status)
	}

	// A live sibling outside the bulk set keeps the issue untouched.
	iss2 := client.Issue.Create().
		SetTitle("sibling survives").
		SetProjectID(p.ID).
		SetStatus(entIssue.StatusTesting).
		SaveX(ctx)
	client.Task.Create().SetPrompt("queued").SetStatus(task.StatusPending).SetIssueID(iss2.ID).SaveX(ctx)
	client.Task.Create().SetPrompt("running").SetStatus(task.StatusRunning).SetIssueID(iss2.ID).SaveX(ctx)

	if _, err := CancelPendingTasks(); err != nil {
		t.Fatalf("CancelPendingTasks(second issue): %v", err)
	}
	if got := client.Issue.GetX(ctx, iss2.ID); got.Status != entIssue.StatusTesting {
		t.Errorf("issue with a surviving running task = %s, want testing", got.Status)
	}
}

func TestDeleteTask_RemovesRow(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	created := client.Task.Create().
		SetPrompt("failed work").
		SetStatus(task.StatusFailed).
		SaveX(ctx)

	if err := DeleteTask(created.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	var getErr error
	if _, getErr = client.Task.Get(ctx, created.ID); getErr == nil {
		t.Fatal("expected not found error after delete")
	}
	if !ent.IsNotFound(getErr) {
		t.Fatalf("error = %v, want not found", getErr)
	}
}

func TestDeleteTask_MissingID(t *testing.T) {
	withTestDB(t)

	err := DeleteTask(999999)
	if err == nil {
		t.Fatal("expected error for missing task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want not found", err)
	}
}

func TestDeleteTask_FailsLinkedIssue(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().
		SetName("demo").
		SetPath("/tmp/demo").
		SaveX(ctx)
	iss := client.Issue.Create().
		SetTitle("linked").
		SetProjectID(p.ID).
		SetStatus(entIssue.StatusInProgress).
		SaveX(ctx)
	created := client.Task.Create().
		SetPrompt("from issue").
		SetStatus(task.StatusFailed).
		SetIssueID(iss.ID).
		SaveX(ctx)

	if err := DeleteTask(created.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	got := client.Issue.GetX(ctx, iss.ID)
	if got.Status != entIssue.StatusFailed {
		t.Errorf("issue status = %s, want failed", got.Status)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("issue completed_at should be set")
	}
}

// TestDeleteTask_KeepsIssueWhileSiblingActive applies the #626 guard to the
// delete path: removing one queued duplicate must not fail an issue that still
// has a live sibling.
func TestDeleteTask_KeepsIssueWhileSiblingActive(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().SetName("demo").SetPath("/tmp/demo").SaveX(ctx)
	iss := client.Issue.Create().
		SetTitle("delete one of two").
		SetProjectID(p.ID).
		SetStatus(entIssue.StatusInProgress).
		SaveX(ctx)
	gone := client.Task.Create().
		SetPrompt("duplicate to delete").
		SetStatus(task.StatusPending).
		SetIssueID(iss.ID).
		SaveX(ctx)
	client.Task.Create().
		SetPrompt("sibling stays queued").
		SetStatus(task.StatusPending).
		SetIssueID(iss.ID).
		SaveX(ctx)

	if err := DeleteTask(gone.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	got := client.Issue.GetX(ctx, iss.ID)
	if got.Status != entIssue.StatusInProgress {
		t.Errorf("issue status = %s, want in_progress while a queued sibling remains", got.Status)
	}
}

func TestCreateFollowUpTask_PersistsModelAndPriority(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().
		SetName("demo").
		SetPath("/tmp/demo").
		SaveX(ctx)
	s := client.Sheep.Create().
		SetName("hamzzi").
		SaveX(ctx)

	got, err := CreateFollowUpTask("continue the work", s.ID, p.ID, 2, "qwen-coder")
	if err != nil {
		t.Fatalf("CreateFollowUpTask: %v", err)
	}
	if got.Priority != 1 {
		t.Errorf("priority = %d, want 1", got.Priority)
	}
	if got.HandoffDepth != 2 {
		t.Errorf("handoff_depth = %d, want 2", got.HandoffDepth)
	}
	if got.Model != "qwen-coder" {
		t.Errorf("model = %q, want qwen-coder", got.Model)
	}
	if got.Status != task.StatusPending {
		t.Errorf("status = %s, want pending", got.Status)
	}

	reloaded := client.Task.GetX(ctx, got.ID)
	if reloaded.Model != "qwen-coder" {
		t.Errorf("persisted model = %q, want qwen-coder", reloaded.Model)
	}
}

func TestCreateFollowUpTask_EmptyModelLeftUnset(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	s := client.Sheep.Create().
		SetName("hamzzi").
		SaveX(ctx)

	got, err := CreateFollowUpTask("continue", s.ID, 0, 1, "")
	if err != nil {
		t.Fatalf("CreateFollowUpTask: %v", err)
	}
	if got.Model != "" {
		t.Errorf("model = %q, want empty", got.Model)
	}
	reloaded := client.Task.GetX(ctx, got.ID)
	if reloaded.Model != "" {
		t.Errorf("persisted model = %q, want empty", reloaded.Model)
	}
}

func TestCountByStatusGroupsInOneQuery(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	seed := []task.Status{
		task.StatusPending,
		task.StatusPending,
		task.StatusRunning,
		task.StatusCompleted,
		task.StatusFailed,
		task.StatusStopped,
	}
	for _, status := range seed {
		if _, err := client.Task.Create().
			SetPrompt("probe").
			SetStatus(status).
			Save(ctx); err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}

	counts, err := CountByStatus()
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}

	want := map[task.Status]int{
		task.StatusPending:   2,
		task.StatusRunning:   1,
		task.StatusCompleted: 1,
		task.StatusFailed:    1,
		task.StatusStopped:   1,
	}
	for status, n := range want {
		if counts[status] != n {
			t.Errorf("counts[%s] = %d, want %d", status, counts[status], n)
		}
	}
}

func TestListTasksByProjectSelectsHistoryFields(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().SetName("demo").SetPath("/tmp/demo").SaveX(ctx)
	s := client.Sheep.Create().SetName("hamzzi").SaveX(ctx)
	if _, err := client.Task.Create().
		SetPrompt("do work").
		SetSummary("done").
		SetStatus(task.StatusCompleted).
		SetOutput([]string{"large output that history does not need"}).
		SetSheepID(s.ID).
		SetProjectID(p.ID).
		Save(ctx); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	tasks, err := ListTasksByProject("demo")
	if err != nil {
		t.Fatalf("ListTasksByProject: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	got := tasks[0]
	if got.Prompt != "do work" || got.Summary != "done" || got.Status != task.StatusCompleted {
		t.Errorf("history fields = prompt %q summary %q status %q", got.Prompt, got.Summary, got.Status)
	}
	if got.Edges.Sheep == nil || got.Edges.Sheep.Name != "hamzzi" {
		t.Errorf("sheep edge = %+v, want hamzzi", got.Edges.Sheep)
	}
}
