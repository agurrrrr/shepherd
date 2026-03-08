package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/envutil"
	"github.com/agurrrrr/shepherd/internal/skill"
)

// InputHandler is a function that gets user input when Claude asks a question.
type InputHandler func(prompt string) (string, error)

// OutputHandler is a function that displays Claude's output to the user.
type OutputHandler func(output string)

// InteractiveOptions contains options for interactive execution.
type InteractiveOptions struct {
	Timeout       time.Duration
	OnOutput      OutputHandler
	OnInput       InputHandler
	ShowRawOutput bool // Show raw output (for debugging)
}

// RunningTask contains information about a running task
type RunningTask struct {
	SheepName    string
	Cancel       context.CancelFunc
	Cmd          *exec.Cmd
	TaskID       int
	OutputLines  []string
	outputMu     sync.Mutex
}

// Running task management
var (
	runningTasks   = make(map[string]*RunningTask)
	runningTasksMu sync.RWMutex
)

// StopTaskResult contains the result of stopping a task
type StopTaskResult struct {
	TaskID      int
	OutputLines []string
}

// StopTask stops the running task for the specified sheep.
// Returns the stopped task's ID and collected output.
func StopTask(sheepName string) (*StopTaskResult, error) {
	runningTasksMu.Lock()
	task, ok := runningTasks[sheepName]
	if ok {
		delete(runningTasks, sheepName)
	}
	runningTasksMu.Unlock()

	if !ok {
		return nil, fmt.Errorf("no running task for '%s'", sheepName)
	}

	// Collect output (before process termination)
	task.outputMu.Lock()
	output := make([]string, len(task.OutputLines))
	copy(output, task.OutputLines)
	taskID := task.TaskID
	task.outputMu.Unlock()

	// Cancel context
	if task.Cancel != nil {
		task.Cancel()
	}

	// Force kill process
	if task.Cmd != nil && task.Cmd.Process != nil {
		task.Cmd.Process.Kill()
	}

	// Restore status
	ctx := context.Background()
	client := db.Client()
	_, _ = client.Sheep.Update().
		Where(sheep.Name(sheepName)).
		SetStatus(sheep.StatusIdle).
		Save(ctx)

	return &StopTaskResult{
		TaskID:      taskID,
		OutputLines: output,
	}, nil
}

// IsTaskRunning checks if a task is running for the specified sheep.
func IsTaskRunning(sheepName string) bool {
	runningTasksMu.RLock()
	defer runningTasksMu.RUnlock()
	_, ok := runningTasks[sheepName]
	return ok
}

// registerRunningTask registers a running task
func registerRunningTask(sheepName string, cancel context.CancelFunc, cmd *exec.Cmd) {
	runningTasksMu.Lock()
	defer runningTasksMu.Unlock()
	runningTasks[sheepName] = &RunningTask{
		SheepName: sheepName,
		Cancel:    cancel,
		Cmd:       cmd,
	}
}

// SetRunningTaskID sets the task ID for the running task.
func SetRunningTaskID(sheepName string, taskID int) {
	runningTasksMu.RLock()
	task, ok := runningTasks[sheepName]
	runningTasksMu.RUnlock()
	if ok {
		task.TaskID = taskID
	}
}

// AppendOutput appends output text to the running task's output buffer.
func AppendOutput(sheepName string, text string) {
	runningTasksMu.RLock()
	task, ok := runningTasks[sheepName]
	runningTasksMu.RUnlock()
	if ok {
		task.outputMu.Lock()
		task.OutputLines = append(task.OutputLines, text)
		task.outputMu.Unlock()
	}
}

// GetRunningTaskOutput returns the collected output for a running task.
func GetRunningTaskOutput(sheepName string) (int, []string) {
	runningTasksMu.RLock()
	task, ok := runningTasks[sheepName]
	runningTasksMu.RUnlock()
	if !ok {
		return 0, nil
	}
	task.outputMu.Lock()
	defer task.outputMu.Unlock()
	output := make([]string, len(task.OutputLines))
	copy(output, task.OutputLines)
	return task.TaskID, output
}

// unregisterRunningTask unregisters a running task
func unregisterRunningTask(sheepName string) {
	runningTasksMu.Lock()
	defer runningTasksMu.Unlock()
	delete(runningTasks, sheepName)
}

// DefaultInteractiveOptions returns default interactive options.
func DefaultInteractiveOptions(onOutput OutputHandler, onInput InputHandler) InteractiveOptions {
	return InteractiveOptions{
		Timeout:       30 * time.Minute, // Longer timeout for interactive mode
		OnOutput:      onOutput,
		OnInput:       onInput,
		ShowRawOutput: false,
	}
}

