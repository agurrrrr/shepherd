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

// grok (Grok Build TUI / xAI, ~/.grok/bin/grok) is a Claude-class general coding
// harness. We drive it non-interactively with `grok -p <prompt>
// --output-format streaming-json`, which streams token deltas as JSON lines:
//
//	{"type":"thought","data":"..."}  reasoning token delta
//	{"type":"text","data":"..."}     answer token delta
//	{"type":"end","stopReason":"EndTurn","sessionId":"...","requestId":"..."}
//
// Unlike OpenCode/pi, grok emits per-token DELTAS rather than whole messages, so
// the final answer is the concatenation of every "text" delta and the terminal
// state (session id + stop reason) lives on the single "end" event.
//
// --always-approve auto-approves every tool execution so headless runs never
// block on a permission prompt (grok's equivalent of OPENCODE_PERMISSION=allow /
// claude's --dangerously-skip-permissions).

// grokEvent is one streaming-json line from grok's headless mode.
type grokEvent struct {
	Type       string `json:"type"`       // "thought" | "text" | "end" | "error"
	Data       string `json:"data"`       // token delta for thought/text
	StopReason string `json:"stopReason"` // end event
	SessionID  string `json:"sessionId"`  // end event
	Message    string `json:"message"`    // error event
}

// executeWithGrok runs a task via the grok CLI in streaming-json mode.
func executeWithGrok(ctx context.Context, sheepName, projectPath, sessionID, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	// -p (--single)          → headless single-turn: print the response and exit.
	// --output-format         → streaming-json: emit token-delta JSON lines.
	// --always-approve        → auto-approve all tool executions (no prompts).
	args := []string{"--output-format", "streaming-json", "--always-approve"}
	args = append(args, grokModelArgs(opts.Model)...)

	// Resume a prior grok session when one is recorded and session reuse is
	// enabled (grok is a large-context agent, so reuse is honored like Claude/Pi).
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	// grok is a Claude-class harness, so feed it the same full system context
	// Claude gets (MCP guide, task history, skills, wiki, sheep memory) with
	// grok's own custom-instructions key.
	fullPrompt := buildPromptWithContextUsing(sheepName, prompt, "custom_prompt_grok")
	// The single-turn prompt is passed via the -p flag (last, so it does not
	// swallow following flags).
	args = append(args, "-p", fullPrompt)

	cmd := exec.CommandContext(ctx, config.GetGrokBinary(), args...)
	cmd.Dir = projectPath
	// Give grok an empty, already-closed stdin so it never inherits the daemon's
	// stdin (a TTY when started from a terminal) and never blocks reading it.
	cmd.Stdin = strings.NewReader("")
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
		return nil, fmt.Errorf("failed to start grok: %w", err)
	}

	var outputBuilder = NewCappedBuffer(maxOutputBuilderBytes)
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(2)

	// Read stdout (streaming-json token deltas). Track the current section so the
	// reasoning stream gets a one-time 💭 marker and a clean separator precedes
	// the answer, instead of prefixing every single token.
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		section := "" // "" | "thought" | "text"

		// Grok's streaming-json emits per-token deltas — often 1-3 characters
		// per {"type":"text","data":".."} event. Passing each delta straight to
		// OnOutput causes the live output to fragment into tiny pieces. We
		// buffer text deltas and flush on newline or when a reasonable chunk
		// size accumulates, matching the line-granularity that Claude CLI and
		// OpenCode CLI already provide. (Same approach as callGrokCLI in
		// internal/magi/proposer.go — task #7188/#7192.)
		var liveBuf strings.Builder
		flushLive := func() {
			if liveBuf.Len() > 0 {
				opts.OnOutput(liveBuf.String())
				liveBuf.Reset()
			}
		}

		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()

			mu.Lock()
			outputBuilder.WriteString(line + "\n")
			mu.Unlock()

			ev := parseGrokLine(line)
			if ev == nil || opts.OnOutput == nil {
				continue
			}
			switch ev.Type {
			case "thought":
				if ev.Data == "" {
					continue
				}
				if section != "thought" {
					section = "thought"
					opts.OnOutput("💭 ")
				}
				opts.OnOutput(strings.ReplaceAll(ev.Data, "\n", "\n   "))
			case "text":
				if ev.Data == "" {
					continue
				}
				if section != "text" {
					if section != "" {
						opts.OnOutput("\n\n")
					}
					section = "text"
				}
				liveBuf.WriteString(ev.Data)
				nlIdx := strings.IndexByte(liveBuf.String(), '\n')
				for nlIdx >= 0 {
					s := liveBuf.String()
					opts.OnOutput(s[:nlIdx+1])
					s = s[nlIdx+1:]
					liveBuf.Reset()
					liveBuf.WriteString(s)
					nlIdx = strings.IndexByte(liveBuf.String(), '\n')
				}
				// Safety flush: if no newline has arrived for a while, emit
				// the accumulated chunk so the UI doesn't appear frozen during
				// long paragraphs without line breaks.
				if liveBuf.Len() >= 120 {
					flushLive()
				}
			case "error":
				if ev.Message != "" {
					flushLive()
					opts.OnOutput("\n❌ " + ev.Message + "\n")
				}
			}
		}

		// Flush any remaining buffered text after the stream ends.
		flushLive()
	}()

	// Read stderr.
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
	mu.Unlock()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("grok execution timeout (%v)", opts.Timeout)
		}
		errStr := strings.ToLower(fullOutput + " " + err.Error())
		if strings.Contains(errStr, "rate limit") ||
			strings.Contains(errStr, "session limit") ||
			strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "too many requests") ||
			strings.Contains(errStr, "limit exceeded") {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
		// Try to salvage a completed result even when grok exited non-zero.
		result := parseGrokOutput(fullOutput)
		if result != nil && result.Result != "" && !result.Incomplete {
			return result, nil
		}
		return nil, fmt.Errorf("grok execution failed: %w\noutput: %s", err, truncateStr(fullOutput, 500))
	}

	result := parseGrokOutput(fullOutput)

	if result.Incomplete {
		return nil, fmt.Errorf("incomplete: %s", result.IncompleteReason)
	}
	if result.Result == "" {
		// Completed with only tool calls and no final text — treat as success.
		result.Result = "(작업 완료 - 텍스트 응답 없음)"
	}

	return result, nil
}

