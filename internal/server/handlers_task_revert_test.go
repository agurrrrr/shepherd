package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/db"
)

func newRevertApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Post("/api/tasks/:id/revert", s.handleRevertTask)
	app.Post("/api/tasks/:id/revert/undo", s.handleUndoRevertTask)
	return app
}

func postRevert(t *testing.T, app *fiber.App, id int, path string, body string) (int, map[string]any, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/tasks/%d/%s", id, path), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// A revert runs a dozen git processes; on Windows runners that can exceed
	// app.Test's default 1s timeout.
	resp, err := app.Test(req, 30_000)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v", raw.String(), err)
	}
	return resp.StatusCode, payload, raw.Bytes()
}

func gitOutputAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func newCompletedTask(t *testing.T, repo string, start, end *checkpoint.Snapshot) int {
	t.Helper()
	created := db.Client().Task.Create().
		SetPrompt("revert me").
		SetStatus(entTask.StatusCompleted).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SetCheckpointEnd(end.Commit).
		SaveX(context.Background())
	return created.ID
}

func TestHandleRevertTask_DryRunThenRevertThenUndo(t *testing.T) {
	withServerTestDB(t)
	repo := newChangesRepo(t)
	start, end := createCheckpointPair(t, repo)
	id := newCompletedTask(t, repo, start, end)
	app := newRevertApp(t)

	headBefore := gitOutputAt(t, repo, "rev-parse", "HEAD")
	indexBefore, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	code, payload, body := postRevert(t, app, id, "revert", `{"dry_run":true}`)
	if code != http.StatusOK {
		t.Fatalf("dry run status = %d body=%s", code, body)
	}
	data, _ := payload["data"].(map[string]any)
	if data["changed"] != true {
		t.Fatalf("dry run changed = %v, want true", data["changed"])
	}
	if got := readFile(t, filepath.Join(repo, "f.txt")); got != "one\ntwo\n" {
		t.Errorf("dry run changed f.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); err != nil {
		t.Errorf("dry run removed new.txt: %v", err)
	}

	code, payload, body = postRevert(t, app, id, "revert", `{"dry_run":false}`)
	if code != http.StatusOK {
		t.Fatalf("revert status = %d body=%s", code, body)
	}
	if got := readFile(t, filepath.Join(repo, "f.txt")); got != "one\n" {
		t.Errorf("f.txt after revert = %q, want %q", got, "one\n")
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("new.txt should be removed after revert, stat err = %v", err)
	}
	got := db.Client().Task.GetX(context.Background(), id)
	if got.RevertedAt.IsZero() || got.RevertPre == "" {
		t.Fatalf("reverted_at/revert_pre not saved: %+v", got)
	}
	if headAfter := gitOutputAt(t, repo, "rev-parse", "HEAD"); headAfter != headBefore {
		t.Errorf("HEAD changed: %s -> %s", headBefore, headAfter)
	}
	indexAfter, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Errorf("index changed during revert")
	}

	// A second revert must be refused until the undo runs.
	code, payload, body = postRevert(t, app, id, "revert", `{}`)
	if code != http.StatusConflict {
		t.Fatalf("second revert status = %d body=%s", code, body)
	}
	msg, _ := payload["message"].(string)
	if !strings.Contains(msg, "되돌리기 취소") {
		t.Errorf("message = %q, want undo hint", msg)
	}
	if got := readFile(t, filepath.Join(repo, "f.txt")); got != "one\n" {
		t.Errorf("refused revert changed f.txt = %q", got)
	}

	code, payload, body = postRevert(t, app, id, "revert/undo", `{}`)
	if code != http.StatusOK {
		t.Fatalf("undo status = %d body=%s", code, body)
	}
	if got := readFile(t, filepath.Join(repo, "f.txt")); got != "one\ntwo\n" {
		t.Errorf("f.txt after undo = %q", got)
	}
	if got := readFile(t, filepath.Join(repo, "new.txt")); got != "new\n" {
		t.Errorf("new.txt after undo = %q", got)
	}
	got = db.Client().Task.GetX(context.Background(), id)
	if !got.RevertedAt.IsZero() || got.RevertPre != "" {
		t.Errorf("revert fields not cleared: %+v", got)
	}
	if headAfter := gitOutputAt(t, repo, "rev-parse", "HEAD"); headAfter != headBefore {
		t.Errorf("HEAD changed after undo: %s -> %s", headBefore, headAfter)
	}
	indexAfter, err = os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Errorf("index changed during undo")
	}
}