// ExecuteInteractive runs AI agent (Claude Code or Local LLM) in interactive mode.
// It streams output to the user and handles input when Claude asks questions.
func ExecuteInteractive(sheepName, prompt string, opts InteractiveOptions) (*ExecuteResult, error) {
	bgCtx := context.Background()
	client := db.Client()

	// Look up the sheep
	s, err := client.Sheep.Query().
		Where(sheep.Name(sheepName)).
		WithProject().
		Only(bgCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to query sheep: %w", err)
	}

	// Check project assignment
	proj, err := s.Edges.ProjectOrErr()
	if err != nil {
		return nil, fmt.Errorf("no project assigned to '%s'", sheepName)
	}

	// Change status to working
	_, err = client.Sheep.Update().
		Where(sheep.Name(sheepName)).
		SetStatus(sheep.StatusWorking).
		Save(bgCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to change status: %w", err)
	}

	// Create cancellable context
	ctx, cancel := context.WithTimeout(bgCtx, opts.Timeout)
	defer cancel()
	defer unregisterRunningTask(sheepName)

	var result *ExecuteResult
	var execErr error

	// Use different AI based on provider
	if opts.OnOutput != nil {
		emoji := ProviderEmoji(s.Provider)
		opts.OnOutput(fmt.Sprintf("%s %s\n", emoji, s.Name))
	}

	// Check session reuse config
	sessionID := s.SessionID
	if !config.GetBool("session_reuse") {
		sessionID = ""
	}

	switch s.Provider {
	case sheep.ProviderOpencode:
		// OpenCode: always start fresh session (local LLMs have small context windows,
		// reusing sessions causes token count to grow unboundedly)
		result, execErr = executeWithOpenCode(ctx, sheepName, proj.Path, "", prompt, opts, cancel)
	case sheep.ProviderAuto:
		// auto mode: default Claude, fallback to OpenCode on failure
		result, execErr = executeWithClaude(ctx, sheepName, proj.Path, sessionID, prompt, opts, cancel)
		if execErr != nil && IsRateLimitError(execErr) {
			result, execErr = executeWithOpenCode(ctx, sheepName, proj.Path, "", prompt, opts, cancel)
		}
	default: // claude
		result, execErr = executeWithClaude(ctx, sheepName, proj.Path, sessionID, prompt, opts, cancel)
	}

	// Restore status
	updateQuery := client.Sheep.Update().
		Where(sheep.Name(sheepName)).
		SetStatus(sheep.StatusIdle)

	if result != nil && result.SessionID != "" {
		updateQuery = updateQuery.SetSessionID(result.SessionID)
	}

	_, _ = updateQuery.Save(bgCtx)

	// User cancelled
	if ctx.Err() == context.Canceled {
		return nil, fmt.Errorf("task was cancelled")
	}

	if execErr != nil {
		return nil, execErr
	}

	return result, nil
}

// executeWithClaude runs Claude Code.
func executeWithClaude(ctx context.Context, sheepName, projectPath, sessionID, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	if config.GetBool("auto_approve") {
		return executeWithStreaming(ctx, sheepName, projectPath, sessionID, prompt, opts, cancel)
	}
	return executeInteractiveWithPty(ctx, sheepName, projectPath, sessionID, prompt, opts, cancel)
}


