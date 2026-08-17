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
