package server

import (
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
