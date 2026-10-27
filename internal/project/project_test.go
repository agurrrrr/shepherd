package project

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/spf13/viper"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/namegen"
)

// TestMain redirects config writes to a throwaway file so viper resets in these
// tests can never truncate the operator's real ~/.shepherd/config.yaml.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "shepherd-project-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testmain: mkdir temp config:", err)
		os.Exit(1)
	}
	config.UseTempConfigFile(filepath.Join(dir, "config.yaml"))

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

// withProjectTestEnv isolates the DB, the config dir (sheep memory dirs and the
// browser profile root used by worker.Rename), and viper (#638 §9). Nothing
// here touches ~/.shepherd.
func withProjectTestEnv(t *testing.T) *ent.Client {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", "file:project_"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
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
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		db.ReplaceClient(prevDB)
		config.UseTempConfigDir(prevCfgDir)
	})
	return client
}

// withFakeSuggest swaps the LLM naming seam so no CLI process or endpoint runs.
func withFakeSuggest(t *testing.T, fn func(ctx context.Context, req namegen.Request) ([]string, error)) {
	t.Helper()
	orig := suggestNames
	suggestNames = fn
	t.Cleanup(func() { suggestNames = orig })
}

// TestAddWithResultNamingDisabledRegression pins the pre-#638 behavior when the
// switch is off: the first idle sheep is reused with its old name, a new sheep
// gets the first pool name, and neither NameSource nor NameFallbackReason is
// set. suggestNames must never be called.
func TestAddWithResultNamingDisabledRegression(t *testing.T) {
	client := withProjectTestEnv(t)
	ctx := context.Background()

	viper.Set("sheep_name_llm_enabled", false)
	withFakeSuggest(t, func(ctx context.Context, req namegen.Request) ([]string, error) {
		t.Error("suggestNames must not run when the switch is off")
		return nil, nil
	})

	idle, err := client.Sheep.Create().SetName("메에롱").SetStatus(sheep.StatusIdle).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	res := AddWithResult("tangbisil", "/tmp/tangbisil", "간식 관리")
	if res.Project == nil {
		t.Fatalf("project not created: %v", res.AssignError)
	}
	if res.AssignedSheep == nil || res.AssignedSheep.ID != idle.ID {
		t.Fatalf("want reused idle sheep, got %+v", res.AssignedSheep)
	}
	if res.SheepCreated {
		t.Errorf("SheepCreated should be false on reuse")
	}
	if res.NameSource != "" || res.NameFallbackReason != "" {
		t.Errorf("name source fields must stay empty when off, got %q / %q", res.NameSource, res.NameFallbackReason)
	}

	// Second project: no idle sheep left → pool name, still no LLM fields.
	res2 := AddWithResult("dotforge", "/tmp/dotforge", "픽셀 아트")
	if res2.Project == nil {
		t.Fatalf("project 2 not created: %v", res2.AssignError)
	}
	if !res2.SheepCreated {
		t.Fatalf("want a newly created sheep")
	}
	if res2.AssignedSheep.Name != "양동이" {
		t.Errorf("pool name = %q, want 양동이", res2.AssignedSheep.Name)
	}
	if res2.NameSource != "" || res2.NameFallbackReason != "" {
		t.Errorf("name source fields must stay empty when off, got %q / %q", res2.NameSource, res2.NameFallbackReason)
	}
}

