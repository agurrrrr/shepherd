package queue

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

// appendTestDB injects an in-memory DB into BOTH db.Client() and db.RawDB():
// AppendToPendingReviewTaskPrompt reads through ent but writes through raw
// SQL, so both handles must point at the same database.
func appendTestDB(t *testing.T) *ent.Client {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", sqlDB)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevClient := db.ReplaceClient(client)
	prevRaw := db.ReplaceRawDB(sqlDB)
	t.Cleanup(func() {
		db.ReplaceClient(prevClient)
		db.ReplaceRawDB(prevRaw)
		client.Close()
	})
	return client
}

func mkReviewTask(t *testing.T, client *ent.Client, status task.Status, prompt string) *ent.Task {
	t.Helper()
	return client.Task.Create().
		SetPrompt(prompt).
		SetStatus(status).
		SetShepherdKind("review").
		SaveX(context.Background())
}

func TestAppendToPendingReviewTaskPrompt(t *testing.T) {
	client := appendTestDB(t)
	ctx := context.Background()

	t.Run("no pending review task returns false", func(t *testing.T) {
		if AppendToPendingReviewTaskPrompt("block") {
			t.Fatal("expected false when no review task exists")
		}
	})

	mkReviewTask(t, client, task.StatusCompleted, "old finished review")
	mkReviewTask(t, client, task.StatusRunning, "running review")

	t.Run("only-pending guard: running/completed reviews are ignored", func(t *testing.T) {
		if AppendToPendingReviewTaskPrompt("block") {
			t.Fatal("expected false when no PENDING review task exists")
		}
	})

	pending := mkReviewTask(t, client, task.StatusPending, "base prompt")

	if !AppendToPendingReviewTaskPrompt("APPENDED BLOCK") {
		t.Fatal("expected true for a pending review task")
	}

	got, err := client.Task.Get(ctx, pending.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Prompt != "base prompt\n\nAPPENDED BLOCK" {
		t.Fatalf("prompt not appended as expected: %q", got.Prompt)
	}

	// Newest pending wins: create a second, later pending review and append.
	newer := mkReviewTask(t, client, task.StatusPending, "newer base")
	if !AppendToPendingReviewTaskPrompt("NEWER BLOCK") {
		t.Fatal("expected true for the newer pending review task")
	}
	oldRow, _ := client.Task.Get(ctx, pending.ID)
	newRow, _ := client.Task.Get(ctx, newer.ID)
	if strings.Contains(oldRow.Prompt, "NEWER BLOCK") {
		t.Fatal("append must target the NEWEST pending review task")
	}
	if !strings.Contains(newRow.Prompt, "NEWER BLOCK") {
		t.Fatalf("newest pending review did not receive the block: %q", newRow.Prompt)
	}

	// Claimed between lookup and update (status flips to running): no pending
	// row remains, so the caller must fall back to creating a task. The
	// status='pending' guard in the raw UPDATE is pinned separately below at
	// SQL level, because the read and the write happen inside one call.
	client.Task.UpdateOneID(newer.ID).SetStatus(task.StatusRunning).SaveX(ctx)
	client.Task.UpdateOneID(pending.ID).SetStatus(task.StatusCompleted).SaveX(ctx)
	if AppendToPendingReviewTaskPrompt("RACE BLOCK") {
		t.Fatal("expected false when no pending review row remains")
	}
	final, _ := client.Task.Get(ctx, newer.ID)
	if strings.Contains(final.Prompt, "RACE BLOCK") {
		t.Fatal("a running review task must never be appended to")
	}

	// SQL-level guard: the guarded UPDATE matches 0 rows on a claimed row.
	res, err := db.RawDB().ExecContext(ctx,
		"UPDATE tasks SET prompt = prompt || ? WHERE id = ? AND status = 'pending'",
		"GOLD", newer.ID)
	if err != nil {
		t.Fatalf("raw update: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("guarded UPDATE must not touch a running row, affected %d", n)
	}
}
