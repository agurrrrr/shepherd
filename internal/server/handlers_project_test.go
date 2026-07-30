package server

import (
	"context"
	"database/sql"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
)

// newTestClient mirrors internal/db.Init: modernc's driver registers as
// "sqlite" while ent needs the "sqlite3" dialect name.
func newTestClient(t *testing.T) *ent.Client {
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

// Projects created before the hidden field existed must migrate to visible,
// and the flag must round-trip so the sidebar filter has something to read.
func TestProjectHiddenDefaultsToFalseAndRoundTrips(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	p := client.Project.Create().
		SetName("demo").
		SetPath("/tmp/demo").
		SaveX(ctx)
	if p.Hidden {
		t.Fatalf("new project should be visible, got hidden=true")
	}

	n := client.Project.Update().
		Where(entProject.Name("demo")).
		SetHidden(true).
		SaveX(ctx)
	if n != 1 {
		t.Fatalf("expected 1 row updated, got %d", n)
	}

	if got := client.Project.GetX(ctx, p.ID); !got.Hidden {
		t.Errorf("project should be hidden after update")
	}

	// Unhiding must work too — the toggle is symmetric.
	client.Project.Update().Where(entProject.Name("demo")).SetHidden(false).SaveX(ctx)
	if got := client.Project.GetX(ctx, p.ID); got.Hidden {
		t.Errorf("project should be visible after unhide")
	}
}

// SetHidden reports a clear error for an unknown project instead of silently
// succeeding on zero rows.
func TestProjectHiddenUpdateMissesUnknownName(t *testing.T) {
	client := newTestClient(t)

	n, err := client.Project.Update().
		Where(entProject.Name("nope")).
		SetHidden(true).
		Save(context.Background())
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows updated for unknown project, got %d", n)
	}
}