// grokModelArgs returns ["-m", "<id>"] from the per-task override or the global
// model_grok config. Returns nil when neither is set so grok falls back to its
// own configured default model (grok-4.5).
func grokModelArgs(modelOverride string) []string {
	m := strings.TrimSpace(modelOverride)
	if m == "" {
		m = strings.TrimSpace(config.GetString("model_grok"))
	}
	if m == "" {
		return nil
	}
	return []string{"-m", m}
}

// parseGrokLine decodes a single grok streaming-json line. Returns nil for
// blank / non-JSON lines.
func parseGrokLine(line string) *grokEvent {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil
	}
	var ev grokEvent
	if json.Unmarshal([]byte(line), &ev) != nil {
		return nil
	}
	return &ev
}

// parseGrokOutput reconstructs the final ExecuteResult from grok's complete
// streaming-json output: the answer is the concatenation of every "text" delta,
// and the session id / stop reason come from the terminal "end" event.
func parseGrokOutput(output string) *ExecuteResult {
	result := &ExecuteResult{}
	var answer strings.Builder
	var stopReason, errMessage string

	for _, line := range strings.Split(output, "\n") {
		ev := parseGrokLine(line)
		if ev == nil {
			continue
		}
		switch ev.Type {
		case "text":
			answer.WriteString(ev.Data)
		case "end":
			if ev.SessionID != "" {
				result.SessionID = ev.SessionID
			}
			if ev.StopReason != "" {
				stopReason = ev.StopReason
			}
		case "error":
			if ev.Message != "" {
				errMessage = ev.Message
			}
		}
	}

	result.Result = strings.TrimSpace(answer.String())

	// grok does not report token usage in headless streaming mode — leave the
	// token/cost fields at zero.

	// Truncation / hard-error detection so the task is recorded as failed rather
	// than silently completed. grok's normal completion is stopReason "EndTurn";
	// a length/token-cap stop with no answer means the turn was cut off.
	switch {
	case errMessage != "":
		result.Incomplete = true
		result.IncompleteReason = errMessage
	case isGrokTruncatedStop(stopReason) && result.Result == "":
		result.Incomplete = true
		result.IncompleteReason = "model output was truncated (stopReason: " + stopReason + ") with no final response"
	}

	return result
}

// isGrokTruncatedStop reports whether a grok end-event stopReason indicates the
// turn was cut off mid-generation (token cap) rather than finishing cleanly.
func isGrokTruncatedStop(stopReason string) bool {
	s := strings.ToLower(stopReason)
	return strings.Contains(s, "max") || strings.Contains(s, "length") || strings.Contains(s, "token")
}
