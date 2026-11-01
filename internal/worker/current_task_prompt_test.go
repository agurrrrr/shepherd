package worker

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// TestCurrentTaskIDEntersEveryBuilder pins #722: every provider prompt states
// the run's own task id, so the sheep does not guess it from get_status or
// get_history (task_complete/task_error now refuse any other id). Previews and
// runs outside the queue pass 0 and must not carry the section.
func TestCurrentTaskIDEntersEveryBuilder(t *testing.T) {
	newWorkerTestClient(t)

	for _, guide := range []bool{true, false} {
		viper.Set("include_mcp_guide", guide)
		viper.Set("include_task_history", false)
		viper.Set("include_sheep_memory", false)
		viper.Set("wiki_enabled", false)

		build := func(taskID int) map[string]string {
			return map[string]string{
				"claude streaming": buildSystemContext("양순이", "p", taskID),
				"claude full":      buildPromptWithContext("양순이", "p", taskID),
				"opencode compact": buildPromptCompact("양순이", "p", taskID),
				"opencode full":    buildPromptWithContextUsing("양순이", "p", "custom_prompt_opencode", taskID),
				"pi":               buildPromptWithContextUsing("양순이", "p", "custom_prompt_pi", taskID),
				"grok":             buildPromptWithContextUsing("양순이", "p", "custom_prompt_grok", taskID),
				"embedded":         BuildSystemPromptForEmbedded("양순이", "/ws/bori", "guide", taskID),
			}
		}

		for name, out := range build(4242) {
			if !strings.Contains(out, "[Current Task]\nCurrent task id: #4242.") {
				t.Errorf("include_mcp_guide=%v %s: prompt missing the current task id\n---\n%s", guide, name, out)
			}
		}
		for name, out := range build(0) {
			if strings.Contains(out, "[Current Task]") {
				t.Errorf("include_mcp_guide=%v %s: taskID 0 must not add the section\n---\n%s", guide, name, out)
			}
		}
	}
}

// TestCurrentTaskSectionKeepsUserRequestLast guards the prompt shape: the
// task id goes into the context part, never after the [User Request] block.
func TestCurrentTaskSectionKeepsUserRequestLast(t *testing.T) {
	newWorkerTestClient(t)
	viper.Set("include_mcp_guide", true)
	viper.Set("include_task_history", false)
	viper.Set("include_sheep_memory", false)
	viper.Set("wiki_enabled", false)

	out := buildPromptWithContextUsing("양순이", "do the work", "custom_prompt_pi", 7)
	sec := strings.Index(out, "[Current Task]")
	req := strings.Index(out, "[User Request]\ndo the work")
	if sec < 0 || req < 0 || sec > req {
		t.Fatalf("[Current Task] must come before [User Request] (sec=%d req=%d)\n---\n%s", sec, req, out)
	}

	compact := buildPromptCompact("양순이", "do the work", 7)
	if !strings.HasPrefix(compact, "do the work") {
		t.Errorf("compact prompt must still start with the user request\n---\n%s", compact)
	}
}
