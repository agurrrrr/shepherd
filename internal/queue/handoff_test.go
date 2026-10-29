package queue

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// DecideLimitHandoff is the single decision point that chooses between an
// immediate provider handoff and the existing backoff retry (#675). Every
// branch is pinned here so a config change cannot silently turn handoff on or
// off in production.

func setupHandoffConfig(t *testing.T, provider, model string) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	for _, p := range []string{"claude", "opencode", "pi", "grok", "embedded", "magi"} {
		viper.SetDefault("provider_enabled_"+p, true)
	}
	viper.SetDefault("limit_handoff_provider", "")
	viper.SetDefault("limit_handoff_model", "")
	if provider != "" {
		viper.Set("limit_handoff_provider", provider)
	}
	if model != "" {
		viper.Set("limit_handoff_model", model)
	}
}

func TestDecideLimitHandoff_OffByDefault(t *testing.T) {
	setupHandoffConfig(t, "", "")

	dec := DecideLimitHandoff("claude", false, 0)
	if dec.Handoff {
		t.Fatal("feature off must not hand off")
	}
	if dec.Reason == "" {
		t.Fatal("decision must explain why it fell back to retry")
	}
}

func TestDecideLimitHandoff_TargetDiffers(t *testing.T) {
	setupHandoffConfig(t, "grok", "grok-4.5")

	dec := DecideLimitHandoff("claude", false, 0)
	if !dec.Handoff {
		t.Fatalf("expected handoff, got retry (%s)", dec.Reason)
	}
	if dec.Provider != "grok" || dec.Model != "grok-4.5" {
		t.Fatalf("target = (%q,%q), want (grok,grok-4.5)", dec.Provider, dec.Model)
	}
}

func TestDecideLimitHandoff_SameProviderRetries(t *testing.T) {
	setupHandoffConfig(t, "claude", "")

	dec := DecideLimitHandoff("claude", false, 0)
	if dec.Handoff {
		t.Fatal("handing off to the same provider would hit the same limit")
	}
}

func TestDecideLimitHandoff_AutoFoldsToClaude(t *testing.T) {
	// A sheep on the runtime alias "auto" runs Claude, so a claude target is
	// the same provider and must retry instead of handing off.
	setupHandoffConfig(t, "claude", "")

	dec := DecideLimitHandoff("auto", false, 0)
	if dec.Handoff {
		t.Fatal("auto == claude: same provider must retry")
	}

	setupHandoffConfig(t, "grok", "")
	dec = DecideLimitHandoff("auto", false, 0)
	if !dec.Handoff || dec.Provider != "grok" {
		t.Fatalf("auto -> grok should hand off: %+v", dec)
	}
}

func TestDecideLimitHandoff_NoChaining(t *testing.T) {
	// A task that already carries a provider override, or sits deeper in a
	// handoff chain, must NOT hand off again (#674 requirement 5).
	setupHandoffConfig(t, "grok", "")

	if dec := DecideLimitHandoff("claude", true, 1); dec.Handoff {
		t.Fatal("a handoff continuation must not chain another handoff")
	}
	if dec := DecideLimitHandoff("claude", false, 2); dec.Handoff {
		t.Fatal("handoffDepth > 0 marks a continuation; must retry")
	}
}

func TestDecideLimitHandoff_DisabledTargetRetries(t *testing.T) {
	setupHandoffConfig(t, "grok", "")
	viper.Set("provider_enabled_grok", false)

	if dec := DecideLimitHandoff("claude", false, 0); dec.Handoff {
		t.Fatal("a disabled target provider must not receive the handoff")
	}
}

func TestDecideLimitHandoff_InvalidTargetRetries(t *testing.T) {
	setupHandoffConfig(t, "totally-made-up", "")

	if dec := DecideLimitHandoff("claude", false, 0); dec.Handoff {
		t.Fatal("an invalid stored target must fall back to retry, not invent a provider")
	}
}

