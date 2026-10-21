package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"

	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/db"
)

func newChangesRepo(t *testing.T) string {
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
		t.Fatalf("write f.txt: %v", err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "initial")
	return dir
}

func setTaskCheckpointEnabled(t *testing.T, enabled bool) {
	t.Helper()
	prev := viper.GetBool("task_checkpoint_enabled")
	viper.Set("task_checkpoint_enabled", enabled)
	t.Cleanup(func() { viper.Set("task_checkpoint_enabled", prev) })
}

func newTaskChangesApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/tasks/:id/changes", s.handleTaskChanges)
	app.Get("/api/tasks/:id/changes/diff", s.handleTaskChangesDiff)
	app.Get("/api/tasks/:id/changes/patch", s.handleTaskChangesPatch)
	return app
}

func getTaskChanges(t *testing.T, app *fiber.App, path string) (int, *http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp, body
}

// createCheckpointPair records a start and end snapshot with one real commit
// in between, so head_start/head_end and commits_during_task are exercised.
func createCheckpointPair(t *testing.T, dir string) (start, end *checkpoint.Snapshot) {
	t.Helper()
	ctx := context.Background()
	start, err := checkpoint.Take(ctx, dir, checkpoint.TaskRef(1, "start"), "start")
	if err != nil {
		t.Fatalf("take start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	run := exec.Command("git", "-C", dir, "commit", "-qam", "during task")
	run.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("commit during task: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatalf("write new.txt: %v", err)
	}
	end, err = checkpoint.Take(ctx, dir, checkpoint.TaskRef(1, "end"), "end")
	if err != nil {
		t.Fatalf("take end: %v", err)
	}
	return start, end
}

type changesPayload struct {
	Success bool `json:"success"`
	Data    struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
		Live      bool   `json:"live"`
		Dir       string `json:"dir"`
		Start     string `json:"start"`
		End       string `json:"end"`
		HeadStart string `json:"head_start"`
		HeadEnd   string `json:"head_end"`
		Commits   []struct {
			Hash    string `json:"hash"`
			Subject string `json:"subject"`
		} `json:"commits_during_task"`
		Files []struct {
			Path      string `json:"path"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		} `json:"files"`
		Additions             int      `json:"additions"`
		Deletions             int      `json:"deletions"`
		FilesModifiedMismatch []string `json:"files_modified_mismatch"`
		Warning               string   `json:"warning"`
	} `json:"data"`
}

func decodeChanges(t *testing.T, body []byte) changesPayload {
	t.Helper()
	var payload changesPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return payload
}

func TestHandleTaskChanges_AvailableSummary(t *testing.T) {
	withServerTestDB(t)
	setTaskCheckpointEnabled(t, true)
	ctx := context.Background()
	repo := newChangesRepo(t)
	start, end := createCheckpointPair(t, repo)

	created := db.Client().Task.Create().
		SetPrompt("change things").
		SetStatus(entTask.StatusCompleted).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SetCheckpointEnd(end.Commit).
		SetFilesModified([]string{"f.txt", "missing.go"}).
		SaveX(ctx)

	code, _, body := getTaskChanges(t, newTaskChangesApp(t), "/api/tasks/"+itoa(created.ID)+"/changes")
	if code != http.StatusOK {
		t.Fatalf("status = %d body=%s", code, body)
	}
	payload := decodeChanges(t, body)
	if !payload.Success || !payload.Data.Available {
		t.Fatalf("payload = %+v, want available", payload)
	}
	if payload.Data.Reason != "" {
		t.Errorf("reason = %q, want empty", payload.Data.Reason)
	}
	if len(payload.Data.Files) != 2 {
		t.Fatalf("files = %+v, want f.txt and new.txt", payload.Data.Files)
	}
	if payload.Data.Additions != 2 || payload.Data.Deletions != 0 {
		t.Errorf("totals = +%d -%d, want +2 -0", payload.Data.Additions, payload.Data.Deletions)
	}
	if payload.Data.HeadStart == "" || payload.Data.HeadEnd == "" || payload.Data.HeadStart == payload.Data.HeadEnd {
		t.Errorf("head range = %q..%q, want two different parents", payload.Data.HeadStart, payload.Data.HeadEnd)
	}
	if len(payload.Data.Commits) != 1 || payload.Data.Commits[0].Subject != "during task" {
		t.Errorf("commits = %+v, want the during-task commit", payload.Data.Commits)
	}
	if len(payload.Data.FilesModifiedMismatch) != 1 || payload.Data.FilesModifiedMismatch[0] != "missing.go" {
		t.Errorf("mismatch = %v, want [missing.go]", payload.Data.FilesModifiedMismatch)
	}
}

func TestHandleTaskChanges_Reasons(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	repo := newChangesRepo(t)
	start, end := createCheckpointPair(t, repo)
	app := newTaskChangesApp(t)

	disabled := db.Client().Task.Create().
		SetPrompt("old task").
		SetStatus(entTask.StatusCompleted).
		SaveX(ctx)
	notGit := db.Client().Task.Create().
		SetPrompt("not git").
		SetStatus(entTask.StatusCompleted).
		SetCheckpointDir(t.TempDir()).
		SetCheckpointError("not a git repository").
		SaveX(ctx)
	pruned := db.Client().Task.Create().
		SetPrompt("pruned").
		SetStatus(entTask.StatusCompleted).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SetCheckpointEnd(strings.Repeat("0", 40)).
		SaveX(ctx)
	running := db.Client().Task.Create().
		SetPrompt("running").
		SetStatus(entTask.StatusRunning).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SaveX(ctx)
	noEnd := db.Client().Task.Create().
		SetPrompt("failed without end").
		SetStatus(entTask.StatusFailed).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SaveX(ctx)

	setTaskCheckpointEnabled(t, false)
	_, _, body := getTaskChanges(t, app, "/api/tasks/"+itoa(disabled.ID)+"/changes")
	if got := decodeChanges(t, body).Data.Reason; got != "checkpoint disabled" {
		t.Errorf("disabled reason = %q", got)
	}
	setTaskCheckpointEnabled(t, true)

	_, _, body = getTaskChanges(t, app, "/api/tasks/"+itoa(notGit.ID)+"/changes")
	if got := decodeChanges(t, body).Data.Reason; got != "not a git repository" {
		t.Errorf("not-git reason = %q", got)
	}

	_, _, body = getTaskChanges(t, app, "/api/tasks/"+itoa(pruned.ID)+"/changes")
	if got := decodeChanges(t, body).Data.Reason; got != "snapshot pruned" {
		t.Errorf("pruned reason = %q", got)
	}

	_, _, body = getTaskChanges(t, app, "/api/tasks/"+itoa(running.ID)+"/changes")
	if got := decodeChanges(t, body).Data.Reason; got != "running" {
		t.Errorf("running reason = %q", got)
	}

	_, _, body = getTaskChanges(t, app, "/api/tasks/"+itoa(noEnd.ID)+"/changes")
	if got := decodeChanges(t, body).Data.Reason; got != "no end snapshot" {
		t.Errorf("no-end reason = %q", got)
	}

	_ = end
}

func TestHandleTaskChanges_LiveRunningAndFailedWarning(t *testing.T) {
	withServerTestDB(t)
	setTaskCheckpointEnabled(t, true)
	ctx := context.Background()
	repo := newChangesRepo(t)
	start, err := checkpoint.Take(ctx, repo, checkpoint.TaskRef(2, "start"), "start")
	if err != nil {
		t.Fatalf("take start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("one\nlive\n"), 0644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}

	running := db.Client().Task.Create().
		SetPrompt("running").
		SetStatus(entTask.StatusRunning).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SaveX(ctx)
	failed := db.Client().Task.Create().
		SetPrompt("failed").
		SetStatus(entTask.StatusFailed).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SaveX(ctx)
	app := newTaskChangesApp(t)

	_, _, body := getTaskChanges(t, app, "/api/tasks/"+itoa(running.ID)+"/changes?live=1")
	got := decodeChanges(t, body).Data
	if !got.Available || !got.Live {
		t.Fatalf("live running = %+v, want available live", got)
	}
	if got.Warning != "" {
		t.Errorf("running warning = %q, want empty", got.Warning)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "f.txt" {
		t.Errorf("files = %+v, want f.txt", got.Files)
	}

	_, _, body = getTaskChanges(t, app, "/api/tasks/"+itoa(failed.ID)+"/changes?live=1")
	got = decodeChanges(t, body).Data
	if !got.Available || !got.Live {
		t.Fatalf("live failed = %+v, want available live", got)
	}
	if !strings.Contains(got.Warning, "작업이 끝난 뒤") {
		t.Errorf("failed warning = %q, want the post-task warning", got.Warning)
	}
}

func TestHandleTaskChangesDiff_AndValidation(t *testing.T) {
	withServerTestDB(t)
	setTaskCheckpointEnabled(t, true)
	ctx := context.Background()
	repo := newChangesRepo(t)
	start, end := createCheckpointPair(t, repo)
	created := db.Client().Task.Create().
		SetPrompt("diff").
		SetStatus(entTask.StatusCompleted).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SetCheckpointEnd(end.Commit).
		SaveX(ctx)
	app := newTaskChangesApp(t)
	base := "/api/tasks/" + itoa(created.ID) + "/changes/diff"

	code, _, body := getTaskChanges(t, app, base+"?file=f.txt")
	if code != http.StatusOK {
		t.Fatalf("diff status = %d body=%s", code, body)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Path      string `json:"path"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Truncated bool   `json:"truncated"`
			Hunks     []struct {
				Header string `json:"header"`
			} `json:"hunks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if !payload.Success || payload.Data.Path != "f.txt" || payload.Data.Status != "modified" {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Data.Additions != 1 || len(payload.Data.Hunks) == 0 {
		t.Errorf("diff = %+v, want +1 with hunks", payload.Data)
	}

	for _, tt := range []struct {
		name string
		url  string
		want int
	}{
		{"leading dash", base + "?file=-evil", http.StatusBadRequest},
		{"nul byte", base + "?file=%00", http.StatusBadRequest},
		{"not in list", base + "?file=missing.go", http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, _, body := getTaskChanges(t, app, tt.url)
			if code != tt.want {
				t.Errorf("status = %d body=%s, want %d", code, body, tt.want)
			}
		})
	}
}

func TestHandleTaskChangesPatch(t *testing.T) {
	withServerTestDB(t)
	setTaskCheckpointEnabled(t, true)
	ctx := context.Background()
	repo := newChangesRepo(t)
	start, end := createCheckpointPair(t, repo)
	created := db.Client().Task.Create().
		SetPrompt("patch").
		SetStatus(entTask.StatusCompleted).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SetCheckpointEnd(end.Commit).
		SaveX(ctx)
	app := newTaskChangesApp(t)

	code, resp, body := getTaskChanges(t, app, "/api/tasks/"+itoa(created.ID)+"/changes/patch")
	if code != http.StatusOK {
		t.Fatalf("status = %d body=%s", code, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/x-diff") {
		t.Errorf("content-type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "task-"+itoa(created.ID)+".patch") {
		t.Errorf("content-disposition = %q", cd)
	}
	patch := string(body)
	if !strings.Contains(patch, "diff --git a/f.txt b/f.txt") || !strings.Contains(patch, "new.txt") {
		t.Errorf("patch = %s", patch)
	}

	unavailable := db.Client().Task.Create().
		SetPrompt("none").
		SetStatus(entTask.StatusCompleted).
		SaveX(ctx)
	code, _, _ = getTaskChanges(t, app, "/api/tasks/"+itoa(unavailable.ID)+"/changes/patch")
	if code != http.StatusNotFound {
		t.Errorf("unavailable patch status = %d, want 404", code)
	}
}

func itoa(id int) string {
	return strconv.Itoa(id)
}
