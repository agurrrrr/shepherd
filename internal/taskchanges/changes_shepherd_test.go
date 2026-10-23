package taskchanges

import (
	"context"
	"database/sql"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"

	_ "modernc.org/sqlite"
)

// Shepherd (목자) tasks never checkpoint (#594 → #598): the change summary must
// say so explicitly instead of falling through to "checkpoint disabled".
func TestBuildReportsShepherdTaskReason(t *testing.T) {
	ctx := context.Background()

	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", sqlDB)))
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	prevClient := db.ReplaceClient(client)
	prevRaw := db.ReplaceRawDB(sqlDB)
	t.Cleanup(func() {
		db.ReplaceClient(prevClient)
		db.ReplaceRawDB(prevRaw)
		client.Close()
	})

	sheepRow, err := client.Sheep.Create().SetName("shepherd").SetProvider("claude").Save(ctx)
	if err != nil {
		t.Fatalf("create sheep: %v", err)
	}

	shepherdTask := client.Task.Create().
		SetPrompt("patrol the flock").
		SetStatus(task.StatusCompleted).
		SetSheepID(sheepRow.ID).
		SetShepherdKind("patrol").
		SaveX(ctx)

	cs := Build(ctx, shepherdTask, false)
	if cs.Available {
		t.Fatalf("shepherd task must not report available changes: %+v", cs)
	}
	if cs.Reason != ReasonShepherd {
		t.Errorf("reason = %q, want %q", cs.Reason, ReasonShepherd)
	}

	// A live request must not snapshot the workspace for a shepherd task either.
	live := Build(ctx, shepherdTask, true)
	if live.Available || live.Reason != ReasonShepherd {
		t.Errorf("live build = available:%v reason:%q, want false/%q", live.Available, live.Reason, ReasonShepherd)
	}

	// An ordinary task without checkpoints keeps the old reason.
	ordinary := client.Task.Create().
		SetPrompt("ordinary").
		SetStatus(task.StatusCompleted).
		SetSheepID(sheepRow.ID).
		SaveX(ctx)
	cs = Build(ctx, ordinary, false)
	if cs.Reason == ReasonShepherd {
		t.Errorf("ordinary task must not report %q: %+v", ReasonShepherd, cs)
	}
}
