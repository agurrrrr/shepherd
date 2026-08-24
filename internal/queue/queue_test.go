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
