package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/ent/skill"
	"github.com/agurrrrr/shepherd/internal/names"
)

// installFakeShepherdContext registers a deterministic stand-in for
// manager.BuildShepherdContext. The worker package cannot import manager
// (import cycle), so the prompt builders are tested against the callback seam
// exactly as the daemon wires it in cmd/shepherd/main.go (#595).
func installFakeShepherdContext(t *testing.T) {
	t.Helper()
	prev := shepherdContextProvider
	SetShepherdContextProvider(func() string {
		return "[Shepherd Orchestrator — operating instructions]\nfake orchestrator block\n" +
			"[Shepherd Workspace]\n/home/user/code\n" +
			"[Managed Projects]\nbori | /ws/bori | 양순이(claude) idle | pending=0 | running=- | issues todo=1/in_progress=0/testing=0/failed=0\n" +
			"[Autonomy]\nissue create + execute allowed; stop/delete not allowed\n" +
			"[User Shepherd Instructions]\n이슈를 닫기 전에 테스트 통과를 확인하라."
	})
	t.Cleanup(func() { SetShepherdContextProvider(prev) })
}

// promptBuilders595 enumerates every provider path the issue lists. The two
// embedded builders take a projectPath/mcpGuide pair and return the joined
// system prompt; the rest are prompt-with-context builders keyed by the
// custom-prompt config key.
func TestManagerContextEntersEveryBuilder(t *testing.T) {
	newWorkerTestClient(t)
	installFakeShepherdContext(t)

	viper.Set("include_mcp_guide", true)
	viper.Set("include_task_history", false)
	viper.Set("include_sheep_memory", false)
	viper.Set("wiki_enabled", false)

	embedded := BuildSystemPromptForEmbedded(names.ManagerName, "/home/user/code", "guide")
	magi := BuildSystemPromptForMagi(names.ManagerName, "/home/user/code", "guide")

	cases := map[string]string{
		"claude streaming": buildSystemContext(names.ManagerName, "p"),
		"claude full":      buildPromptWithContextUsing(names.ManagerName, "p", "custom_prompt_claude"),
		"opencode compact": buildPromptCompact(names.ManagerName, "p"),
		"opencode full":    buildPromptWithContextUsing(names.ManagerName, "p", "custom_prompt_opencode"),
		"pi":               buildPromptWithContextUsing(names.ManagerName, "p", "custom_prompt_pi"),
		"grok":             buildPromptWithContextUsing(names.ManagerName, "p", "custom_prompt_grok"),
		"embedded":         embedded,
		"magi":             magi,
	}

	for name, out := range cases {
		for _, want := range []string{
			"[Shepherd Orchestrator — operating instructions]",
			"[Shepherd Workspace]\n/home/user/code",
			"[Managed Projects]\nbori | /ws/bori",
			"[Autonomy]\nissue create + execute allowed; stop/delete not allowed\n" +
			"[User Shepherd Instructions]\n이슈를 닫기 전에 테스트 통과를 확인하라.",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: manager prompt missing %q\n---\n%s", name, want, out)
			}
		}
	}
}

// TestOrdinarySheepPromptsUnchanged is the regression guard for the #595 goal
// "일반 양의 프롬프트는 바뀌지 않는다": with the same provider installed, a
// normal sheep's prompts must not contain any manager section.
func TestOrdinarySheepPromptsUnchanged(t *testing.T) {
	client := newWorkerTestClient(t)
	installFakeShepherdContext(t)

	viper.Set("include_mcp_guide", true)
	viper.Set("include_task_history", false)
	viper.Set("include_sheep_memory", false)
	viper.Set("wiki_enabled", false)

	// Give the ordinary sheep a project so the project-skill path is exercised.
	ctx := context.Background()
	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	client.Sheep.Create().SetName("양순이").SetProject(p).SaveX(ctx)

	out := map[string]string{
		"claude streaming": buildSystemContext("양순이", "p"),
		"claude full":      buildPromptWithContextUsing("양순이", "p", "custom_prompt_claude"),
		"opencode compact": buildPromptCompact("양순이", "p"),
		"pi":               buildPromptWithContextUsing("양순이", "p", "custom_prompt_pi"),
		"grok":             buildPromptWithContextUsing("양순이", "p", "custom_prompt_grok"),
		"embedded":         BuildSystemPromptForEmbedded("양순이", "/ws/bori", "guide"),
		"magi":             BuildSystemPromptForMagi("양순이", "/ws/bori", "guide"),
	}

	for name, s := range out {
		for _, banned := range []string{
			"[Shepherd Orchestrator",
			"[Shepherd Workspace]",
			"[Managed Projects]",
			"[Autonomy]",
		} {
			if strings.Contains(s, banned) {
				t.Errorf("%s: ordinary sheep prompt must not contain %q", name, banned)
			}
		}
	}
}