// executeWithOpenCode runs tasks via OpenCode CLI.
func executeWithOpenCode(ctx context.Context, sheepName, projectPath, sessionID, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	// Use OpenCode run command — let opencode use its own config for model selection
	args := []string{
		"run",
		"--format", "json",
	}

	// Resume session
	if sessionID != "" {
		args = append(args, "-s", sessionID)
	}

	// Add prompt (compact for local LLMs with limited context)
	args = append(args, buildPromptCompact(sheepName, prompt))

	cmd := exec.CommandContext(ctx, config.GetOpenCodeBinary(), args...)
	cmd.Dir = projectPath
	envutil.SetCleanEnv(cmd)

	// Register running task
	registerRunningTask(sheepName, cancel, cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start OpenCode: %w", err)
	}

	var outputBuilder strings.Builder
	var newSessionID string
	var mu sync.Mutex

	// Wait for goroutines to complete with WaitGroup
	var wg sync.WaitGroup
	wg.Add(2)

	// Read stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			mu.Lock()
			outputBuilder.WriteString(line + "\n")
			mu.Unlock()

			// Parse OpenCode JSON events
			parsed, sid := parseOpenCodeLine(line)
			if sid != "" {
				mu.Lock()
				newSessionID = sid
				mu.Unlock()
			}
			if opts.OnOutput != nil && parsed != "" {
				opts.OnOutput(parsed + "\n")
			}
		}
	}()

	// Read stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if opts.OnOutput != nil && strings.TrimSpace(line) != "" {
				opts.OnOutput("⚠️ " + line + "\n")
			}
		}
	}()

	// Wait for goroutines to complete, then call cmd.Wait
	wg.Wait()

	err = cmd.Wait()

	mu.Lock()
	fullOutput := outputBuilder.String()
	sid := newSessionID
	mu.Unlock()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("OpenCode execution timeout (%v)", opts.Timeout)
		}
		// Check for rate limit
		errStr := strings.ToLower(fullOutput + " " + err.Error())
		if strings.Contains(errStr, "rate limit") ||
			strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "too many requests") ||
			strings.Contains(errStr, "limit exceeded") {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
		// Try to parse output even on error
		result := parseOpenCodeOutput(fullOutput)
		if result != nil && result.Result != "" {
			result.SessionID = sid
			return result, nil
		}
		return nil, fmt.Errorf("OpenCode execution failed: %w\noutput: %s", err, truncateStr(fullOutput, 500))
	}

	result := parseOpenCodeOutput(fullOutput)
	result.SessionID = sid

	// Check for OpenCode error events in output (exit code 0 but error JSON)
	if result.Result == "" {
		if errMsg := extractOpenCodeError(fullOutput); errMsg != "" {
			return nil, fmt.Errorf("OpenCode error: %s", errMsg)
		}
		return nil, fmt.Errorf("OpenCode returned empty result")
	}

	return result, nil
}

// extractOpenCodeError checks if the output contains an OpenCode error event.
// OpenCode may exit with code 0 even on errors (e.g., connection failures).
func extractOpenCodeError(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Error struct {
				Name string `json:"name"`
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type == "error" {
			msg := event.Error.Data.Message
			if msg == "" {
				msg = event.Error.Name
			}
			return msg
		}
	}
	return ""
}

// parseOpenCodeLine parses a single OpenCode JSON output line.
// OpenCode event types: step_start, tool_use, text, step_finish, error
func parseOpenCodeLine(line string) (text string, sessionID string) {
	if !strings.HasPrefix(line, "{") {
		return line, ""
	}

	var event struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		Part      struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Tool  string `json:"tool"`
			State struct {
				Status string `json:"status"`
				Input  struct {
					Command     string `json:"command"`
					Description string `json:"description"`
					FilePath    string `json:"file_path"`
					Pattern     string `json:"pattern"`
				} `json:"input"`
				Output string `json:"output"`
				Title  string `json:"title"`
			} `json:"state"`
		} `json:"part"`
		Error struct {
			Name string `json:"name"`
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}

	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return "", ""
	}

	// Extract session ID
	if event.SessionID != "" {
		sessionID = event.SessionID
	}

	switch event.Type {
	case "text":
		if event.Part.Text != "" {
			text = event.Part.Text
		}
	case "tool_use":
		toolName := event.Part.Tool
		if toolName != "" {
			toolInfo := fmt.Sprintf("🔧 %s", toolName)
			input := event.Part.State.Input
			if input.Description != "" {
				toolInfo += fmt.Sprintf(" → %s", input.Description)
			} else if input.Command != "" {
				cmd := input.Command
				if len(cmd) > 80 {
					cmd = cmd[:80] + "..."
				}
				toolInfo += fmt.Sprintf(" → %s", cmd)
			} else if input.FilePath != "" {
				toolInfo += fmt.Sprintf(" → %s", input.FilePath)
			} else if input.Pattern != "" {
				toolInfo += fmt.Sprintf(" → %s", input.Pattern)
			} else if event.Part.State.Title != "" {
				toolInfo += fmt.Sprintf(" → %s", event.Part.State.Title)
			}
			// Show truncated output for completed tools
			if event.Part.State.Status == "completed" && event.Part.State.Output != "" {
				output := event.Part.State.Output
				lines := strings.Split(output, "\n")
				if len(lines) > 5 {
					output = strings.Join(lines[:5], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-5)
				} else if len(output) > 500 {
					output = output[:500] + "..."
				}
				toolInfo += "\n   " + strings.ReplaceAll(output, "\n", "\n   ")
			}
			text = toolInfo
		}
	case "error":
		errMsg := event.Error.Data.Message
		if errMsg == "" {
			errMsg = event.Error.Name
		}
		if errMsg != "" {
			text = "❌ " + errMsg
		}
	}

	return text, sessionID
}

