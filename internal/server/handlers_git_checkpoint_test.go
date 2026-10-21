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
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/db"
)

func newGitLogRepo(t *testing.T) string {
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
	for i, content := range []string{"one\n", "two\n"} {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		run("add", "f.txt")
		run("commit", "-q", "-m", "commit "+string(rune('1'+i)))
	}
	return dir
}

func TestHandleGitLogExcludesCheckpointRefs(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	repo := newGitLogRepo(t)

	if _, err := db.Client().Project.Create().SetName("demo").SetPath(repo).Save(ctx); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Two checkpoint refs (start/end) make `rev-list --all` see four commits.
	start, err := checkpoint.Take(ctx, repo, checkpoint.TaskRef(1, "start"), "shepherd checkpoint task #1 start")
	if err != nil {
		t.Fatalf("take start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("three\n"), 0644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	end, err := checkpoint.Take(ctx, repo, checkpoint.TaskRef(1, "end"), "shepherd checkpoint task #1 end")
	if err != nil {
		t.Fatalf("take end: %v", err)
	}

	rawCount, err := exec.Command("git", "-C", repo, "rev-list", "--count", "--all").Output()
	if err != nil {
		t.Fatalf("raw rev-list: %v", err)
	}
	if strings.TrimSpace(string(rawCount)) != "4" {
		t.Fatalf("raw commit count = %q, want 4", strings.TrimSpace(string(rawCount)))
	}

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/projects/:name/git/log", s.handleGitLog)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/demo/git/log", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Total   int `json:"total"`
			Commits []struct {
				Hash    string   `json:"hash"`
				Subject string   `json:"subject"`
				Refs    []string `json:"refs"`
			} `json:"commits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if !payload.Success {
		t.Fatalf("request failed: %s", body)
	}
	if payload.Data.Total != 2 {
		t.Errorf("total = %d, want 2 (checkpoint commits excluded)", payload.Data.Total)
	}
	if len(payload.Data.Commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(payload.Data.Commits))
	}

	checkpointCommits := map[string]bool{start.Commit: true, end.Commit: true}
	for _, commit := range payload.Data.Commits {
		if checkpointCommits[commit.Hash] {
			t.Errorf("checkpoint commit %s leaked into git log", commit.Hash)
		}
		if strings.Contains(commit.Subject, "shepherd checkpoint") {
			t.Errorf("checkpoint subject leaked: %q", commit.Subject)
		}
		for _, ref := range commit.Refs {
			if strings.HasPrefix(ref, "refs/shepherd/") || strings.Contains(ref, "shepherd/tasks") {
				t.Errorf("checkpoint ref leaked into decorations: %q", ref)
			}
		}
	}
}
