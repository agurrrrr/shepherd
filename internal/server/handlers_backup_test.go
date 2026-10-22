package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	entProject "github.com/agurrrrr/shepherd/ent/project"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

func TestTasksExportImportPreservesCheckpointFields(t *testing.T) {
	withServerTestDB(t)
	client := db.Client()
	ctx := context.Background()

	p := client.Project.Create().SetName("demo").SetPath(t.TempDir()).SaveX(ctx)
	completed := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	reverted := time.Date(2026, 10, 2, 13, 30, 0, 0, time.UTC)
	created := client.Task.Create().
		SetPrompt("checkpoint backup").
		SetStatus(entTask.StatusCompleted).
		SetProjectID(p.ID).
		SetCreatedAt(completed).
		SetCompletedAt(completed).
		SetCheckpointDir("/tmp/repo").
		SetCheckpointStart("aaaa").
		SetCheckpointEnd("bbbb").
		SetCheckpointError("partial snapshot").
		SetRevertedAt(reverted).
		SetRevertPre("cccc").
		SaveX(ctx)

	s := &Server{}
	app := fiber.New()
	app.Get("/api/settings/tasks-export", s.handleExportTasks)
	app.Post("/api/settings/tasks-import", s.handleImportTasks)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/settings/tasks-export?project=demo", nil))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	exported, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status %d: %s", resp.StatusCode, exported)
	}

	// Remove the source row so the import is not skipped as a duplicate.
	if err := client.Task.DeleteOneID(created.ID).Exec(ctx); err != nil {
		t.Fatalf("delete source task: %v", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "tasks.jsonl")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(exported); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/tasks-import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	impResp, err := app.Test(req)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	importedBody, _ := io.ReadAll(impResp.Body)
	impResp.Body.Close()
	if impResp.StatusCode != http.StatusOK {
		t.Fatalf("import status %d: %s", impResp.StatusCode, importedBody)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Imported int `json:"imported"`
		} `json:"data"`
	}
	if err := json.Unmarshal(importedBody, &payload); err != nil {
		t.Fatalf("decode import response %s: %v", importedBody, err)
	}
	if !payload.Success || payload.Data.Imported != 1 {
		t.Fatalf("import response = %s", importedBody)
	}

	got, err := client.Task.Query().
		Where(entTask.HasProjectWith(entProject.Name("demo"))).
		Only(ctx)
	if err != nil {
		t.Fatalf("query imported task: %v", err)
	}
	if got.CheckpointDir != "/tmp/repo" ||
		got.CheckpointStart != "aaaa" ||
		got.CheckpointEnd != "bbbb" ||
		got.CheckpointError != "partial snapshot" ||
		got.RevertPre != "cccc" {
		t.Fatalf("checkpoint fields did not round-trip: %+v", got)
	}
	if !got.RevertedAt.Equal(reverted) {
		t.Errorf("reverted_at = %v, want %v", got.RevertedAt, reverted)
	}
}
