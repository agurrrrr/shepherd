package server

import (
	"context"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
)

func TestStatsKey(t *testing.T) {
	cases := []struct {
		provider, model, want string
	}{
		{"claude", "", "claude"},
		{"claude", "anything", "claude"},
		{"claude", "umans", "claude"}, // polluted model must not leak into key
		{"Claude", "x", "claude"},     // case-insensitive provider
		{"grok", "umans", "grok"},
		{"grok", "", "grok"},
		{"auto", "foo", "claude"},
		{"auto", "", "claude"},
		{"magi", "gpu0", "magi"},
		{"embedded", "umans", "embedded/umans"},
		{"embedded", "", "embedded"},
		{"opencode", "a/b", "opencode/a/b"},
		{"opencode", "", "opencode"},
		{"pi", "my-model", "pi/my-model"},
		{"pi", "  ", "pi"},
		{"", "umans", "(unknown)"},
		{"  ", "", "(unknown)"},
		{"future", "m1", "future/m1"},
		{"future", "", "future"},
	}
	for _, tc := range cases {
		got := statsKey(tc.provider, tc.model)
		if got != tc.want {
			t.Errorf("statsKey(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestStatsProvider(t *testing.T) {
	if got := statsProvider(""); got != "(unknown)" {
		t.Errorf("empty → %q", got)
	}
	if got := statsProvider("auto"); got != "claude" {
		t.Errorf("auto → %q", got)
	}
	if got := statsProvider("EMBEDDED"); got != "embedded" {
		t.Errorf("EMBEDDED → %q", got)
	}
}

func TestModelActivityFrom_CompletedOnlyAndSums(t *testing.T) {
	start := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Second) // 90s

	mk := func(status entTask.Status, provider sheep.Provider, model string, prompt, completion int64, started, completed time.Time) *ent.Task {
		t := &ent.Task{
			Status:           status,
			Model:            model,
			PromptTokens:     prompt,
			CompletionTokens: completion,
			StartedAt:        started,
			CompletedAt:      completed,
		}
		if provider != "" {
			t.Edges.Sheep = &ent.Sheep{Provider: provider}
		}
		return t
	}

	tasks := []*ent.Task{
		// claude completed — model pollution ignored
		mk(entTask.StatusCompleted, sheep.ProviderClaude, "umans", 100, 50, start, end),
		// another claude → same bucket
		mk(entTask.StatusCompleted, sheep.ProviderClaude, "opus", 200, 100, start, end),
		// embedded endpoint
		mk(entTask.StatusCompleted, sheep.ProviderEmbedded, "umans", 1000, 500, start, end),
		// failed — must be excluded
		mk(entTask.StatusFailed, sheep.ProviderGrok, "", 999, 999, start, end),
		// running — excluded
		mk(entTask.StatusRunning, sheep.ProviderGrok, "", 1, 1, start, time.Time{}),
		// grok completed with zero tokens (normal for CLI)
		mk(entTask.StatusCompleted, sheep.ProviderGrok, "something", 0, 0, start, end),
		// no sheep
		mk(entTask.StatusCompleted, "", "", 10, 5, start, end),
		// opencode empty model
		mk(entTask.StatusCompleted, sheep.ProviderOpencode, "", 20, 10, start, end),
		// stopped — excluded
		mk(entTask.StatusStopped, sheep.ProviderPi, "m", 1, 1, start, end),
	}

	win := modelActivityFrom(tasks)

	if win.TotalCompleted != 6 {
		t.Fatalf("TotalCompleted = %d, want 6", win.TotalCompleted)
	}
	// tokens: claude(150+300)=450 + embedded 1500 + grok 0 + unknown 15 + opencode 30 = 1995
	if win.TotalTokens != 1995 {
		t.Fatalf("TotalTokens = %d, want 1995", win.TotalTokens)
	}
	// 6 completed * 90s
	if win.TotalDurationSec != 6*90 {
		t.Fatalf("TotalDurationSec = %d, want %d", win.TotalDurationSec, 6*90)
	}

	byKey := map[string]modelStatItem{}
	for _, it := range win.Items {
		byKey[it.Key] = it
	}

	// Sort order: completed desc — claude has 2, others 1.
	if len(win.Items) == 0 || win.Items[0].Key != "claude" {
		t.Fatalf("first item key = %q, want claude (highest completed)", win.Items[0].Key)
	}

	claude := byKey["claude"]
	if claude.Completed != 2 || claude.TotalTokens != 450 || claude.DurationSec != 180 {
		t.Errorf("claude = %+v, want completed=2 tokens=450 duration=180", claude)
	}
	if claude.Provider != "claude" {
		t.Errorf("claude.Provider = %q", claude.Provider)
	}
	if claude.PromptTokens != 300 || claude.CompletionTokens != 150 {
		t.Errorf("claude prompt/completion = %d/%d", claude.PromptTokens, claude.CompletionTokens)
	}

	emb := byKey["embedded/umans"]
	if emb.Completed != 1 || emb.TotalTokens != 1500 || emb.Provider != "embedded" {
		t.Errorf("embedded/umans = %+v", emb)
	}

	grok := byKey["grok"]
	if grok.Completed != 1 || grok.TotalTokens != 0 {
		t.Errorf("grok = %+v", grok)
	}

	unk := byKey["(unknown)"]
	if unk.Completed != 1 || unk.TotalTokens != 15 {
		t.Errorf("(unknown) = %+v", unk)
	}

	oc := byKey["opencode"]
	if oc.Completed != 1 || oc.TotalTokens != 30 {
		t.Errorf("opencode = %+v", oc)
	}

	if _, ok := byKey["pi/m"]; ok {
		t.Error("stopped pi task should not appear")
	}
	if _, ok := byKey["grok"]; !ok {
		t.Error("expected grok bucket from completed task")
	}
}

func TestModelActivityFrom_Empty(t *testing.T) {
	win := modelActivityFrom(nil)
	if win.TotalCompleted != 0 || win.TotalTokens != 0 || win.TotalDurationSec != 0 || len(win.Items) != 0 {
		t.Errorf("empty input should yield zero window, got %+v", win)
	}
}

func TestModelActivityFrom_SortTieBreakByTokensThenKey(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)

	// Same completed count (1 each); higher tokens first; then key asc.
	tasks := []*ent.Task{
		{
			Status:           entTask.StatusCompleted,
			PromptTokens:     10,
			CompletionTokens: 0,
			StartedAt:        start,
			CompletedAt:      end,
			Edges:            ent.TaskEdges{Sheep: &ent.Sheep{Provider: sheep.ProviderGrok}},
		},
		{
			Status:           entTask.StatusCompleted,
			PromptTokens:     100,
			CompletionTokens: 0,
			StartedAt:        start,
			CompletedAt:      end,
			Edges:            ent.TaskEdges{Sheep: &ent.Sheep{Provider: sheep.ProviderClaude}},
		},
		{
			Status:           entTask.StatusCompleted,
			PromptTokens:     10,
			CompletionTokens: 0,
			StartedAt:        start,
			CompletedAt:      end,
			Edges:            ent.TaskEdges{Sheep: &ent.Sheep{Provider: sheep.ProviderMagi}},
		},
	}
	win := modelActivityFrom(tasks)
	if len(win.Items) != 3 {
		t.Fatalf("len=%d", len(win.Items))
	}
	// claude (100 tokens) first, then grok before magi (same tokens, key asc)
	if win.Items[0].Key != "claude" {
		t.Errorf("items[0]=%q want claude", win.Items[0].Key)
	}
	if win.Items[1].Key != "grok" {
		t.Errorf("items[1]=%q want grok", win.Items[1].Key)
	}
	if win.Items[2].Key != "magi" {
		t.Errorf("items[2]=%q want magi", win.Items[2].Key)
	}
}

// Each window must only count tasks created inside it; a 40-day-old task
// belongs to 3m/6m/1y but not to 1m or narrower.
func TestDashboardActivity_WindowsBucketByCreatedAt(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	mk := func(age time.Duration) *ent.Task {
		return &ent.Task{
			Status:           entTask.StatusCompleted,
			PromptTokens:     10,
			CompletionTokens: 5,
			CreatedAt:        now.Add(-age),
			Edges: ent.TaskEdges{
				Project: &ent.Project{Name: "demo"},
				Sheep:   &ent.Sheep{Provider: sheep.ProviderClaude},
			},
		}
	}
	day := 24 * time.Hour
	tasks := []*ent.Task{
		mk(time.Hour),      // 5h and wider
		mk(10 * time.Hour), // 1d and wider
		mk(3 * day),        // 7d and wider
		mk(20 * day),       // 1m and wider
		mk(40 * day),       // 3m and wider
		mk(120 * day),      // 6m and wider
		mk(300 * day),      // 1y only
		mk(400 * day),      // outside every window
	}

	activity, byModel := dashboardActivity(tasks, now)

	want := map[string]int{"5h": 1, "1d": 2, "7d": 3, "1m": 4, "3m": 5, "6m": 6, "1y": 7}
	for key, n := range want {
		if got := activity[key].Total; got != n {
			t.Errorf("activity[%s].Total = %d, want %d", key, got, n)
		}
		if got := byModel[key].TotalCompleted; got != n {
			t.Errorf("byModel[%s].TotalCompleted = %d, want %d", key, got, n)
		}
	}
	if len(activity) != len(dashboardWindows) || len(byModel) != len(dashboardWindows) {
		t.Errorf("expected %d windows, got activity=%d byModel=%d", len(dashboardWindows), len(activity), len(byModel))
	}
	if got := activity["1y"].Items[0].TotalTokenCount; got != 7*15 {
		t.Errorf("1y tokens = %d, want %d", got, 7*15)
	}
}

// dashboardTasks trims columns, so the project/sheep edges and the fields the
// folds read must still come back populated — and the project filter must
// drop other projects' tasks.
func TestDashboardTasks_ProjectFilterAndTrimmedColumns(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	alpha := client.Project.Create().SetName("alpha").SetPath("/tmp/alpha").SaveX(ctx)
	beta := client.Project.Create().SetName("beta").SetPath("/tmp/beta").SaveX(ctx)
	sh := client.Sheep.Create().SetName("dolly").SetProvider(sheep.ProviderEmbedded).SaveX(ctx)

	now := time.Now()
	client.Task.Create().SetPrompt("a").SetProject(alpha).SetSheep(sh).
		SetStatus(entTask.StatusCompleted).SetModel("umans").
		SetPromptTokens(100).SetCompletionTokens(20).SetOutput([]string{"big log"}).
		SetCreatedAt(now.Add(-time.Hour)).SaveX(ctx)
	client.Task.Create().SetPrompt("b").SetProject(beta).SetSheep(sh).
		SetStatus(entTask.StatusCompleted).
		SetCreatedAt(now.Add(-time.Hour)).SaveX(ctx)
	// Older than the requested window — must not be loaded.
	client.Task.Create().SetPrompt("old").SetProject(alpha).
		SetCreatedAt(now.AddDate(-2, 0, 0)).SaveX(ctx)

	all, err := dashboardTasks(ctx, client, now.AddDate(-1, 0, 0), "")
	if err != nil {
		t.Fatalf("dashboardTasks: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered len = %d, want 2", len(all))
	}

	got, err := dashboardTasks(ctx, client, now.AddDate(-1, 0, 0), "alpha")
	if err != nil {
		t.Fatalf("dashboardTasks(alpha): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("alpha len = %d, want 1", len(got))
	}
	task := got[0]
	if task.Edges.Project == nil || task.Edges.Project.Name != "alpha" {
		t.Errorf("project edge = %+v, want alpha", task.Edges.Project)
	}
	if task.Edges.Sheep == nil || task.Edges.Sheep.Provider != sheep.ProviderEmbedded {
		t.Errorf("sheep edge = %+v, want embedded provider", task.Edges.Sheep)
	}
	if task.Status != entTask.StatusCompleted || task.Model != "umans" || task.PromptTokens != 100 || task.CompletionTokens != 20 {
		t.Errorf("selected fields not loaded: %+v", task)
	}
	if task.Output != nil || task.Prompt != "" {
		t.Errorf("heavy columns should not be loaded: output=%v prompt=%q", task.Output, task.Prompt)
	}

	none, err := dashboardTasks(ctx, client, now.AddDate(-1, 0, 0), "missing")
	if err != nil {
		t.Fatalf("dashboardTasks(missing): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unknown project len = %d, want 0", len(none))
	}
}
