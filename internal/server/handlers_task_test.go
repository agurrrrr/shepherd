package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

func withServerTestDB(t *testing.T) {
	t.Helper()
	client := newTestClient(t)
	prev := db.ReplaceClient(client)
	t.Cleanup(func() { db.ReplaceClient(prev) })
}

func retryApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Post("/api/tasks/:id/retry", s.handleRetryTask)
	app.Post("/api/tasks/:id/retry-from", s.handleRetryFromTask)
	return app
}

// TestRetryFromInheritsIssueLink (#627) reproduces the 2026-10-24 incident:
// bulk retry-from created six unlinked copies (#11000~#11005) within ~18ms,
// bypassing both the hasActiveTask guard and the cascade delay, and the copies
// lost the issue link so issue status and results diverged. The copy must now
// inherit the issue link, and an issue that already has a live task must not
// receive a second one through this path either.
func TestRetryFromInheritsIssueLink(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	app := retryApp(t)

	client := db.Client()
	sh := client.Sheep.Create().SetName("retry-sheep").SaveX(ctx)
	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SetSheep(sh).SaveX(ctx)
	iss := client.Issue.Create().SetTitle("queued once").SetProject(proj).SaveX(ctx)

	failed := client.Task.Create().
		SetPrompt("[이슈 #627 repro] queued once").
		SetStatus(entTask.StatusFailed).
		SetSheep(sh).SetProject(proj).SetIssueID(iss.ID).
		SaveX(ctx)

	code, out := postJSON(t, app, "/api/tasks/"+strconv.Itoa(failed.ID)+"/retry-from", nil)
	if code != http.StatusOK {
		t.Fatalf("retry-from: %d %v", code, out)
	}
	created, _ := out["data"].(map[string]any)["created_tasks"].([]any)
	if len(created) != 1 {
		t.Fatalf("created_tasks = %v, want exactly one copy", out["data"])
	}
	copyID := int(created[0].(float64))

	row := client.Task.Query().Where(entTask.ID(copyID)).WithIssue().OnlyX(ctx)
	if row.Edges.Issue == nil || row.Edges.Issue.ID != iss.ID {
		t.Errorf("retry copy issue link = %v, want issue #%d inherited", row.Edges.Issue, iss.ID)
	}

	// Second retry-from: the original is still failed so it is matched again,
	// but the issue already has the pending copy — the path must skip it and
	// create nothing.
	code, out = postJSON(t, app, "/api/tasks/"+strconv.Itoa(failed.ID)+"/retry-from", nil)
	if code != http.StatusOK {
		t.Fatalf("second retry-from status = %d %v, want 200 with zero copies", code, out)
	}
	if again, _ := out["data"].(map[string]any)["created_tasks"].([]any); len(again) != 0 {
		t.Errorf("second retry-from created %v, want none — issue already has a live task", again)
	}
	live, err := client.Task.Query().
		Where(entTask.HasIssueWith(entIssue.ID(iss.ID)),
			entTask.StatusIn(entTask.StatusPending, entTask.StatusRunning)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count live: %v", err)
	}
	if live != 1 {
		t.Errorf("live tasks for issue = %d, want 1", live)
	}
}

// TestRetryTaskInheritsIssueLinkAndSkipsDuplicates covers the single retry:
// the copy keeps the issue link, and a retry that would double-enqueue an issue
// that already has a live task is refused with 409.
func TestRetryTaskInheritsIssueLinkAndSkipsDuplicates(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	app := retryApp(t)

	client := db.Client()
	sh := client.Sheep.Create().SetName("retry-sheep2").SaveX(ctx)
	proj := client.Project.Create().SetName("bori2").SetPath("/ws/bori2").SetSheep(sh).SaveX(ctx)
	iss := client.Issue.Create().SetTitle("retry target").SetProject(proj).SaveX(ctx)

	stopped := client.Task.Create().
		SetPrompt("stopped issue task").
		SetStatus(entTask.StatusStopped).
		SetSheep(sh).SetProject(proj).SetIssueID(iss.ID).
		SaveX(ctx)

	code, out := postJSON(t, app, "/api/tasks/"+strconv.Itoa(stopped.ID)+"/retry", nil)
	if code != http.StatusOK {
		t.Fatalf("retry: %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	newID := int(data["task_id"].(float64))
	row := client.Task.Query().Where(entTask.ID(newID)).WithIssue().OnlyX(ctx)
	if row.Edges.Issue == nil || row.Edges.Issue.ID != iss.ID {
		t.Fatalf("retry copy issue link = %v, want issue #%d", row.Edges.Issue, iss.ID)
	}

	// The copy is pending: retrying the same stopped task again must conflict.
	if code, out = postJSON(t, app, "/api/tasks/"+strconv.Itoa(stopped.ID)+"/retry", nil); code != http.StatusConflict {
		t.Fatalf("second retry status = %d %v, want 409", code, out)
	}
	live, err := client.Task.Query().
		Where(entTask.HasIssueWith(entIssue.ID(iss.ID)),
			entTask.StatusIn(entTask.StatusPending, entTask.StatusRunning)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count live: %v", err)
	}
	if live != 1 {
		t.Errorf("live tasks for issue = %d, want 1", live)
	}
}

func newCancelApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Post("/api/tasks/:id/cancel", s.handleCancelTask)
	return app
}

func postCancel(t *testing.T, app *fiber.App, id string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/cancel", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return resp.StatusCode, payload
}

func TestHandleCancelTask_PendingBecomesStopped(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	client := db.Client()

	created := client.Task.Create().
		SetPrompt("queued").
		SetStatus(entTask.StatusPending).
		SaveX(ctx)

	code, payload := postCancel(t, newCancelApp(t), fmt.Sprintf("%d", created.ID))
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v, want 200", code, payload)
	}
	if payload["success"] != true {
		t.Fatalf("success = %v", payload["success"])
	}
	data, _ := payload["data"].(map[string]any)
	if data["cancelled"] != true {
		t.Errorf("data = %v, want cancelled=true", data)
	}

	got := client.Task.GetX(ctx, created.ID)
	if got.Status != entTask.StatusStopped {
		t.Errorf("status = %s, want stopped", got.Status)
	}
	if got.Error != "cancelled by user" {
		t.Errorf("error = %q", got.Error)
	}
}

