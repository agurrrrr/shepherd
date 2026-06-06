package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/envutil"
)

// pi (pi-coding-agent, https://pi.dev) is a Claude-class general coding harness.
// We drive it non-interactively with `pi --mode json`, which streams well-typed
// JSON-line events (see https://pi.dev/docs/latest/json). Unlike OpenCode, pi's
// event schema is explicit and stable — completion is a single `agent_end` event
// and token/cost data live in one `usage` struct — so the parser below stays
// small and does not need the multi-spelling probing the OpenCode parser does.

// piMessage mirrors the AgentMessage shapes pi emits (packages/ai/src/types.ts).
// Content is left raw because assistant messages carry a typed content-block
// array while user messages carry a plain string.
type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Usage        piUsage         `json:"usage"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
}

// piUsage is pi's Usage struct. Cost is already computed by pi, so we read it
// straight out instead of pricing tokens ourselves.
type piUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
	Cost        struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// piContentBlock is one element of an AssistantMessage's content array.
type piContentBlock struct {
	Type      string          `json:"type"` // "text" | "thinking" | "toolCall"
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// executeWithPi runs a task via the pi CLI in JSON event-stream mode.
func executeWithPi(ctx context.Context, sheepName, projectPath, sessionID, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	// --mode json → stream session events as JSON lines and run non-interactively
	//               (the run processes the prompt and exits; no --print/-p needed).
	//               CLAUDE.md/AGENTS.md are loaded by default — there is no
	//               --approve flag (pi has no trust prompt in json mode).
	args := []string{"--mode", "json"}
	args = append(args, piModelArgs(opts.Model)...)
	if opts.Thinking {
		// pi takes a reasoning *level*; map our boolean toggle to a balanced default.
		args = append(args, "--thinking", "medium")
	}
	// Resume a prior pi session when one is recorded and session reuse is enabled.
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}

	// pi is a Claude-class harness, so feed it the same full system context Claude
	// gets (MCP guide, task history, skills, wiki, sheep memory) with pi's own
	// custom-instructions key. In --mode json the prompt is a positional argument.
	fullPrompt := buildPromptWithContextUsing(sheepName, prompt, "custom_prompt_pi")
	args = append(args, fullPrompt)

	cmd := exec.CommandContext(ctx, config.GetPiBinary(), args...)
	cmd.Dir = projectPath
	envutil.SetCleanEnv(cmd)

	// Register running task; unregister with the returned token so a late finish
	// can only remove our own entry, never a newer task's (stop+restart race).
	rt := registerRunningTask(sheepName, cancel, cmd)
	defer unregisterRunningTask(sheepName, rt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start pi: %w", err)
	}

	var outputBuilder strings.Builder
	var newSessionID string
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(2)

	// Read stdout (JSON event lines)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()

			mu.Lock()
			outputBuilder.WriteString(line + "\n")
			mu.Unlock()

			parsed, sid := parsePiLine(line)
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
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()
			if opts.OnOutput != nil && strings.TrimSpace(line) != "" {
				opts.OnOutput("⚠️ " + line + "\n")
			}
		}
	}()

	wg.Wait()
	err = cmd.Wait()

	mu.Lock()
	fullOutput := outputBuilder.String()
	sid := newSessionID
	mu.Unlock()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("pi execution timeout (%v)", opts.Timeout)
		}
		errStr := strings.ToLower(fullOutput + " " + err.Error())
		if strings.Contains(errStr, "rate limit") ||
			strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "too many requests") ||
			strings.Contains(errStr, "limit exceeded") {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
		// Try to salvage a completed result even when pi exited non-zero.
		result := parsePiOutput(fullOutput)
		if result != nil && result.Result != "" && !result.Incomplete {
			if result.SessionID == "" {
				result.SessionID = sid
			}
			return result, nil
		}
		return nil, fmt.Errorf("pi execution failed: %w\noutput: %s", err, truncateStr(fullOutput, 500))
	}

	result := parsePiOutput(fullOutput)
	if result.SessionID == "" {
		result.SessionID = sid
	}

	if result.Incomplete {
		return nil, fmt.Errorf("incomplete: %s", result.IncompleteReason)
	}
	if result.Result == "" {
		// Completed with only tool calls and no final text — treat as success.
		result.Result = "(작업 완료 - 텍스트 응답 없음)"
	}

	return result, nil
}

// piModelArgs returns ["--model", "<id>"] from the per-task override or the
// global model_pi config. Returns nil when neither is set so pi falls back to
// its own configured default model/provider.
func piModelArgs(modelOverride string) []string {
	m := strings.TrimSpace(modelOverride)
	if m == "" {
		m = strings.TrimSpace(config.GetString("model_pi"))
	}
	if m == "" {
		return nil
	}
	return []string{"--model", m}
}

// parsePiLine renders a single pi JSON event for the live output stream.
// Text answers come from message_end; tool activity from tool_execution_*.
func parsePiLine(line string) (text string, sessionID string) {
	if !strings.HasPrefix(line, "{") {
		return "", ""
	}

	var ev struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"` // session header UUID
		Message  *piMessage      `json:"message"`
		ToolName string          `json:"toolName"`
		Args     json.RawMessage `json:"args"`
		IsError  bool            `json:"isError"`
		// auto_retry / compaction lifecycle
		Attempt      int    `json:"attempt"`
		MaxAttempts  int    `json:"maxAttempts"`
		ErrorMessage string `json:"errorMessage"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return "", ""
	}

	switch ev.Type {
	case "session":
		return "", ev.ID
	case "message_end":
		if ev.Message != nil {
			return piRenderAssistantContent(ev.Message), ""
		}
	case "tool_execution_start":
		if ev.ToolName != "" {
			return piRenderToolStart(ev.ToolName, ev.Args), ""
		}
	case "tool_execution_end":
		if ev.IsError && ev.ToolName != "" {
			return "❌ " + ev.ToolName, ""
		}
	case "auto_retry_start":
		return fmt.Sprintf("♻️ retry %d/%d: %s", ev.Attempt, ev.MaxAttempts, ev.ErrorMessage), ""
	case "compaction_start":
		return "🗜️ compacting context...", ""
	}
	return "", ""
}

// piRenderAssistantContent renders the text and thinking blocks of an assistant
// message for the live stream. Tool-call blocks are skipped because the
// tool_execution_* events already cover tool activity.
func piRenderAssistantContent(m *piMessage) string {
	if m.Role != "assistant" {
		return ""
	}
	var parts []string
	for _, b := range parsePiContentBlocks(m.Content) {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				parts = append(parts, "💭 "+strings.ReplaceAll(strings.TrimSpace(b.Thinking), "\n", "\n   "))
			}
		}
	}
	return strings.Join(parts, "\n")
}

// piRenderToolStart formats a tool invocation line, probing the common argument
// keys pi's built-in tools use.
func piRenderToolStart(name string, args json.RawMessage) string {
	info := "🔧 " + name
	var a struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		FilePath    string `json:"file_path"`
		Path        string `json:"path"`
		Pattern     string `json:"pattern"`
		URL         string `json:"url"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &a)
	}
	switch {
	case a.Description != "":
		info += " → " + a.Description
	case a.Command != "":
		cmd := a.Command
		if len(cmd) > 80 {
			cmd = cmd[:80] + "..."
		}
		info += " → " + cmd
	case a.FilePath != "":
		info += " → " + a.FilePath
	case a.Path != "":
		info += " → " + a.Path
	case a.Pattern != "":
		info += " → " + a.Pattern
	case a.URL != "":
		info += " → " + a.URL
	}
	return info
}

