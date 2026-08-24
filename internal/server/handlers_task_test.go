package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

func withServerTestDB(t *testing.T) {
	t.Helper()
	client := newTestClient(t)
	prev := db.ReplaceClient(client)
	t.Cleanup(func() { db.ReplaceClient(prev) })
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