// parseOpenCodeOutput parses the complete OpenCode JSON output.
// Only "text" type events are used for the final result (not tool output).
func parseOpenCodeOutput(output string) *ExecuteResult {
	result := &ExecuteResult{}
	var lastText string

	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var msg struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionID"`
			Part      struct {
				Text string `json:"text"`
			} `json:"part"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		if msg.SessionID != "" {
			result.SessionID = msg.SessionID
		}
		// Only text events count as final result (skip tool output)
		if msg.Type == "text" && msg.Part.Text != "" {
			lastText = msg.Part.Text
		}
	}

	result.Result = lastText
	return result
}

// IsRateLimitError checks if the error is a rate limit error.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "hit your limit") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "too many requests") ||
		strings.Contains(errStr, "limit exceeded")
}

// buildPromptWithGuide adds MCP tool usage guide and recent task context to the prompt.
func buildPromptWithGuide(prompt string) string {
	return buildPromptWithContext("", prompt)
}

// buildPromptCompact builds a prompt for OpenCode (local LLMs).
// User prompt comes FIRST to ensure the model focuses on the actual request,
// with MCP tool guide, task history, and project skills appended as context.
// Compact version: shorter MCP guide, same task history depth, skills included.
func buildPromptCompact(sheepName, prompt string) string {
	var sb strings.Builder

	// User request first — prevents system instructions from overwhelming the actual request
	sb.WriteString(prompt)

	sb.WriteString("\n\n---\n")

	if config.GetBool("include_mcp_guide") {
		sb.WriteString(`[Available Shepherd MCP Tools]
Task management: task_complete (task_id, summary), task_error (task_id, error), get_history (project_name, limit), get_status
Browser automation: browser_session_start (sheep_name), browser_open, browser_click, browser_type, browser_get_text, browser_get_html, browser_screenshot, browser_session_stop
For web tasks, use browser tools instead of WebFetch.

`)
	}

	if config.GetBool("include_task_history") {
		if sheepName != "" {
			if ctx := getRecentTaskContext(sheepName); ctx != "" {
				sb.WriteString(ctx)
				sb.WriteString("\n")
			}
		}
	}

	if sheepName != "" {
		if skillsText := getProjectSkills(sheepName); skillsText != "" {
			sb.WriteString(skillsText)
			sb.WriteString("\n")
		}
	}

	if config.GetBool("include_mcp_guide") {
		sb.WriteString("If you need details of previous tasks, use get_history tool.\n")
	}

	return sb.String()
}

// buildSystemContext returns the system-level context (MCP guide, task history, skills)
// separated from the user prompt. Used by streaming mode to pass via --append-system-prompt.
func buildSystemContext(sheepName string) string {
	var sb strings.Builder

	if config.GetBool("include_mcp_guide") {
		sb.WriteString(`For browser automation tasks, always use shepherd MCP tools:
- browser_session_start: Start browser session (sheep_name required)
- browser_open: Open URL
- browser_click, browser_type: Element interaction
- browser_get_text, browser_get_html: Information extraction
- browser_screenshot: Capture screenshot
- browser_session_stop: End session

[Available Shepherd MCP Tools]
Task management:
- task_start: Queue a task (sheep_name, project_name, prompt)
- task_complete: Record task completion (task_id, summary)
- task_error: Record task error (task_id, error)
- get_history: Query project task history (project_name, limit)
- get_status: Get overall system status

Browser automation (PREFERRED over WebFetch for web tasks):
- browser_session_start, browser_session_stop, browser_open, browser_close
- browser_click, browser_type, browser_select, browser_check, browser_hover
- browser_scroll, browser_get_text, browser_get_html, browser_get_attribute
- browser_get_url, browser_get_title, browser_eval, browser_screenshot, browser_pdf
- browser_wait_selector, browser_wait_hidden, browser_wait_load, browser_wait_idle
- browser_navigate, browser_reload, browser_back, browser_forward, browser_list_pages

For web search/crawling tasks, use browser tools instead of WebFetch.
`)
	}

	if config.GetBool("include_task_history") {
		if sheepName != "" {
			if ctx := getRecentTaskContext(sheepName); ctx != "" {
				sb.WriteString(ctx)
				sb.WriteString("\n")
			}
		}
	}

	if sheepName != "" {
		if skillsText := getProjectSkills(sheepName); skillsText != "" {
			sb.WriteString(skillsText)
			sb.WriteString("\n")
		}
	}

	if config.GetBool("include_mcp_guide") {
		sb.WriteString(`If you need details of previous tasks, use shepherd MCP tools:
- get_history: Query project task history (project_name required, limit optional)
Only query when needed. If the summary above is sufficient, start working immediately.
`)
	}

	return sb.String()
}

// buildPromptWithContext adds MCP guide and recent task context for a specific sheep.
func buildPromptWithContext(sheepName, prompt string) string {
	var sb strings.Builder

	if config.GetBool("include_mcp_guide") {
		sb.WriteString(`[System Instructions]
For browser automation tasks, always use shepherd MCP tools:
- browser_session_start: Start browser session (sheep_name required)
- browser_open: Open URL
- browser_click, browser_type: Element interaction
- browser_get_text, browser_get_html: Information extraction
- browser_screenshot: Capture screenshot
- browser_session_stop: End session

Example: When crawling a webpage
1. Start session with browser_session_start
2. Open URL with browser_open
3. Extract content with browser_get_text
4. End with browser_session_stop

[Available Shepherd MCP Tools]
Task management:
- task_start: Queue a task (sheep_name, project_name, prompt)
- task_complete: Record task completion (task_id, summary)
- task_error: Record task error (task_id, error)
- get_history: Query project task history (project_name, limit)
- get_status: Get overall system status

Browser automation (PREFERRED over WebFetch for web tasks):
- browser_session_start: Start browser session (sheep_name required)
- browser_session_stop: End browser session (sheep_name)
- browser_list_pages: List open pages
- browser_open: Open URL in browser
- browser_close: Close current page
- browser_navigate: Navigate to URL
- browser_reload, browser_back, browser_forward: Navigation
- browser_click: Click element (selector)
- browser_type: Type text into element (selector, text)
- browser_select: Select option (selector, value)
- browser_check: Toggle checkbox (selector)
- browser_hover: Hover over element (selector)
- browser_scroll: Scroll page (direction, amount)
- browser_get_text: Extract text content (selector)
- browser_get_html: Get HTML content (selector)
- browser_get_attribute: Get element attribute (selector, attribute)
- browser_get_url: Get current URL
- browser_get_title: Get page title
- browser_eval: Execute JavaScript
- browser_wait_selector: Wait for element (selector, timeout)
- browser_wait_hidden: Wait for element to hide
- browser_wait_load: Wait for page load
- browser_wait_idle: Wait for network idle
- browser_screenshot: Capture screenshot
- browser_pdf: Generate PDF

IMPORTANT: For web search/crawling tasks, use browser tools instead of WebFetch.

`)
	}

	// Add recent task context
	if config.GetBool("include_task_history") {
		if sheepName != "" {
			if ctx := getRecentTaskContext(sheepName); ctx != "" {
				sb.WriteString(ctx)
				sb.WriteString("\n")
			}
		}
	}

	// Inject project skills
	if sheepName != "" {
		if skillsText := getProjectSkills(sheepName); skillsText != "" {
			sb.WriteString(skillsText)
			sb.WriteString("\n")
		}
	}

	if config.GetBool("include_mcp_guide") {
		sb.WriteString(`[Task Detail Lookup]
If you need details of previous tasks, use shepherd MCP tools:
- get_history: Query project task history (project_name required, limit optional)
Only query when needed. If the summary above is sufficient, start working immediately.

`)
	}

	sb.WriteString("[User Request]\n")
	sb.WriteString(prompt)
	return sb.String()
}

// getRecentTaskContext returns recent task history as context string.
func getRecentTaskContext(sheepName string) string {
	ctx := context.Background()
	client := db.Client()

	// Look up the sheep
	s, err := client.Sheep.Query().
		Where(sheep.Name(sheepName)).
		WithProject().
		Only(ctx)
	if err != nil {
		return ""
	}

	// Query last 3 completed/failed tasks for the project
	var tasks []*ent.Task
	if s.Edges.Project != nil {
		tasks, _ = client.Task.Query().
			Where(
				task.HasProjectWith(entProject.ID(s.Edges.Project.ID)),
				task.StatusIn(task.StatusCompleted, task.StatusFailed),
			).
			Order(ent.Desc(task.FieldCompletedAt)).
			Limit(3).
			All(ctx)
	}

	if len(tasks) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Recent Task History - For previous context reference]\n")
	for i := len(tasks) - 1; i >= 0; i-- {
		t := tasks[i]
		status := "completed"
		if t.Status == task.StatusFailed {
			status = "failed"
		}
		sb.WriteString(fmt.Sprintf("- #%d [%s] %s\n", t.ID, status, truncateStr(t.Prompt, 80)))
		if t.Summary != "" {
			sb.WriteString(fmt.Sprintf("  Result: %s\n", truncateStr(t.Summary, 200)))
		}
		if t.Error != "" {
			sb.WriteString(fmt.Sprintf("  Error: %s\n", truncateStr(t.Error, 200)))
		}
	}
	return sb.String()
}

// getRecentTaskContextCompact returns minimal recent task history (last 1 only) for compact prompts.
func getRecentTaskContextCompact(sheepName string) string {
	bgCtx := context.Background()
	client := db.Client()

	s, err := client.Sheep.Query().
		Where(sheep.Name(sheepName)).
		WithProject().
		Only(bgCtx)
	if err != nil {
		return ""
	}

	var tasks []*ent.Task
	if s.Edges.Project != nil {
		tasks, _ = client.Task.Query().
			Where(
				task.HasProjectWith(entProject.ID(s.Edges.Project.ID)),
				task.StatusIn(task.StatusCompleted, task.StatusFailed),
			).
			Order(ent.Desc(task.FieldCompletedAt)).
			Limit(1).
			All(bgCtx)
	}

	if len(tasks) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Recent Task]\n")
	t := tasks[0]
	status := "completed"
	if t.Status == task.StatusFailed {
		status = "failed"
	}
	sb.WriteString(fmt.Sprintf("- #%d [%s] %s\n", t.ID, status, truncateStr(t.Prompt, 60)))
	if t.Summary != "" {
		sb.WriteString(fmt.Sprintf("  Result: %s\n", truncateStr(t.Summary, 100)))
	}
	return sb.String()
}

// getProjectSkills returns formatted skill content for prompt injection.
func getProjectSkills(sheepName string) string {
	bgCtx := context.Background()
	client := db.Client()

	s, err := client.Sheep.Query().
		Where(sheep.Name(sheepName)).
		WithProject().
		Only(bgCtx)
	if err != nil || s.Edges.Project == nil {
		return ""
	}

	skills, err := skill.GetEnabledSkillsForProject(s.Edges.Project.Name)
	if err != nil || len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Project Skills - Follow these instructions when applicable]\n")
	for _, sk := range skills {
		sb.WriteString(fmt.Sprintf("## %s\n", sk.Name))
		if sk.Description != "" {
			sb.WriteString(fmt.Sprintf("(%s)\n", sk.Description))
		}
		sb.WriteString(sk.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// truncateStr truncates a string to maxLen runes.
func truncateStr(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen]) + "..."
}

// GetMCPConfigJSON returns the MCP config JSON merging user's settings with shepherd.
func GetMCPConfigJSON() string {
	// Read user's ~/.claude/settings.json
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return `{"mcpServers":{"shepherd":{"command":"shepherd","args":["mcp"]}}}`
	}

	settingsPath := homeDir + "/.claude/settings.json"
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return `{"mcpServers":{"shepherd":{"command":"shepherd","args":["mcp"]}}}`
	}

	// Parse existing settings
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return `{"mcpServers":{"shepherd":{"command":"shepherd","args":["mcp"]}}}`
	}

	// Create mcpServers if not present
	mcpServers, ok := settings["mcpServers"].(map[string]interface{})
	if !ok {
		mcpServers = make(map[string]interface{})
	}

	// Add shepherd MCP
	mcpServers["shepherd"] = map[string]interface{}{
		"command": "shepherd",
		"args":    []string{"mcp"},
	}

	// Generate result
	result := map[string]interface{}{
		"mcpServers": mcpServers,
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return `{"mcpServers":{"shepherd":{"command":"shepherd","args":["mcp"]}}}`
	}

	return string(jsonBytes)
}

// executeWithStreaming runs Claude Code with streaming output (no PTY).
// This is used when auto_approve is enabled - no interactive prompts.
func executeWithStreaming(ctx context.Context, sheepName, projectPath, sessionID, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
		"--mcp-config", GetMCPConfigJSON(),
	}

	// Separate system context from user prompt to prevent
	// system instructions from overwhelming the user's actual request.
	if sysCtx := buildSystemContext(sheepName); sysCtx != "" {
		args = append(args, "--append-system-prompt", sysCtx)
	}

	// Resume session (with specific session ID)
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = projectPath
	cmd.Stdin = strings.NewReader(prompt)
	envutil.SetCleanEnv(cmd)

	// Register running task
	registerRunningTask(sheepName, cancel, cmd)

	// stdout pipe
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// stderr pipe
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Claude Code: %w", err)
	}

	// Stream output
	var outputBuilder strings.Builder
	var mu sync.Mutex

	// Read stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		// Increase buffer size (handle long JSON lines)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			mu.Lock()
			outputBuilder.WriteString(line + "\n")
			mu.Unlock()

			// Try to parse stream-json
			parsed := parseStreamLine(line)
			if opts.OnOutput != nil {
				if parsed != "" {
					opts.OnOutput(parsed)
				} else if opts.ShowRawOutput && strings.TrimSpace(line) != "" {
					// Raw output on parse failure (for debugging)
					opts.OnOutput("[raw] " + line)
				}
			}
		}
	}()

	// Read stderr (error messages)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if opts.OnOutput != nil && strings.TrimSpace(line) != "" {
				opts.OnOutput("⚠️ " + line + "\n")
			}
		}
	}()

	// Wait for completion
	err = cmd.Wait()

	mu.Lock()
	fullOutput := outputBuilder.String()
	mu.Unlock()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("execution timeout (%v)", opts.Timeout)
		}
		// Check for rate limit in output or error
		errStr := strings.ToLower(fullOutput + " " + err.Error())
		if strings.Contains(errStr, "rate limit") ||
			strings.Contains(errStr, "you've hit your limit") ||
			strings.Contains(errStr, "hit your limit") ||
			strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "too many requests") ||
			strings.Contains(errStr, "limit exceeded") {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
		// Other errors: return error with output context
		result := parseStreamOutput(fullOutput)
		if result != nil && result.Result != "" {
			// Got valid result despite exit code, return it
			return result, nil
		}
		return nil, fmt.Errorf("Claude Code execution failed: %w\noutput: %s", err, truncateStr(fullOutput, 500))
	}

	// Parse result
	result := parseStreamOutput(fullOutput)

	// Validate result is not empty
	if result == nil || result.Result == "" {
		// Check if output contains rate limit indicators
		outputLower := strings.ToLower(fullOutput)
		if strings.Contains(outputLower, "hit your limit") ||
			strings.Contains(outputLower, "rate limit") ||
			strings.Contains(outputLower, "too many requests") {
			return nil, fmt.Errorf("rate limit: claude CLI hit rate limit")
		}
		if strings.TrimSpace(fullOutput) == "" {
			return nil, fmt.Errorf("Claude Code returned empty output")
		}
	}

	return result, nil
}

// parseStreamLine parses a single line of stream-json output for real-time display.
func parseStreamLine(line string) string {
	if !strings.HasPrefix(line, "{") {
		return line
	}

	var msg struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Name    string `json:"name"`    // tool name
				Input   any    `json:"input"`   // tool input
				Content string `json:"content"` // tool_result content (string)
				ToolID  string `json:"tool_use_id"`
			} `json:"content"`
		} `json:"message"`
		Result    string `json:"result"`
		SessionID string `json:"session_id"`
	}

	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return ""
	}

	switch msg.Type {
	case "assistant":
		var outputs []string
		for _, content := range msg.Message.Content {
			switch content.Type {
			case "text":
				if content.Text != "" {
					outputs = append(outputs, content.Text)
				}
			case "tool_use":
				if content.Name != "" {
					toolInfo := fmt.Sprintf("🔧 %s", content.Name)
					if content.Input != nil {
						if inputMap, ok := content.Input.(map[string]any); ok {
							if cmd, ok := inputMap["command"].(string); ok {
								if len(cmd) > 80 {
									cmd = cmd[:80] + "..."
								}
								toolInfo += fmt.Sprintf(" → %s", cmd)
							} else if pattern, ok := inputMap["pattern"].(string); ok {
								toolInfo += fmt.Sprintf(" → %s", pattern)
							} else if filePath, ok := inputMap["file_path"].(string); ok {
								toolInfo += fmt.Sprintf(" → %s", filePath)
							}
						}
					}
					outputs = append(outputs, toolInfo)
				}
			}
		}
		if len(outputs) > 0 {
			return strings.Join(outputs, "\n")
		}
	case "user":
		// Tool execution result
		var outputs []string
		for _, content := range msg.Message.Content {
			if content.Type == "tool_result" {
				if content.Content != "" {
					// Truncate if result is too long
					result := content.Content
					lines := strings.Split(result, "\n")
					if len(lines) > 5 {
						// Show only first 5 lines
						result = strings.Join(lines[:5], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-5)
					} else if len(result) > 500 {
						result = result[:500] + "..."
					}
					outputs = append(outputs, "   "+strings.ReplaceAll(result, "\n", "\n   "))
				}
			}
		}
		if len(outputs) > 0 {
			return strings.Join(outputs, "\n")
		}
	case "content_block_delta":
		// Streaming text delta
		var delta struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(line), &delta); err == nil {
			if delta.Delta.Text != "" {
				return delta.Delta.Text
			}
		}
	case "result":
		// Final result text was already output in assistant message
		// Only show completion indicator
		return "✅ Task complete"
	case "system":
		// System messages (init, etc.)
		if msg.Subtype == "init" {
			return "🚀 Claude session starting..."
		}
	}

	return ""
}

// parseStreamOutput parses the complete stream-json output.
func parseStreamOutput(output string) *ExecuteResult {
	result := &ExecuteResult{}

	lines := strings.Split(output, "\n")

	for _, line := range lines {
		if !strings.HasPrefix(line, "{") {
			continue
		}

		var msg struct {
			Type         string  `json:"type"`
			Subtype      string  `json:"subtype"`
			SessionID    string  `json:"session_id"`
			Result       string  `json:"result"`
			TotalCostUSD float64 `json:"total_cost_usd"`
			Message      struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}

		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "result":
			// Final result
			if msg.Result != "" {
				result.Result = msg.Result
			}
			if msg.SessionID != "" {
				result.SessionID = msg.SessionID
			}
			if msg.TotalCostUSD > 0 {
				result.CostUSD = msg.TotalCostUSD
			}
		case "assistant":
			// Collect text response (fallback if no result)
			for _, content := range msg.Message.Content {
				if content.Type == "text" && content.Text != "" {
					if result.Result == "" {
						result.Result = content.Text
					}
				}
			}
		}
	}

	return result
}

// executeInteractiveWithPty is defined in pty_unix.go / pty_windows.go

// isInputPrompt checks if the current line looks like an input prompt.
func isInputPrompt(line string) bool {
	trimmed := strings.TrimSpace(stripAnsi(line))

	// Ignore empty or too short lines
	if len(trimmed) < 2 {
		return false
	}

	// Claude Code's actual input prompt patterns (exact matching)
	exactPrompts := []string{
		"❯",                    // Menu selection cursor
		"> ",                   // Default input prompt (trailing space)
		"? ",                   // Question prompt
		"Enter to confirm",     // Confirmation prompt
		"Esc to cancel",        // Cancel prompt
		"to confirm · Esc",     // Confirm/cancel prompt
	}

	for _, pattern := range exactPrompts {
		if strings.Contains(trimmed, pattern) {
			return true
		}
	}

	// y/n confirmation prompts (must be at end of line)
	ynPatterns := []string{
		"(y/n)",
		"(Y/n)",
		"[Y/n]",
		"[y/N]",
		"(yes/no)",
	}

	for _, pattern := range ynPatterns {
		if strings.HasSuffix(trimmed, pattern) {
			return true
		}
	}

	// Numeric selection menu (lines starting with 1. 2. 3. format where current line awaits selection)
	// Short lines starting with "1. " or "2. " are menu items
	if len(trimmed) > 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.' {
		return false // Menu items themselves are not prompts
	}

	return false
}

// stripAnsi removes ANSI escape codes from a string.
func stripAnsi(s string) string {
	// Simple ANSI removal (use regexp for more sophisticated handling)
	result := s
	for {
		start := strings.Index(result, "\033[")
		if start == -1 {
			break
		}
		end := start + 2
		for end < len(result) && result[end] != 'm' && result[end] != 'K' && result[end] != 'H' && result[end] != 'J' {
			end++
		}
		if end < len(result) {
			end++
		}
		result = result[:start] + result[end:]
	}
	return result
}

// extractResultFromOutput extracts the main result from Claude's output.
func extractResultFromOutput(output string) string {
	// Extract main result from output (simple version)
	lines := strings.Split(output, "\n")
	var resultLines []string

	for _, line := range lines {
		clean := strings.TrimSpace(stripAnsi(line))
		if clean != "" && !strings.HasPrefix(clean, ">") && !strings.HasPrefix(clean, "?") {
			resultLines = append(resultLines, clean)
		}
	}

	if len(resultLines) > 10 {
		resultLines = resultLines[len(resultLines)-10:]
	}

	return strings.Join(resultLines, "\n")
}

// extractFilesFromOutput extracts modified file names from Claude's output.
func extractFilesFromOutput(output string) []string {
	var files []string
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		clean := stripAnsi(line)
		// Detect file modification patterns
		if strings.Contains(clean, "Modified") || strings.Contains(clean, "Created") {
			// Try to extract file names
			parts := strings.Fields(clean)
			for _, part := range parts {
				if strings.Contains(part, ".") && !strings.HasPrefix(part, ".") {
					// Add anything that looks like a file name
					files = append(files, part)
				}
			}
		}
	}

	return files
}
