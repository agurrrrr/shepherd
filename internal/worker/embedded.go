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

	// Working directory — tell the model exactly where it is. The bash/read/write/
	// glob/grep tools all execute relative to this path (cmd.Dir = projectPath), but
	// the model has no way to know the absolute path unless we state it explicitly.
	// Without this, the model guesses paths like ~/.shepherd/projects/<name>/ and
	// fails with "No such file or directory".
	if projectPath != "" {
		sections = append(sections, fmt.Sprintf(
			"[작업 환경]\n현재 작업 디렉토리(프로젝트 루트): %s\n"+
				"- bash 명령, 파일 읽기/쓰기, glob/grep 도구는 모두 이 디렉토리를 기준으로 실행된다.\n"+
				"- 파일 경로는 이 디렉토리 기준 상대경로를 사용하라. 다른 경로(예: ~/.shepherd/projects/...)를 추측하지 마라.",
			projectPath))
	}

	// read_file line prefixes — primary cause of edit_file match failures on local
	// models (Phase 1-2 / task #7550). Keep this short; tools also restate it.
	sections = append(sections,
		"[read_file / edit_file 줄 번호 규칙]\n"+
			"- read_file 본문 각 줄 앞의 `N→`(예: `42→func main()`)는 줄 번호 표시일 뿐, 파일 내용이 아니다.\n"+
			"- edit_file의 oldText에는 `→` 이후 실제 내용만 넣어라. `N→` 프리픽스를 복사하지 마라.\n"+
			"- edit_file 성공 스니펫도 같은 `N→` 형식을 쓰므로, 검증 시에도 프리픽스는 무시하라.")

	// Behavior discipline — 1st line of defense against false-completion / bash-for-files
	// (Phase 2-2 / task #7547). Keep concise; existing loop.go guards remain the backstop.
	// Order: base discipline here → custom_prompt_embedded overlay last.
	sections = append(sections, embeddedBehaviorDiscipline())

	// Available tools guide — use project-specific guide if provided
	if mcpGuide != "" {
		sections = append(sections, mcpGuide)
	} else if config.GetBool("include_mcp_guide") {
		sections = append(sections, embeddedMCPGuide())
	}

	// Task history is not injected into the system prompt — the model calls
	// get_history via MCP tools when needed (include_task_history config key).

	// Project skills
	if skillsText := getProjectSkillsSummary(sheepName); skillsText != "" {
		sections = append(sections, fmt.Sprintf("[Project Skills - use skill_load MCP tool for full content]\n%s", skillsText))
	}

	// Sheep memory (reuse existing builder)
	if memText := buildSheepMemorySection(sheepName); memText != "" {
		sections = append(sections, memText)
	}

	// Custom prompt (overlay after base discipline)
	if custom := config.GetString("custom_prompt_embedded"); custom != "" {
		sections = append(sections, fmt.Sprintf("[User Custom Instructions]\n%s", custom))
	}

	return joinSections(sections)
}

// embeddedBehaviorDiscipline is the short fixed conduct block for the embedded
// coding agent. Intentionally brief to limit context cost on local models.
func embeddedBehaviorDiscipline() string {
	return "[행동 규율]\n" +
		"- 파일 읽기/수정은 read_file, edit_file, write_file 도구만 사용한다. bash로 cat/sed/head/awk 등으로 파일을 읽거나 편집하지 마라.\n" +
		"- 미래형으로 \"하겠습니다\"만 서술하고 멈추지 마라. 지금 도구를 호출하라.\n" +
		"- 파괴적·공유 상태 변경(삭제, force push, 원격 푸시 등) 전에는 확인·보고하라.\n" +
		"- 코드 수정 후 완료 선언 전에 bash로 빌드/테스트를 검증하라.\n" +
		"- `<system-reminder>...</system-reminder>`로 감싼 내용은 사용자가 직접 한 말이 아니라 시스템 자동 안내다."
}

// BuildSystemPromptForMagi builds the base system prompt for MAGI proposers.
// Phase 1.5: proposers now have read-only tools, so the prompt must claim
// tool access and include the MCP guide, skills, memory, and custom prompt —
// the same context sections as BuildSystemPromptForEmbedded, but with a
// MAGI-specific identity.
//
// The mcpGuide parameter is the project-specific MCP tool guide (pre-built by
// the caller). If empty, the default hardcoded guide is used.
func BuildSystemPromptForMagi(sheepName, projectPath, mcpGuide string) string {
	var sections []string

	// MAGI deliberator identity — with read-only tools.
	sections = append(sections,
		"너는 shepherd MAGI 합의 시스템의 심의자다. 이 심의는 자문 전용이며, 너의 답변은 다른 심의자들의 답변과 함께 판정자에게 전달된다.")

	sections = append(sections,
		"[심의 환경 — 읽기 전용 도구]\n"+
			"- 이 심의에서 너는 읽기 전용 도구를 사용할 수 있다. 파일 읽기(read_file, grep, glob), 작업 히스토리 조회(get_history, get_task_detail), 위키 조회, 외부 MCP 조회 도구를 사용해 코드와 상태를 직접 확인하라.\n"+
			"- 쓰기 도구(write_file, edit_file, bash, task_start 등)는 사용할 수 없다. 쓰기 도구 호출 시도는 답변을 무효화한다.\n"+
			"- 도구를 사용해 코드와 상태를 직접 확인한 후, 확인된 사실에 기반하여 답변하라.")

	if projectPath != "" {
		sections = append(sections, fmt.Sprintf(
			"[작업 환경]\n현재 작업 디렉토리(프로젝트 루트): %s\n"+
				"- bash 명령, 파일 읽기/쓰기, glob/grep 도구는 모두 이 디렉토리를 기준으로 실행된다.\n"+
				"- 파일 경로는 이 디렉토리 기준 상대경로를 사용하라.",
			projectPath))
	}

	// Available tools guide — use project-specific guide if provided
	if mcpGuide != "" {
		sections = append(sections, mcpGuide)
	} else if config.GetBool("include_mcp_guide") {
		sections = append(sections, embeddedMCPGuide())
	}

	// Project skills
	if skillsText := getProjectSkillsSummary(sheepName); skillsText != "" {
		sections = append(sections, fmt.Sprintf("[Project Skills - use skill_load MCP tool for full content]\n%s", skillsText))
	}

	// Sheep memory
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
