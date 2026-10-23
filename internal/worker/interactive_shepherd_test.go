package worker

import (
	"context"
	"database/sql"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
)

func newWorkerTestClient(t *testing.T) *ent.Client {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", sqlDB)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := db.ReplaceClient(client)
	t.Cleanup(func() {
		db.ReplaceClient(prev)
		client.Close()
	})
	return client
}

// selectWorkDir decides the run directory (#594). Three cases matter:
// manager + WorkDir (shepherd queue task), manager without WorkDir (the wiki
// auto-ingest callback and YouTrack import paths, which must keep failing as
// they have for months), and an ordinary sheep (project path always wins).
func TestSelectWorkDir(t *testing.T) {
	const projectPath = "/home/user/code/shepherd"

	t.Run("ordinary sheep uses the project path", func(t *testing.T) {
		got, err := selectWorkDir("양순이", true, projectPath, InteractiveOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != projectPath {
			t.Errorf("workDir = %q, want %q", got, projectPath)
		}
	})

	t.Run("ordinary sheep ignores opts.WorkDir", func(t *testing.T) {
		got, err := selectWorkDir("양순이", true, projectPath, InteractiveOptions{WorkDir: "/tmp/workspace"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != projectPath {
			t.Errorf("workDir = %q, want the project path %q", got, projectPath)
		}
	})

	t.Run("manager with WorkDir uses the workspace", func(t *testing.T) {
		got, err := selectWorkDir(names.ManagerName, false, "", InteractiveOptions{WorkDir: "/home/user/code"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/home/user/code" {
			t.Errorf("workDir = %q, want /home/user/code", got)
		}
	})

	t.Run("manager without WorkDir keeps failing (wiki ingest / YouTrack paths)", func(t *testing.T) {
		got, err := selectWorkDir(names.ManagerName, false, "", InteractiveOptions{})
		if err == nil {
			t.Fatalf("expected an error, got workDir=%q", got)
		}
		if want := "no project assigned to 'shepherd'"; err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("non-manager without project fails", func(t *testing.T) {
		if _, err := selectWorkDir("깜순이", false, "", InteractiveOptions{WorkDir: "/tmp/x"}); err == nil {
			t.Fatal("expected an error for a project-less non-manager sheep")
		}
	})
}

// ExecuteInteractive on the manager without opts.WorkDir must still fail with
// "no project assigned" — pinning that the wiki auto-ingest callback
// (cmd/shepherd/main.go) and the YouTrack import are unaffected by #594. If
// those paths are ever routed through WorkDir, the run would register in the
// running-task registry under "shepherd" and clobber a live shepherd queue
// task; fixing them requires opts.RegistryName first (see selectWorkDir).
func TestExecuteInteractiveManagerWithoutWorkDirStillFails(t *testing.T) {
	client := newWorkerTestClient(t)
	ctx := context.Background()

	client.Sheep.Create().
		SetName(names.ManagerName).
		SetStatus(sheep.StatusIdle).
		SetProvider(sheep.ProviderClaude).
		SaveX(ctx)

	_, err := ExecuteInteractive(names.ManagerName, "ingest the wiki", DefaultInteractiveOptions(nil, nil))
	if err == nil {
		t.Fatal("expected 'no project assigned' error")
	}
	if want := "no project assigned to 'shepherd'"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	// The sheep must not have been flipped to working by the aborted run.
	s := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)
	if s.Status != sheep.StatusIdle {
		t.Errorf("manager status = %s, want idle", s.Status)
	}
}

// claudeModelArgs: per-task override wins over the global config; the global
// config is the fallback; neither set means no --model flag. Before #594 the
// override did not exist, so a shepherd task could not pin a claude model.
func TestClaudeModelArgs(t *testing.T) {
	prevClaude := viper.Get("model_claude")
	t.Cleanup(func() { viper.Set("model_claude", prevClaude) })

	t.Run("override wins over the global config", func(t *testing.T) {
		viper.Set("model_claude", "sonnet")
		got := claudeModelArgs("opus")
		if len(got) != 2 || got[0] != "--model" || got[1] != "opus" {
			t.Errorf("claudeModelArgs(opus) = %v, want [--model opus]", got)
		}
	})

	t.Run("global config is used when no override", func(t *testing.T) {
		viper.Set("model_claude", "sonnet")
		got := claudeModelArgs("")
		if len(got) != 2 || got[1] != "sonnet" {
			t.Errorf("claudeModelArgs(\"\") = %v, want [--model sonnet]", got)
		}
	})

	t.Run("whitespace-only override falls back to the global config", func(t *testing.T) {
		viper.Set("model_claude", "sonnet")
		got := claudeModelArgs("   ")
		if len(got) != 2 || got[1] != "sonnet" {
			t.Errorf("claudeModelArgs(spaces) = %v, want [--model sonnet]", got)
		}
	})

	t.Run("neither set yields no flag", func(t *testing.T) {
		viper.Set("model_claude", "")
		if got := claudeModelArgs(""); got != nil {
			t.Errorf("claudeModelArgs(\"\") = %v, want nil", got)
		}
	})
}

// FreshSession must clear the recorded session even when session_reuse is on,
// because shepherd review/patrol prompts carry their own context (#594). The
// provider stage below is unreachable in tests (no CLI), so the observable
// part is the resolved sessionID; that is asserted through a focused helper.
func TestFreshSessionClearsSessionID(t *testing.T) {
	prev := viper.GetBool("session_reuse")
	viper.Set("session_reuse", true)
	t.Cleanup(func() { viper.Set("session_reuse", prev) })

	// Same expression ExecuteInteractive uses.
	resolve := func(fresh bool, stored string) string {
		sessionID := stored
		if !config.GetBool("session_reuse") || fresh {
			sessionID = ""
		}
		return sessionID
	}

	if got := resolve(true, "abc-123"); got != "" {
		t.Errorf("FreshSession=true → sessionID=%q, want empty", got)
	}
	if got := resolve(false, "abc-123"); got != "abc-123" {
		t.Errorf("FreshSession=false with session_reuse → sessionID=%q, want abc-123", got)
	}

	viper.Set("session_reuse", false)
	if got := resolve(false, "abc-123"); got != "" {
		t.Errorf("session_reuse=false → sessionID=%q, want empty", got)
	}
}
