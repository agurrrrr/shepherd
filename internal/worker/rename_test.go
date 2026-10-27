package worker

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
)

// withRenameTestEnv isolates DB, config dir (memory dirs), and browser profile
// root so Rename tests never touch ~/.shepherd (#638 §9).
func withRenameTestEnv(t *testing.T) *ent.Client {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", "file:rename_"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", sqlDB)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	prevDB := db.ReplaceClient(client)
	prevCfgDir := config.UseTempConfigDir(t.TempDir())
	t.Cleanup(func() {
		db.ReplaceClient(prevDB)
		config.UseTempConfigDir(prevCfgDir)
	})
	return client
}

func TestRenameMovesMemoryAndBrowserDirs(t *testing.T) {
	client := withRenameTestEnv(t)
	ctx := context.Background()

	sh, err := client.Sheep.Create().SetName("양동이").SetStatus(sheep.StatusIdle).Save(ctx)
	if err != nil {
		t.Fatalf("create sheep: %v", err)
	}

	// Seed memory dir and browser profile with a marker file each.
	oldMem := config.GetSheepMemoryDir("양동이")
	if err := os.MkdirAll(oldMem, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldMem, "MEMORY.md"), []byte("mem"), 0644); err != nil {
		t.Fatal(err)
	}
	oldProfile := filepath.Join(filepath.Join(config.GetConfigDir(), "browser"), "양동이")
	if err := os.MkdirAll(oldProfile, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldProfile, "Cookies"), []byte("cookie"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Rename("양동이", "탕비양"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, err := client.Sheep.Get(ctx, sh.ID)
	if err != nil || got.Name != "탕비양" {
		t.Fatalf("DB name after rename = %q (err %v)", got.Name, err)
	}
	if _, err := os.Stat(filepath.Join(config.GetSheepMemoryDir("탕비양"), "MEMORY.md")); err != nil {
		t.Errorf("memory dir not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldMem, "MEMORY.md")); !os.IsNotExist(err) {
		t.Errorf("old memory dir still exists")
	}
	newProfile := filepath.Join(filepath.Join(config.GetConfigDir(), "browser"), "탕비양")
	if _, err := os.Stat(filepath.Join(newProfile, "Cookies")); err != nil {
		t.Errorf("browser profile not moved: %v", err)
	}
}

func TestRenameRejectsWorkingSheep(t *testing.T) {
	client := withRenameTestEnv(t)
	ctx := context.Background()

	_, err := client.Sheep.Create().SetName("메에롱").SetStatus(sheep.StatusWorking).Save(ctx)
	if err != nil {
		t.Fatalf("create sheep: %v", err)
	}

	err = Rename("메에롱", "뭉치")
	if !errors.Is(err, ErrSheepWorking) {
		t.Fatalf("want ErrSheepWorking, got %v", err)
	}
}

func TestRenameRejectsReservedAndManager(t *testing.T) {
	client := withRenameTestEnv(t)
	ctx := context.Background()

	if _, err := client.Sheep.Create().SetName("깜순이").SetStatus(sheep.StatusIdle).Save(ctx); err != nil {
		t.Fatalf("create sheep: %v", err)
	}
	if err := Rename("깜순이", "shepherd"); err == nil {
		t.Errorf("renaming to reserved name should fail")
	}
	if _, err := client.Sheep.Create().SetName("shepherd").SetStatus(sheep.StatusIdle).Save(ctx); err != nil {
		t.Fatalf("create manager row: %v", err)
	}
	if err := Rename("shepherd", "다른이름"); err == nil {
		t.Errorf("renaming the manager sheep should fail")
	}
}

func TestRenameDuplicateNameConflict(t *testing.T) {
	client := withRenameTestEnv(t)
	ctx := context.Background()

	if _, err := client.Sheep.Create().SetName("흰둥이").SetStatus(sheep.StatusIdle).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Sheep.Create().SetName("복실이").SetStatus(sheep.StatusIdle).Save(ctx); err != nil {
		t.Fatal(err)
	}

	err := Rename("흰둥이", "복실이")
	if !errors.Is(err, ErrSheepNameExists) {
		t.Fatalf("want ErrSheepNameExists, got %v", err)
	}
}

func TestRenameNotFound(t *testing.T) {
	withRenameTestEnv(t)
	err := Rename("없는양", "새이름")
	if !errors.Is(err, ErrSheepNotFound) {
		t.Fatalf("want ErrSheepNotFound, got %v", err)
	}
}
