package worker

import (
	"context"
	"fmt"

	"github.com/agurrrrr/shepherd/internal/config"
)

// embeddedExecFunc is the signature for the embedded execution function.
// The actual implementation is set by SetEmbeddedExecutor from outside the
// worker package to avoid import cycles (mcp → queue → worker → mcp).
var embeddedExecutor func(
	ctx context.Context,
	sheepName, projectPath string,
	prompt string,
	opts InteractiveOptions,
	cancel context.CancelFunc,
	injectCh <-chan string,
) (*ExecuteResult, error)

// SetEmbeddedExecutor registers the embedded executor function.
// Must be called once during application initialization.
func SetEmbeddedExecutor(fn func(
	ctx context.Context,
	sheepName, projectPath string,
	prompt string,
	opts InteractiveOptions,
	cancel context.CancelFunc,
	injectCh <-chan string,
) (*ExecuteResult, error)) {
	embeddedExecutor = fn
}

func executeWithEmbedded(
	ctx context.Context,
	sheepName, projectPath string,
	prompt string,
	opts InteractiveOptions,
	cancel context.CancelFunc,
) (*ExecuteResult, error) {
	if embeddedExecutor == nil {
		return nil, fmt.Errorf("embedded executor not initialized")
	}

	// Create an inject channel so the user can send mid-execution prompts.
	// Buffer of 16 allows multiple quick injections without blocking.
	injectCh := make(chan string, 16)

	// Register in the running-task registry so StopTask can find and cancel
	// this work. Embedded runs have no subprocess (Cmd == nil); killProcessGroup
	// already guards against nil, so this is safe. The identity token prevents
	// a late-finishing task from clobbering a newer task's entry.
	rt := registerRunningTask(sheepName, cancel, nil)
	rt.InjectCh = injectCh
	defer func() {
		close(injectCh)
		unregisterRunningTask(sheepName, rt)
	}()

	return embeddedExecutor(ctx, sheepName, projectPath, prompt, opts, cancel, injectCh)
}

// BuildSystemPromptForEmbedded builds the system prompt for the embedded provider.
// Composes the same context sections used by other providers.
// The mcpGuide parameter is the project-specific MCP tool guide (pre-built by the caller).
// If mcpGuide is empty, the default hardcoded guide is used.
func BuildSystemPromptForEmbedded(sheepName, projectPath, mcpGuide string) string {
	var sections []string

	// Agent identity
	sections = append(sections,
		"너는 shepherd의 코드 에이전트다. 프로젝트 디렉토리에서 파일 읽기/쓰기/수정, 셸 명령어 실행, MCP 도구 호출을 할 수 있다.")

	// Available tools guide — use project-specific guide if provided
	if mcpGuide != "" {
		sections = append(sections, mcpGuide)
	} else if config.GetBool("include_mcp_guide") {
		sections = append(sections, embeddedMCPGuide())
	}

	// Task history
	if config.GetBool("include_task_history") {
		// The model will call get_history when needed via MCP tools
	}

	// Project skills
	if skillsText := getProjectSkillsSummary(sheepName); skillsText != "" {
		sections = append(sections, fmt.Sprintf("[Project Skills - use skill_load MCP tool for full content]\n%s", skillsText))
	}

	// Sheep memory (reuse existing builder)
	if memText := buildSheepMemorySection(sheepName); memText != "" {
		sections = append(sections, memText)
	}

	// Custom prompt
	if custom := config.GetString("custom_prompt_embedded"); custom != "" {
		sections = append(sections, fmt.Sprintf("[User Custom Instructions]\n%s", custom))
	}

	return joinSections(sections)
}

func embeddedMCPGuide() string {
	return `[Available Shepherd MCP Tools]
Task management:
- task_start: Queue a task (sheep_name, project_name, prompt)
- task_complete: Record task completion (task_id, summary)
- task_error: Record task error (task_id, error)
- get_history: Query project task history (project_name, limit)
- get_status: Get overall system status

Skills:
- skill_load: Load full content of a skill by name (use when you need detailed instructions)

Wiki:
- wiki_read_page: Read a wiki page (project_name, slug)
- wiki_list_pages: List wiki pages for a project (project_name)
- wiki_search: Search wiki pages by query (project_name, query)

Browser automation (PREFERRED over WebFetch for web tasks):
- browser_session_start, browser_open, browser_get_text, browser_click, browser_type, ...
- All browser tools require sheep_name parameter.`
}

func joinSections(sections []string) string {
	var result string
	for i, s := range sections {
		if i > 0 {
			result += "\n\n"
		}
		result += s
	}
	return result
}
