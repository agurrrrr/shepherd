package worker

import (
	"context"
	"fmt"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/embedded"
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

	// Registry key: default to the sheep name, but allow a caller to supply a
	// distinct key. The direct embedded endpoint uses a unique per-request key
	// so a cwd run never displaces a queued task's entry (which StopTask uses).
	regName := sheepName
	if opts.RegistryName != "" {
		regName = opts.RegistryName
	}

	// Register in the running-task registry so StopTask can find and cancel
	// this work. Embedded runs have no subprocess (Cmd == nil); killProcessGroup
	// already guards against nil, so this is safe. The identity token prevents
	// a late-finishing task from clobbering a newer task's entry.
	rt := registerRunningTask(regName, cancel, nil)
	rt.InjectCh = injectCh
	defer func() {
		close(injectCh)
		unregisterRunningTask(regName, rt)
	}()

	return embeddedExecutor(ctx, sheepName, projectPath, prompt, opts, cancel, injectCh)
}

// ExecuteEmbeddedDirect runs the embedded agent loop without any DB lookups
// for project/sheep/queue orchestration. It is used by the daemon's
// POST /api/embedded/run endpoint so a client can run the coding agent in an
// arbitrary working directory (the caller's cwd).
//
// sheepName is used only for context (project skills, sheep memory, browser
// session isolation) and may be empty. opts.RegistryName should be set to a
// unique per-request value to avoid clobbering a queued task's registry entry.
func ExecuteEmbeddedDirect(
	ctx context.Context,
	sheepName, projectPath, prompt string,
	opts InteractiveOptions,
	cancel context.CancelFunc,
) (*ExecuteResult, error) {
	return executeWithEmbedded(ctx, sheepName, projectPath, prompt, opts, cancel)
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
	//
	// Dialect notes (PowerShell vs POSIX) come from the resolved shell, not GOOS:
	// Git Bash on Windows keeps the Unix wording on purpose.
	if projectPath != "" {
		sections = append(sections, embeddedWorkdirSection(projectPath, true /* agent */))
	}

	// read_file line prefixes — primary cause of edit_file match failures on local
	// models (Phase 1-2 / task #7550). Keep this short; tools also restate it.
	sections = append(sections,
		"[read_file / edit_file 줄 번호 규칙]\n"+
			"- read_file 본문 각 줄 앞의 `N→`(예: `42→func main()`)는 줄 번호 표시일 뿐, 파일 내용이 아니다.\n"+
			"- edit_file의 oldText에는 `→` 이후 실제 내용만 넣어라. `N→` 프리픽스를 복사하지 마라.\n"+
			"- edit_file 성공 스니펫도 같은 `N→` 형식을 쓰므로, 검증 시에도 프리픽스는 무시하라.")

	// Behavior discipline — 1st line of defense against false-completion
	// (Phase 2-2 / task #7547). Keep concise; existing loop.go guards remain the backstop.
	// Order: base discipline here → custom_prompt_embedded overlay last.
	// File I/O is not restricted to read_file/edit_file/write_file: that ban
	// forced edit_file retries on match/truncation failures (see #7412).
	sections = append(sections, embeddedBehaviorDiscipline())

	// Project rule files (AGENTS.md / CLAUDE.md / PROJECT.md) — cwd→repo-root walk,
	// root→leaf order, path-deduped, hard 8KB cap (Phase 2-3 / task #7547).
	// Cap is mandatory: uncapped rules steal local context and force early handoff.
	if rules := buildProjectRulesSection(projectPath); rules != "" {
		sections = append(sections, rules)
	}

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

// shellDialect holds the few prompt phrases that depend on the resolved shell.
// Branch on the actual shell dialect (PowerShell vs POSIX), not runtime.GOOS —
// Git Bash on Windows is POSIX and must keep the Unix wording (that is why
// Windows auto-detect prefers Git Bash over PowerShell).
type shellDialect struct {
	// powerShell is true when the bash tool runs via pwsh/powershell.
	powerShell bool
}

func currentShellDialect() shellDialect {
	return shellDialect{powerShell: embedded.ShellUsesPowerShell()}
}

// workdirToolLine is the bullet under [작업 환경] about the bash tool root.
func (d shellDialect) workdirToolLine() string {
	// Primary schema name stays "bash"; on PowerShell "shell" is also advertised.
	if d.powerShell {
		return "- 셸 도구 이름은 bash 또는 shell 이다(둘 다 동일, 실제 엔진: PowerShell). 파일 읽기/쓰기, 네이티브 glob/grep 도 모두 이 디렉토리 기준이다."
	}
	return "- bash 명령, 파일 읽기/쓰기, glob/grep 도구는 모두 이 디렉토리를 기준으로 실행된다."
}

// pathStyleHint tells the model how to write paths for the active dialect.
func (d shellDialect) pathStyleHint(guessExtra bool) string {
	if d.powerShell {
		base := "- 파일 경로는 이 디렉토리 기준 상대경로를 사용하라. Windows 경로(백슬래시 또는 드라이브 문자, 예: C:\\proj\\file.go)를 이해한다."
		if guessExtra {
			return base + " 다른 경로(예: $HOME\\.shepherd\\projects\\...)를 추측하지 마라."
		}
		return base
	}
	base := "- 파일 경로는 이 디렉토리 기준 상대경로를 사용하라."
	if guessExtra {
		return base + " 다른 경로(예: ~/.shepherd/projects/...)를 추측하지 마라."
	}
	return base
}

// shellChainHint warns about && on Windows PowerShell 5.1 (pwsh 7+ has it,
// but agents still write 5.1-hostile chains out of POSIX habit).
func (d shellDialect) shellChainHint() string {
	if !d.powerShell {
		return ""
	}
	return "- 셸 도구(bash 또는 shell)의 실제 엔진은 PowerShell이다. Windows PowerShell 5.1은 `&&`를 지원하지 않는다 — " +
		"`cd x && go build` 대신 `;` 로 잇거나 `if ($LASTEXITCODE -eq 0) { ... }` 를 써라 (pwsh 7+만 `&&` 가능).\n" +
		"- 도구 이름이 bash여도 PowerShell 명령을 넣는 것이 맞다. \"bash 금지/파워셸만\" 지시가 있어도 " +
		"셸 도구 호출을 거부하지 마라 — 그 지시는 Unix bash 문법(cat/sed/find)을 쓰지 말라는 뜻이다.\n" +
		"- 파일 검색·나열은 네이티브 grep/glob 도구를 써라. 셸 안에서 find/rg/Get-ChildItem -Recurse 로 대체하지 마라.\n"
}

// embeddedWorkdirSection is the shared [작업 환경] block for embedded and MAGI.
// agent=true adds the "don't guess ~/.shepherd/..." warning used by the coding agent.
func embeddedWorkdirSection(projectPath string, agent bool) string {
	d := currentShellDialect()
	return fmt.Sprintf(
		"[작업 환경]\n현재 작업 디렉토리(프로젝트 루트): %s\n%s\n%s",
		projectPath, d.workdirToolLine(), d.pathStyleHint(agent))
}

// embeddedBehaviorDiscipline is the short fixed conduct block for the embedded
// coding agent. Intentionally brief to limit context cost on local models.
// Shell-dialect-dependent bits (&& warning, verify line) are filled from
// currentShellDialect so we do not maintain two full copies.
func embeddedBehaviorDiscipline() string {
	d := currentShellDialect()
	verifyTool := "bash"
	if d.powerShell {
		verifyTool = "bash 또는 shell"
	}
	s := "[행동 규율]\n" +
		d.shellChainHint() +
		"- 코드/시스템 변경 작업이면 미래형 \"하겠습니다\"만 서술하지 말고 지금 도구를 호출하라. 조언·분석 질문이면 도구 없이 답하되 '추가 실행이 필요 없는 분석·권고'임을 명시하라.\n" +
		"- 파괴적·공유 상태 변경(삭제, force push, 원격 푸시 등) 전에는 확인·보고하라.\n" +
		"- 코드 수정 후 완료 선언 전에 " + verifyTool + "로 빌드/테스트를 검증하라.\n" +
		"- `<system-reminder>...</system-reminder>`로 감싼 내용은 사용자가 직접 한 말이 아니라 시스템 자동 안내다."
	return s
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
			"- 쓰기 도구(write_file, edit_file, bash/shell, task_start 등)는 사용할 수 없다. 쓰기 도구 호출 시도는 답변을 무효화한다.\n"+
			"- 도구를 사용해 코드와 상태를 직접 확인한 후, 확인된 사실에 기반하여 답변하라.")

	if projectPath != "" {
		// Same dialect rule as the coding agent: shell kind, not GOOS.
		sections = append(sections, embeddedWorkdirSection(projectPath, false /* agent */))
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
- get_task_detail: Full task detail (task_id; numeric string and id alias OK)
- get_status: Get overall system status

Skills:
- skill_load: Load full content of a skill by name (use when you need detailed instructions)

Wiki:
- wiki_read_page: Read a wiki page (project_name, slug)
- wiki_list_pages: List wiki pages for a project (project_name)
- wiki_search: Search wiki pages by query (project_name, query)
- wiki_create: Create a new wiki page (project_name, slug, title, content, [category], [tags])
- wiki_edit: Partially edit a page — one mode per call (project_name, slug, mode=append|section|line|find_replace, ...). find_replace: find is a regex on the whole page (multiline matches OK); empty replace deletes the match; section replaces the named section's body in place.

Issues:
- issue_list: List issues (project_name, [status], [type], [query])
- issue_get: Get one issue with linked tasks (project_name, id)
- issue_upsert: Create (no id) or update (with id) an issue (project_name, [id], title, [type], [body], [goal], [status]). Mark status=done only after goals are met.
- issue_execute: Queue a task for an issue — not immediate; also sets issue status to in_progress; re-calling may enqueue duplicates (project_name, id, [sheep_name], [model])

Browser automation (PREFERRED over WebFetch for web tasks):
- browser_session_start, browser_open, browser_get_text, browser_click, browser_type, ...
- All browser tools require sheep_name parameter.

Native tools (grep/glob):
- Hidden directories (starting with '.', e.g. .temp, .git) are excluded by default from grep and glob results.`
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