// TestShepherdContextProviderUnset guards the daemon-less path (CLI unit tests,
// a worker used before wiring): the manager falls back to the ordinary shape
// instead of crashing or emitting half a block.
func TestShepherdContextProviderUnset(t *testing.T) {
	newWorkerTestClient(t)
	prev := shepherdContextProvider
	SetShepherdContextProvider(nil)
	t.Cleanup(func() { SetShepherdContextProvider(prev) })

	viper.Set("include_mcp_guide", false)
	viper.Set("include_task_history", false)
	viper.Set("include_sheep_memory", false)
	viper.Set("wiki_enabled", false)

	out := buildSystemContext(names.ManagerName, "p")
	if strings.Contains(out, "[Shepherd Orchestrator") {
		t.Errorf("no context block expected without a provider, got:\n%s", out)
	}
}

// TestManagerGetsGlobalSkillsSummaryOnly covers #595's skill rule: the manager
// has no project, so it sees enabled GLOBAL skills but never another project's
// skills. An ordinary sheep still gets global + its own project's skills.
func TestManagerGetsGlobalSkillsSummaryOnly(t *testing.T) {
	client := newWorkerTestClient(t)
	ctx := context.Background()

	client.Skill.Create().SetName("global-notify").SetDescription("discord webhook").
		SetContent("# notify").SetScope(skill.ScopeGlobal).SetEnabled(true).SaveX(ctx)

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	client.Skill.Create().SetName("bori-only").SetDescription("bori workflow").
		SetContent("# bori").SetScope(skill.ScopeProject).SetEnabled(true).SetProject(p).SaveX(ctx)

	// The manager sheep row exists but has no project edge.
	client.Sheep.Create().SetName(names.ManagerName).SaveX(ctx)
	client.Sheep.Create().SetName("양순이").SetProject(p).SaveX(ctx)

	mgr := skillsSummaryFor(names.ManagerName)
	if !strings.Contains(mgr, "global-notify") {
		t.Errorf("manager must see enabled global skills:\n%s", mgr)
	}
	if strings.Contains(mgr, "bori-only") {
		t.Errorf("manager must not see project-scoped skills:\n%s", mgr)
	}

	sheep := skillsSummaryFor("양순이")
	if !strings.Contains(sheep, "global-notify") || !strings.Contains(sheep, "bori-only") {
		t.Errorf("ordinary sheep keeps global + own project skills:\n%s", sheep)
	}

	// Disabled global skills stay out for everyone.
	disabled := client.Skill.Create().SetName("sleepy").SetDescription("off").
		SetContent("# off").SetScope(skill.ScopeGlobal).SetEnabled(false).SaveX(ctx)
	_ = disabled
	if strings.Contains(skillsSummaryFor(names.ManagerName), "sleepy") {
		t.Error("disabled global skill must not appear")
	}

	// getProjectSkillsSummary still returns "" for the manager (no project) —
	// skillsSummaryFor is what adds the global fallback.
	if got := getProjectSkillsSummary(names.ManagerName); got != "" {
		t.Errorf("manager has no project skills, got %q", got)
	}
}

// TestPreviewSystemPromptForManager checks the settings-preview path: asking
// for the manager name must render the orchestrator block in every preview
// mode, including the embedded and magi modes added for #595.
func TestPreviewSystemPromptForManager(t *testing.T) {
	newWorkerTestClient(t)
	installFakeShepherdContext(t)

	viper.Set("include_mcp_guide", true)
	viper.Set("include_task_history", false)
	viper.Set("include_sheep_memory", false)
	viper.Set("wiki_enabled", false)
	viper.Set("opencode_compact_prompt", true)
	viper.Set("workspace_path", "/home/user/code")

	m := PreviewSystemPrompt(names.ManagerName)
	for mode, out := range m {
		if !strings.Contains(out, "[Shepherd Orchestrator — operating instructions]") {
			t.Errorf("preview[%s]: manager preview missing the orchestrator block", mode)
		}
	}

	if !strings.Contains(m["embedded"], "현재 작업 디렉토리(프로젝트 루트): /home/user/code") {
		t.Errorf("embedded preview must use the workspace as its working directory:\n%s", m["embedded"])
	}

	// An ordinary sheep's preview stays clean.
	m2 := PreviewSystemPrompt("양순이")
	for mode, out := range m2 {
		if strings.Contains(out, "[Shepherd Orchestrator") {
			t.Errorf("preview[%s]: ordinary sheep preview must not contain the manager block", mode)
		}
	}
}