// TestAddWithResultLLMCreatedSheep: switch on, no idle sheep → the new sheep is
// born with the first LLM candidate; NameSource=llm, no fallback reason.
func TestAddWithResultLLMCreatedSheep(t *testing.T) {
	client := withProjectTestEnv(t)
	ctx := context.Background()

	viper.Set("sheep_name_llm_enabled", true)
	var gotReq namegen.Request
	withFakeSuggest(t, func(ctx context.Context, req namegen.Request) ([]string, error) {
		gotReq = req
		return []string{"탕비양", "탕메에", "건어뭉치"}, nil
	})

	// Occupy the first candidate with a sheep already assigned to another
	// project, so the reuse query skips it and the name counts as taken.
	other, err := client.Project.Create().SetName("other").SetPath("/tmp/other").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	taken, err := client.Sheep.Create().SetName("탕비양").SetStatus(sheep.StatusIdle).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Update().SetSheep(taken).Save(ctx); err != nil {
		t.Fatal(err)
	}

	res := AddWithResult("tangbisil", "/tmp/tangbisil", "간식 관리")
	if res.Project == nil {
		t.Fatalf("project not created: %v", res.AssignError)
	}
	if gotReq.ProjectName != "tangbisil" || gotReq.Description != "간식 관리" {
		t.Errorf("suggest request = %+v", gotReq)
	}
	if res.AssignedSheep == nil || !res.SheepCreated {
		t.Fatalf("want a newly created sheep, got %+v (created=%v)", res.AssignedSheep, res.SheepCreated)
	}
	if res.AssignedSheep.Name != "탕메에" {
		t.Errorf("name = %q, want 탕메에 (first free candidate)", res.AssignedSheep.Name)
	}
	if res.NameSource != NameSourceLLM {
		t.Errorf("NameSource = %q, want %q", res.NameSource, NameSourceLLM)
	}
	if res.NameFallbackReason != "" {
		t.Errorf("NameFallbackReason = %q, want empty", res.NameFallbackReason)
	}

	// The project edge really points at the new sheep.
	proj, err := client.Project.Get(ctx, res.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := proj.QuerySheep().First(ctx)
	if err != nil || sh == nil {
		t.Fatalf("project→sheep edge missing: %v", err)
	}
	if sh.Name != "탕메에" {
		t.Errorf("edge sheep = %q, want 탕메에", sh.Name)
	}
}

// TestAddWithResultLLMFailureFallsBackToPool: the provider call fails, but the
// registration still succeeds with a pool name and a recorded reason.
func TestAddWithResultLLMFailureFallsBackToPool(t *testing.T) {
	withProjectTestEnv(t)

	viper.Set("sheep_name_llm_enabled", true)
	withFakeSuggest(t, func(ctx context.Context, req namegen.Request) ([]string, error) {
		return nil, fmt.Errorf("claude call failed: exec not found")
	})

	res := AddWithResult("nagar-mcp", "/tmp/nagar-mcp", "알림 감시")
	if res.Project == nil {
		t.Fatalf("registration must succeed despite naming failure, got AssignError=%v", res.AssignError)
	}
	if res.AssignedSheep == nil || !res.SheepCreated {
		t.Fatalf("want pool-named new sheep, got %+v", res.AssignedSheep)
	}
	if res.AssignedSheep.Name != "양동이" {
		t.Errorf("pool name = %q, want 양동이", res.AssignedSheep.Name)
	}
	if res.NameSource != NameSourcePool {
		t.Errorf("NameSource = %q, want %q", res.NameSource, NameSourcePool)
	}
	if res.NameFallbackReason == "" {
		t.Error("NameFallbackReason must carry the provider error")
	}
}

// TestAddWithResultLLMReusedSheepRenamed: switch on and an idle sheep exists →
// the sheep is reused AND renamed through worker.Rename; NameSource=reused.
func TestAddWithResultLLMReusedSheepRenamed(t *testing.T) {
	client := withProjectTestEnv(t)
	ctx := context.Background()

	viper.Set("sheep_name_llm_enabled", true)
	withFakeSuggest(t, func(ctx context.Context, req namegen.Request) ([]string, error) {
		return []string{"도트뭉치", "픽셀양"}, nil
	})

	idle, err := client.Sheep.Create().SetName("깜순이").SetStatus(sheep.StatusIdle).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	res := AddWithResult("dotforge", "/tmp/dotforge", "픽셀 에디터")
	if res.Project == nil {
		t.Fatalf("project not created: %v", res.AssignError)
	}
	if res.AssignedSheep == nil || res.SheepCreated {
		t.Fatalf("want reused sheep, got %+v (created=%v)", res.AssignedSheep, res.SheepCreated)
	}
	got, err := client.Sheep.Get(ctx, idle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "도트뭉치" {
		t.Errorf("renamed name = %q, want 도트뭉치", got.Name)
	}
	if res.NameSource != NameSourceReused {
		t.Errorf("NameSource = %q, want %q", res.NameSource, NameSourceReused)
	}
	if res.NameFallbackReason != "" {
		t.Errorf("NameFallbackReason = %q, want empty", res.NameFallbackReason)
	}
}

// TestAddWithResultReuseRenameFailureKeepsRegistration: the reused sheep is
// working (rename refused), but the assignment and registration still stand.
func TestAddWithResultReuseRenameFailureKeepsRegistration(t *testing.T) {
	client := withProjectTestEnv(t)
	ctx := context.Background()

	viper.Set("sheep_name_llm_enabled", true)
	withFakeSuggest(t, func(ctx context.Context, req namegen.Request) ([]string, error) {
		return []string{"새이름"}, nil
	})

	idle, err := client.Sheep.Create().SetName("흰둥이").SetStatus(sheep.StatusWorking).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	res := AddWithResult("nagar-mcp", "/tmp/nagar-mcp", "")
	if res.Project == nil {
		t.Fatalf("registration must succeed, AssignError=%v", res.AssignError)
	}
	got, err := client.Sheep.Get(ctx, idle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "흰둥이" {
		t.Errorf("working sheep must keep its name, got %q", got.Name)
	}
	if res.AssignedSheep == nil || res.AssignedSheep.ID != idle.ID {
		t.Fatalf("working sheep must still be assigned, got %+v", res.AssignedSheep)
	}
	if res.NameSource != NameSourceReused {
		t.Errorf("NameSource = %q, want %q", res.NameSource, NameSourceReused)
	}
	if res.NameFallbackReason == "" {
		t.Error("NameFallbackReason must explain the refused rename")
	}
}