func TestBuildLimitHandoffPrompt(t *testing.T) {
	i18n.Init("en")
	t.Cleanup(func() { i18n.Init("ko") })

	lines := []string{"step one done\n", "editing internal/queue\n", "rate limit hit\n"}
	got := BuildLimitHandoffPrompt("fix the bug", "grok", "grok-4.5", lines)

	if !strings.Contains(got, "grok/grok-4.5") {
		t.Errorf("prompt must name the new target: %q", got)
	}
	if !strings.Contains(got, "rate limit hit") {
		t.Errorf("prompt must carry the previous progress tail: %q", got)
	}
	if !strings.HasSuffix(got, "fix the bug") {
		t.Errorf("prompt must end with the original request: %q", got)
	}
	if strings.Contains(got, i18n.T().Handoff.ProgressTruncated) {
		t.Errorf("short output must not be marked truncated: %q", got)
	}

	// Model empty → target label is the provider alone.
	got2 := BuildLimitHandoffPrompt("keep going", "embedded", "", nil)
	if !strings.Contains(got2, "embedded") || strings.Contains(got2, "embedded/") {
		t.Errorf("empty model must render provider only: %q", got2)
	}
	if !strings.HasSuffix(got2, "keep going") {
		t.Errorf("prompt must still end with the original request: %q", got2)
	}
}

func TestBuildLimitHandoffPrompt_TruncatesOldOutput(t *testing.T) {
	i18n.Init("en")
	t.Cleanup(func() { i18n.Init("ko") })

	var lines []string
	for i := 0; i < 2000; i++ {
		lines = append(lines, "line filler content to blow past the budget\n")
	}
	got := BuildLimitHandoffPrompt("finish it", "magi", "", lines)

	if !strings.Contains(got, i18n.T().Handoff.ProgressTruncated) {
		t.Errorf("oversized output must carry the truncation marker")
	}
	if len(got) > handoffProgressChars+len("finish it")+len(i18n.T().Handoff.PromptHeaderFmt)+len(i18n.T().Handoff.ProgressTruncated)+500 {
		t.Errorf("prompt grew past its budget: %d bytes", len(got))
	}
}

func TestTailOfOutput(t *testing.T) {
	if got := tailOfOutput(nil, 100); got != "" {
		t.Errorf("nil lines = %q, want empty", got)
	}
	lines := []string{"aaa\n", "bbb\n", "ccc\n"}
	got := tailOfOutput(lines, 5)
	if got != "ccc\n" {
		t.Errorf("budget 5 kept %q, want the last line only", got)
	}
}

// --- tryRateLimitHandoff integration (#675) ---------------------------------
//
// These exercise the real follow-up creation path against an in-memory DB: the
// follow-up must be priority 1 with the target provider/model overrides, the
// parent must finish as COMPLETED (never failed, so the circuit breaker is
// untouched), and a linked issue must move onto the follow-up.

func TestTryRateLimitHandoff_CreatesFollowUpAndCompletesParent(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()

	proj := client.Project.Create().SetName("demo").SetPath("/tmp/demo").SaveX(ctx)
	s := client.Sheep.Create().SetName("hamzzi").SaveX(ctx)
	client.Sheep.UpdateOneID(s.ID).SetProvider("claude").SetProjectID(proj.ID).SaveX(ctx)

	parent := client.Task.Create().
		SetPrompt("do the thing").
		SetStatus(task.StatusRunning).
		SetSheepID(s.ID).
		SaveX(ctx)

	setupHandoffConfig(t, "grok", "grok-4.5")

	var outputLines []string
	coalescer := worker.NewLineCoalescer(func(line string) { outputLines = append(outputLines, line) })
	coalescer.Append("halfway there\n")
	coalescer.Flush()

	idleCalls, completeCalls := 0, 0
	p := &Processor{
		OnStatusChange: func(_, status string) {
			if status == "idle" {
				idleCalls++
			}
		},
		OnTaskComplete: func(_ int, _, _, _ string) { completeCalls++ },
	}

	ok := p.tryRateLimitHandoff("hamzzi", "demo", parent.ID, "do the thing", outputLines, coalescer, "")
	if !ok {
		t.Fatal("expected handoff to succeed")
	}

	// Parent completed, not failed.
	reloaded := client.Task.GetX(ctx, parent.ID)
	if reloaded.Status != task.StatusCompleted {
		t.Errorf("parent status = %s, want completed", reloaded.Status)
	}

	// The handoff notice landed in the parent's saved output.
	joined := strings.Join(reloaded.Output, "")
	if !strings.Contains(joined, "grok/grok-4.5") {
		t.Errorf("parent output missing handoff notice: %q", joined)
	}

	// Exactly one follow-up, priority 1, with provider+model overrides and depth 1.
	followUps := client.Task.Query().Where(task.HasSheepWith(sheep.Name("hamzzi"))).AllX(ctx)
	var fu *ent.Task
	for _, c := range followUps {
		if c.ID != parent.ID {
			fu = c
		}
	}
	if fu == nil {
		t.Fatal("no follow-up task created")
	}
	if fu.Priority != 1 {
		t.Errorf("follow-up priority = %d, want 1", fu.Priority)
	}
	if fu.ProviderOverride != "grok" {
		t.Errorf("follow-up provider_override = %q, want grok", fu.ProviderOverride)
	}
	if fu.Model != "grok-4.5" {
		t.Errorf("follow-up model = %q, want grok-4.5", fu.Model)
	}
	if fu.HandoffDepth != 1 {
		t.Errorf("follow-up handoff_depth = %d, want 1", fu.HandoffDepth)
	}
	if !strings.Contains(fu.Prompt, "do the thing") {
		t.Errorf("follow-up prompt must carry the original request: %q", fu.Prompt)
	}

	if idleCalls != 1 || completeCalls != 1 {
		t.Errorf("callbacks idle=%d complete=%d, want 1/1", idleCalls, completeCalls)
	}

	// Circuit breaker untouched: consecutive_failures stays 0.
	sReloaded := client.Sheep.GetX(ctx, s.ID)
	if sReloaded.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures = %d, want 0 (handoff must not count as failure)", sReloaded.ConsecutiveFailures)
	}
}

