package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/spf13/viper"
)

func TestCallerTaskArgRoundTrip(t *testing.T) {
	args := map[string]interface{}{"project_name": "bori", "id": float64(3)}

	if got := CallerTaskIDFromArgs(args); got != 0 {
		t.Fatalf("clean args should report caller 0, got %d", got)
	}

	args = SetCallerTaskID(args, 42)
	if got := CallerTaskIDFromArgs(args); got != 42 {
		t.Fatalf("CallerTaskIDFromArgs = %d, want 42", got)
	}

	// A hostile/host-provided internal key must be stripped too.
	args["_internal_leak"] = "x"
	caller := StripInternalArgs(args)
	if caller != 42 {
		t.Fatalf("StripInternalArgs returned caller %d, want 42", caller)
	}
	if _, ok := args[CallerTaskArgKey]; ok {
		t.Error("reserved key survived StripInternalArgs")
	}
	if _, ok := args["_internal_leak"]; ok {
		t.Error("_internal_-prefixed key survived StripInternalArgs")
	}
	if args["project_name"] != "bori" {
		t.Error("StripInternalArgs must not touch ordinary keys")
	}

	// taskID <= 0 is a no-op and nil args stay nil.
	before := len(args)
	got := SetCallerTaskID(args, 0)
	if len(got) != before {
		t.Error("SetCallerTaskID(0) must be a no-op")
	}
	if _, ok := got[CallerTaskArgKey]; ok {
		t.Error("SetCallerTaskID(0) must not re-add the key after strip")
	}
	if SetCallerTaskID(nil, -1) != nil {
		t.Error("SetCallerTaskID(nil, -1) must stay nil")
	}
}

func TestCallerTaskIDFromEnv(t *testing.T) {
	t.Setenv("SHEPHERD_TASK_ID", "123")
	if got := CallerTaskIDFromEnv(); got != 123 {
		t.Fatalf("CallerTaskIDFromEnv = %d, want 123", got)
	}
	t.Setenv("SHEPHERD_TASK_ID", "not-a-number")
	if got := CallerTaskIDFromEnv(); got != 0 {
		t.Fatalf("garbage env must yield 0, got %d", got)
	}
	os.Unsetenv("SHEPHERD_TASK_ID")
	if got := CallerTaskIDFromEnv(); got != 0 {
		t.Fatalf("unset env must yield 0, got %d", got)
	}
}

// TestHandleIssueExecute_AutoWatchByCaller pins the two watch paths (#597):
// explicit watch=true marks the issue, and a caller task running on the
// manager (목자) sheep marks it automatically; an ordinary sheep's caller
// does not.
func TestHandleIssueExecute_AutoWatchByCaller(t *testing.T) {
	client := withMCPTestDB(t)
	ctx := context.Background()

	// issue.Execute checks the provider switch; viper.Set only (config.Set
	// would write ~/.shepherd/config.yaml — wiki: viper-writeconfigas-clobbers-config).
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("provider_enabled_claude", true)

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	workerSh := client.Sheep.Create().SetName("양순이").
		SetProvider(sheep.ProviderClaude).SetStatus(sheep.StatusIdle).SaveX(ctx)
	mgrSh := client.Sheep.Create().SetName(names.ManagerName).
		SetProvider(sheep.ProviderClaude).SetStatus(sheep.StatusIdle).SaveX(ctx)
	p.Edges.Sheep = workerSh
	client.Project.UpdateOneID(p.ID).SetSheepID(workerSh.ID).SaveX(ctx)

	mkIssue := func(title string) *ent.Issue {
		t.Helper()
		return client.Issue.Create().SetTitle(title).SetProject(p).SaveX(ctx)
	}

	mgrTask := client.Task.Create().SetPrompt("shepherd review").SetStatus(task.StatusRunning).
		SetSheepID(mgrSh.ID).SetShepherdKind("review").SaveX(ctx)
	workerTask := client.Task.Create().SetPrompt("ordinary work").SetStatus(task.StatusRunning).
		SetSheepID(workerSh.ID).SaveX(ctx)

	tests := []struct {
		name      string
		args      map[string]interface{}
		wantWatch bool
	}{
		{
			name:      "plain call stays unwatched",
			args:      map[string]interface{}{},
			wantWatch: false,
		},
		{
			name:      "explicit watch=true marks the issue",
			args:      map[string]interface{}{"watch": true},
			wantWatch: true,
		},
		{
			name:      "manager-sheep caller auto-watches",
			args:      map[string]interface{}{CallerTaskArgKey: mgrTask.ID},
			wantWatch: true,
		},
		{
			name:      "ordinary sheep caller does not auto-watch",
			args:      map[string]interface{}{CallerTaskArgKey: workerTask.ID},
			wantWatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iss := mkIssue("task " + tt.name)
			args := map[string]interface{}{"project_name": "bori", "id": float64(iss.ID)}
			for k, v := range tt.args {
				args[k] = v
			}

			out, err := handleIssueExecute(args)
			if err != nil {
				t.Fatalf("issue_execute: %v", err)
			}

			got, err := client.Issue.Get(ctx, iss.ID)
			if err != nil {
				t.Fatalf("reload issue: %v", err)
			}
			if got.ShepherdWatch != tt.wantWatch {
				t.Fatalf("shepherd_watch = %v, want %v (output: %s)", got.ShepherdWatch, tt.wantWatch, out)
			}
			if tt.wantWatch && got.ShepherdWatchedAt == nil {
				t.Fatal("watched_at must be stamped when the watch is set")
			}
			if tt.args["watch"] == true && !strings.Contains(out, "shepherd_watch=true") {
				t.Fatalf("output should mention the watch: %s", out)
			}
		})
	}
}

// TestHandleIssueUpsert_ShepherdReview pins the shepherd_review write path:
// it updates shepherd_last_review without touching other fields.
func TestHandleIssueUpsert_ShepherdReview(t *testing.T) {
	client := withMCPTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	iss := client.Issue.Create().SetTitle("review target").SetProject(p).SaveX(ctx)

	out, err := handleIssueUpsert(map[string]interface{}{
		"project_name":    "bori",
		"id":              float64(iss.ID),
		"shepherd_review": "rework: tests missing",
	})
	if err != nil {
		t.Fatalf("issue_upsert: %v", err)
	}
	if !strings.Contains(out, "수정됨") {
		t.Fatalf("unexpected output: %s", out)
	}

	got, err := client.Issue.Get(ctx, iss.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ShepherdLastReview != "rework: tests missing" {
		t.Fatalf("shepherd_last_review = %q", got.ShepherdLastReview)
	}
	if got.Title != "review target" || got.Status != entIssue.StatusTodo {
		t.Fatalf("other fields must stay untouched: %+v", got)
	}
}
