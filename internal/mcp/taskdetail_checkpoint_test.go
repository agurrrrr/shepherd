package mcp

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/db"
)

func newMCPTestClient(t *testing.T) *ent.Client {
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

func withMCPTestDB(t *testing.T) *ent.Client {
	t.Helper()
	client := newMCPTestClient(t)
	prev := db.ReplaceClient(client)
	t.Cleanup(func() { db.ReplaceClient(prev) })
	return client
}

func newMCPCheckpointRepo(t *testing.T) string {
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

func TestHandleGetTaskDetail_IncludesCheckpointChanges(t *testing.T) {
	client := withMCPTestDB(t)
	ctx := context.Background()
	repo := newMCPCheckpointRepo(t)

	start, err := checkpoint.Take(ctx, repo, checkpoint.TaskRef(1, "start"), "start")
	if err != nil {
		t.Fatalf("take start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatalf("write new.txt: %v", err)
	}
	end, err := checkpoint.Take(ctx, repo, checkpoint.TaskRef(1, "end"), "end")
	if err != nil {
		t.Fatalf("take end: %v", err)
	}

	created := client.Task.Create().
		SetPrompt("change things").
		SetStatus(entTask.StatusCompleted).
		SetCheckpointDir(repo).
		SetCheckpointStart(start.Commit).
		SetCheckpointEnd(end.Commit).
		SetFilesModified([]string{"f.txt", "new.txt"}).
		SaveX(ctx)

	out, err := handleGetTaskDetail(map[string]interface{}{"task_id": created.ID})
	if err != nil {
		t.Fatalf("handleGetTaskDetail: %v", err)
	}
	if !strings.Contains(out, "--- 변경 통계 (체크포인트) ---") {
		t.Fatalf("missing checkpoint section:\n%s", out)
	}
	for _, want := range []string{"f.txt", "new.txt", "합계: 2 files, +2 -0",
		"shepherd task diff"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestHandleGetTaskDetail_OmitsCheckpointSectionWithoutSnapshots(t *testing.T) {
	client := withMCPTestDB(t)
	ctx := context.Background()

	created := client.Task.Create().
		SetPrompt("no checkpoint").
		SetStatus(entTask.StatusCompleted).
		SaveX(ctx)

	out, err := handleGetTaskDetail(map[string]interface{}{"task_id": created.ID})
	if err != nil {
		t.Fatalf("handleGetTaskDetail: %v", err)
	}
	if strings.Contains(out, "변경 통계 (체크포인트)") {
		t.Fatalf("checkpoint section should be omitted:\n%s", out)
	}
}