func TestTryRateLimitHandoff_SkipsWhenSameProvider(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()
	proj := client.Project.Create().SetName("demo").SetPath("/tmp/demo").SaveX(ctx)
	s := client.Sheep.Create().SetName("hamzzi").SaveX(ctx)
	client.Sheep.UpdateOneID(s.ID).SetProvider("claude").SetProjectID(proj.ID).SaveX(ctx)
	parent := client.Task.Create().SetPrompt("x").SetStatus(task.StatusRunning).SetSheepID(s.ID).SaveX(ctx)

	setupHandoffConfig(t, "claude", "") // target == sheep provider

	var outputLines []string
	coalescer := worker.NewLineCoalescer(func(line string) { outputLines = append(outputLines, line) })
	p := &Processor{}

	if p.tryRateLimitHandoff("hamzzi", "demo", parent.ID, "x", outputLines, coalescer, "") {
		t.Fatal("same provider must fall back to retry (return false)")
	}
	if got := client.Task.GetX(ctx, parent.ID).Status; got != task.StatusRunning {
		t.Errorf("parent must stay running for the retry loop, got %s", got)
	}
	if n := client.Task.Query().CountX(ctx); n != 1 {
		t.Errorf("no follow-up should be created, found %d task(s)", n)
	}
}

func TestTryRateLimitHandoff_InheritsIssue(t *testing.T) {
	client := withTestDB(t)
	ctx := context.Background()
	proj := client.Project.Create().SetName("demo").SetPath("/tmp/demo").SaveX(ctx)
	s := client.Sheep.Create().SetName("hamzzi").SaveX(ctx)
	client.Sheep.UpdateOneID(s.ID).SetProvider("claude").SetProjectID(proj.ID).SaveX(ctx)

	iss := client.Issue.Create().SetTitle("work item").SetProjectID(proj.ID).SaveX(ctx)
	parent := client.Task.Create().
		SetPrompt("x").
		SetStatus(task.StatusRunning).
		SetSheepID(s.ID).
		SetIssueID(iss.ID).
		SaveX(ctx)

	setupHandoffConfig(t, "embedded", "ep-1")

	var outputLines []string
	coalescer := worker.NewLineCoalescer(func(line string) { outputLines = append(outputLines, line) })
	p := &Processor{}

	if !p.tryRateLimitHandoff("hamzzi", "demo", parent.ID, "x", outputLines, coalescer, "") {
		t.Fatal("expected handoff")
	}

	// The follow-up inherits the issue; parent is terminal so the partial
	// unique index (one live task per issue) is satisfied. Only pending/running
	// rows count as "live" — the completed parent keeps its historical link.
	followUps := client.Task.Query().
		Where(task.HasIssueWith(issue.ID(iss.ID)), task.StatusIn(task.StatusPending, task.StatusRunning)).
		AllX(ctx)
	if len(followUps) != 1 {
		t.Fatalf("expected exactly one live task linked to the issue, got %d", len(followUps))
	}
	if followUps[0].ID == parent.ID {
		t.Fatal("the follow-up (not the parent) should hold the issue link")
	}
	if followUps[0].ProviderOverride != "embedded" || followUps[0].Model != "ep-1" {
		t.Errorf("follow-up overrides = (%q,%q), want (embedded,ep-1)",
			followUps[0].ProviderOverride, followUps[0].Model)
	}
}