func TestHandleRevertTask_ConflictLeavesWorkingTreeUntouched(t *testing.T) {
	withServerTestDB(t)
	repo := newChangesRepo(t)
	start, err := checkpoint.Take(context.Background(), repo, checkpoint.TaskRef(2, "start"), "start")
	if err != nil {
		t.Fatalf("take start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	end, err := checkpoint.Take(context.Background(), repo, checkpoint.TaskRef(2, "end"), "end")
	if err != nil {
		t.Fatalf("take end: %v", err)
	}
	// The task changed the same line again after its end snapshot; a revert
	// cannot merge that safely.
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("three\n"), 0644); err != nil {
		t.Fatalf("write conflict: %v", err)
	}
	id := newCompletedTask(t, repo, start, end)

	code, payload, body := postRevert(t, newRevertApp(t), id, "revert", `{"dry_run":false}`)
	if code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", code, body)
	}
	conflicts, _ := payload["conflicts"].([]any)
	if len(conflicts) != 1 || conflicts[0] != "f.txt" {
		t.Errorf("conflicts = %v, want [f.txt]", conflicts)
	}
	if got := readFile(t, filepath.Join(repo, "f.txt")); got != "three\n" {
		t.Errorf("conflict changed f.txt = %q", got)
	}
	got := db.Client().Task.GetX(context.Background(), id)
	if !got.RevertedAt.IsZero() {
		t.Errorf("conflict recorded reverted_at = %v", got.RevertedAt)
	}
}

func TestHandleRevertTask_RefusesRunningTaskInSameRepo(t *testing.T) {
	withServerTestDB(t)
	repo := newChangesRepo(t)
	start, end := createCheckpointPair(t, repo)
	id := newCompletedTask(t, repo, start, end)

	db.Client().Task.Create().
		SetPrompt("still running").
		SetStatus(entTask.StatusRunning).
		SetCheckpointDir(repo).
		SaveX(context.Background())

	code, payload, body := postRevert(t, newRevertApp(t), id, "revert", `{"dry_run":false}`)
	if code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", code, body)
	}
	msg, _ := payload["message"].(string)
	if !strings.Contains(msg, "실행 중") {
		t.Errorf("message = %q, want running-task hint", msg)
	}
	if got := readFile(t, filepath.Join(repo, "f.txt")); got != "one\ntwo\n" {
		t.Errorf("refused revert changed f.txt = %q", got)
	}
}

func TestHandleRevertTask_RejectsInvalidStateAndMissingCheckpoint(t *testing.T) {
	withServerTestDB(t)
	repo := newChangesRepo(t)
	start, end := createCheckpointPair(t, repo)
	ctx := context.Background()

	running := db.Client().Task.Create().
		SetPrompt("running").
		SetStatus(entTask.StatusRunning).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SetCheckpointEnd(end.Commit).
		SaveX(ctx)
	noCheckpoint := db.Client().Task.Create().
		SetPrompt("no checkpoint").
		SetStatus(entTask.StatusCompleted).
		SaveX(ctx)
	app := newRevertApp(t)

	for _, tt := range []struct {
		name string
		id   int
		want int
	}{
		{"running", running.ID, http.StatusBadRequest},
		{"no checkpoint", noCheckpoint.ID, http.StatusBadRequest},
		{"undo without revert", noCheckpoint.ID, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := "revert"
			if tt.name == "undo without revert" {
				path = "revert/undo"
			}
			code, _, body := postRevert(t, app, tt.id, path, `{}`)
			if code != tt.want {
				t.Fatalf("status = %d body=%s, want %d", code, body, tt.want)
			}
		})
	}
}

func TestHandleRevertTask_SubdirectoryDoesNotTouchParent(t *testing.T) {
	withServerTestDB(t)
	repo := newChangesRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "parent.txt"), []byte("parent\n"), 0644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	gitOutputAt(t, repo, "add", "parent.txt")
	gitOutputAt(t, repo, "commit", "-qm", "parent")

	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "child.txt"), []byte("child start\n"), 0644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	start, err := checkpoint.Take(context.Background(), sub, checkpoint.TaskRef(3, "start"), "start")
	if err != nil {
		t.Fatalf("take sub start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "child.txt"), []byte("child end\n"), 0644); err != nil {
		t.Fatalf("write child end: %v", err)
	}
	end, err := checkpoint.Take(context.Background(), sub, checkpoint.TaskRef(3, "end"), "end")
	if err != nil {
		t.Fatalf("take sub end: %v", err)
	}
	parentBefore := readFile(t, filepath.Join(repo, "parent.txt"))
	id := newCompletedTask(t, sub, start, end)

	code, _, body := postRevert(t, newRevertApp(t), id, "revert", `{"dry_run":false}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d body=%s", code, body)
	}
	if got := readFile(t, filepath.Join(sub, "child.txt")); got != "child start\n" {
		t.Errorf("child.txt = %q, want reverted content", got)
	}
	if got := readFile(t, filepath.Join(repo, "parent.txt")); got != parentBefore {
		t.Errorf("parent.txt changed: %q -> %q", parentBefore, got)
	}
}