// parsePiContentBlocks decodes an assistant message's content array. Returns nil
// when content is a plain string (user message) or otherwise not a block array.
func parsePiContentBlocks(raw json.RawMessage) []piContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var blocks []piContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	return blocks
}

// piAssistantText returns just the joined text-block content of an assistant
// message (the user-facing answer), excluding thinking and tool calls.
func piAssistantText(m *piMessage) string {
	var parts []string
	for _, b := range parsePiContentBlocks(m.Content) {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// parsePiOutput parses the complete pi JSON event stream into an ExecuteResult.
// The authoritative final state is the agent_end event, which carries the full
// final message list. If the run was interrupted before agent_end, we fall back
// to reconstructing from the per-message message_end events.
func parsePiOutput(output string) *ExecuteResult {
	result := &ExecuteResult{}

	var finalText, lastStopReason, lastErrorMessage string
	var sumIn, sumOut, sumCacheR, sumCacheW int64
	var sumCost float64
	sawAgentEnd := false

	accumulate := func(msgs []piMessage) {
		for i := range msgs {
			m := &msgs[i]
			if m.Role != "assistant" {
				continue
			}
			sumIn += m.Usage.Input
			sumOut += m.Usage.Output
			sumCacheR += m.Usage.CacheRead
			sumCacheW += m.Usage.CacheWrite
			sumCost += m.Usage.Cost.Total
			if t := piAssistantText(m); t != "" {
				finalText = t
			}
			if m.StopReason != "" {
				lastStopReason = m.StopReason
			}
			if m.ErrorMessage != "" {
				lastErrorMessage = m.ErrorMessage
			}
		}
	}

	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev struct {
			Type     string      `json:"type"`
			ID       string      `json:"id"`
			Messages []piMessage `json:"messages"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "session":
			if ev.ID != "" {
				result.SessionID = ev.ID
			}
		case "agent_end":
			accumulate(ev.Messages)
			sawAgentEnd = true
		}
	}

	// Fallback for interrupted runs that never reached agent_end.
	if !sawAgentEnd {
		for _, line := range strings.Split(output, "\n") {
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var ev struct {
				Type    string     `json:"type"`
				Message *piMessage `json:"message"`
			}
			if json.Unmarshal([]byte(line), &ev) != nil {
				continue
			}
			if ev.Type == "message_end" && ev.Message != nil {
				accumulate([]piMessage{*ev.Message})
			}
		}
	}

	result.Result = finalText
	result.PromptTokens = sumIn + sumCacheR + sumCacheW
	result.CompletionTokens = sumOut
	result.CostUSD = sumCost

	// Truncation / hard-error detection so the task is recorded as failed rather
	// than silently completed.
	switch {
	case lastStopReason == "length" && strings.TrimSpace(finalText) == "":
		result.Incomplete = true
		result.IncompleteReason = "model output was truncated (stopReason: length) with no final response"
	case lastStopReason == "error":
		result.Incomplete = true
		if lastErrorMessage != "" {
			result.IncompleteReason = lastErrorMessage
		} else {
			result.IncompleteReason = "pi reported stopReason=error"
		}
	}

	return result
}