func TestHandleCancelTask_RejectsNonPendingAndBadID(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	client := db.Client()
	app := newCancelApp(t)

	running := client.Task.Create().
		SetPrompt("live").
		SetStatus(entTask.StatusRunning).
		SaveX(ctx)
	completed := client.Task.Create().
		SetPrompt("done").
		SetStatus(entTask.StatusCompleted).
		SaveX(ctx)

	tests := []struct {
		name       string
		id         string
		wantStatus int
		wantSub    string
	}{
		{"invalid id", "abc", http.StatusBadRequest, "invalid task ID"},
		{"missing", "999999", http.StatusNotFound, "not found"},
		{"running", fmt.Sprintf("%d", running.ID), http.StatusBadRequest, "task is running"},
		{"completed", fmt.Sprintf("%d", completed.ID), http.StatusBadRequest, "only pending"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, payload := postCancel(t, app, tt.id)
			if code != tt.wantStatus {
				t.Fatalf("status = %d payload=%v, want %d", code, payload, tt.wantStatus)
			}
			msg, _ := payload["message"].(string)
			if !strings.Contains(msg, tt.wantSub) {
				t.Errorf("message = %q, want substring %q", msg, tt.wantSub)
			}
		})
	}
}

func newDeleteApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Delete("/api/tasks/:id", s.handleDeleteTask)
	return app
}

func postDelete(t *testing.T, app *fiber.App, id string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/tasks/"+id, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return resp.StatusCode, payload
}

func TestHandleDeleteTask_FailedRemoved(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	client := db.Client()

	created := client.Task.Create().
		SetPrompt("broke").
		SetStatus(entTask.StatusFailed).
		SaveX(ctx)

	code, payload := postDelete(t, newDeleteApp(t), fmt.Sprintf("%d", created.ID))
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v, want 200", code, payload)
	}
	if payload["success"] != true {
		t.Fatalf("success = %v", payload["success"])
	}
	data, _ := payload["data"].(map[string]any)
	if data["deleted"] != true {
		t.Errorf("data = %v, want deleted=true", data)
	}
	if data["task_id"] != float64(created.ID) {
		t.Errorf("data task_id = %v, want %d", data["task_id"], created.ID)
	}

	if _, err := client.Task.Get(ctx, created.ID); err == nil {
		t.Fatal("expected task row to be removed")
	}
}

func TestHandleDeleteTask_RejectsOtherStatus(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	client := db.Client()
	app := newDeleteApp(t)

	pending := client.Task.Create().
		SetPrompt("queued").
		SetStatus(entTask.StatusPending).
		SaveX(ctx)
	running := client.Task.Create().
		SetPrompt("live").
		SetStatus(entTask.StatusRunning).
		SaveX(ctx)
	completed := client.Task.Create().
		SetPrompt("done").
		SetStatus(entTask.StatusCompleted).
		SaveX(ctx)

	tests := []struct {
		name       string
		id         string
		wantStatus int
		wantSub    string
	}{
		{"invalid id", "abc", http.StatusBadRequest, "invalid task ID"},
		{"missing", "999999", http.StatusNotFound, "not found"},
		{"pending", fmt.Sprintf("%d", pending.ID), http.StatusBadRequest, "only failed or stopped"},
		{"running", fmt.Sprintf("%d", running.ID), http.StatusBadRequest, "only failed or stopped"},
		{"completed", fmt.Sprintf("%d", completed.ID), http.StatusBadRequest, "only failed or stopped"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, payload := postDelete(t, app, tt.id)
			if code != tt.wantStatus {
				t.Fatalf("status = %d payload=%v, want %d", code, payload, tt.wantStatus)
			}
			msg, _ := payload["message"].(string)
			if !strings.Contains(msg, tt.wantSub) {
				t.Errorf("message = %q, want substring %q", msg, tt.wantSub)
			}
		})
	}

	// Rows should still exist after rejections
	for _, id := range []int{pending.ID, running.ID, completed.ID} {
		if _, err := client.Task.Get(ctx, id); err != nil {
			t.Errorf("task %d should still exist: %v", id, err)
		}
	}
}
