package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"unicode/utf8"

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

		stream := newGrokStreamState(opts.OnOutput)

		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()

			mu.Lock()
			outputBuilder.WriteString(line + "\n")
			mu.Unlock()

			if ev := parseGrokLine(line); ev != nil {
				stream.handle(ev)
			}
		}

		stream.flush()
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
// own configured default model (grok-4.6).
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

// grokStreamState turns streaming-json thought/text/error events into
// OnOutput chunks. Thought and text share grokLiveBuf so per-token
// deltas do not become one live line each.
type grokStreamState struct {
	section string // "" | "thought" | "text"
	live    *grokLiveBuf
	emit    func(string)
}

func newGrokStreamState(emit func(string)) *grokStreamState {
	s := &grokStreamState{emit: emit}
	s.live = newGrokLiveBuf(func(chunk string) {
		if s.emit == nil || chunk == "" {
			return
		}
		if s.section == "thought" {
			// Drop indent-only / blank flushes so classifyLine does not
			// treat them as text and close the thinking card (#8109).
			if strings.TrimSpace(chunk) == "" {
				return
			}
			chunk = tagThoughtChunk(chunk)
		}
		s.emit(chunk)
	})
	return s
}

func (s *grokStreamState) handle(ev *grokEvent) {
	if ev == nil || s.emit == nil {
		return
	}
	switch ev.Type {
	case "thought":
		if ev.Data == "" {
			return
		}
		if s.section != "thought" {
			s.live.Flush()
			s.section = "thought"
			// Line boundary so text→thought does not glue ("answer.💭").
			s.live.Write("\n💭 ")
		}
		s.live.Append(indentThoughtData(ev.Data))
	case "text":
		if ev.Data == "" {
			return
		}
		if s.section != "text" {
			s.live.Flush()
			if s.section != "" {
				s.emit("\n\n")
			}
			s.section = "text"
		}
		s.live.Append(ev.Data)
	case "error":
		if ev.Message != "" {
			s.live.Flush()
			s.emit("\n❌ " + ev.Message + "\n")
		}
	}
}

func (s *grokStreamState) flush() {
	s.live.Flush()
}

// indentThoughtData turns thought newlines into 3-space continuations.
// Grok's next sentence usually has no leading space, so the break must
// stay; blank line bodies are omitted. Whitespace-only flushes are
// dropped in grokStreamState so they do not close the thinking card.
func indentThoughtData(data string) string {
	if data == "" || !strings.Contains(data, "\n") {
		return data
	}
	var b strings.Builder
	for i, line := range strings.Split(data, "\n") {
		if i > 0 {
			b.WriteString("\n   ")
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// tagThoughtChunk keeps a flushed thought chunk inside the thinking block
// without injecting a 💭 on every safety flush.
//
// The first flush of a thought section is already prefixed via
// live.Write("\n💭 "). After LineCoalescer (#7209), mid-line remnants of a
// ≥120B safety flush stay on that same open 💭 line — re-tagging them
// produced "want 💭 me to" inside the Thinking card (every wrap / ~120B).
//
// A new physical line without a marker is indented (3 spaces) so
// classifyLine keeps it as a thinking continuation. We do not add another
// 💭; only the section opener should carry the marker.
func tagThoughtChunk(s string) string {
	if s == "" {
		return s
	}
	// Preserve leading newlines (section separators).
	i := 0
	for i < len(s) && (s[i] == '\n' || s[i] == '\r') {
		i++
	}
	prefix, rest := s[:i], s[i:]
	if rest == "" {
		return s
	}
	trimmed := strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(trimmed, "💭") {
		return s
	}
	// Already a 3-space continuation (indentThoughtData / prior pass).
	if strings.HasPrefix(rest, "   ") {
		return s
	}
	// New physical line without a marker: indent, do not re-tag.
	if prefix != "" {
		return prefix + "   " + rest
	}
	// Mid-line safety-flush remnant: leave untagged so LineCoalescer
	// concatenates onto the still-open 💭 line.
	return s
}

// grokLiveBuf coalesces per-token thought/text deltas into line-sized chunks
// before calling OnOutput. See executeWithGrok and task #7201/#7202.
type grokLiveBuf struct {
	buf strings.Builder
	out func(string)
}

func newGrokLiveBuf(out func(string)) *grokLiveBuf {
	return &grokLiveBuf{out: out}
}

// Write appends raw bytes without flushing (e.g. the one-time 💭 marker).
func (b *grokLiveBuf) Write(s string) {
	if s != "" {
		b.buf.WriteString(s)
	}
}

// Append adds a delta, flushing complete lines and safety-flushing long runs.
func (b *grokLiveBuf) Append(data string) {
	if data == "" || b.out == nil {
		return
	}
	b.buf.WriteString(data)
	for {
		s := b.buf.String()
		nlIdx := strings.IndexByte(s, '\n')
		if nlIdx < 0 {
			break
		}
		b.out(s[:nlIdx+1])
		b.buf.Reset()
		b.buf.WriteString(s[nlIdx+1:])
	}
	if b.buf.Len() >= 120 {
		b.flushSafety()
	}
}

// Flush emits any remaining buffered content (section switch / stream end).
func (b *grokLiveBuf) Flush() {
	if b.out == nil || b.buf.Len() == 0 {
		return
	}
	b.out(b.buf.String())
	b.buf.Reset()
}

// flushSafety emits without a newline, preferring a word boundary so BPE
// token cuts (e.g. "screens"+"hots") don't become mid-word line breaks.
// For CJK text (which rarely contains spaces), we also accept sentence-ending
// punctuation and CJK character boundaries as split points (task #7209).
func (b *grokLiveBuf) flushSafety() {
	if b.out == nil || b.buf.Len() == 0 {
		return
	}
	s := b.buf.String()

	// Try ASCII space first (original heuristic).
	if sp := strings.LastIndexByte(s, ' '); sp >= len(s)/2 {
		b.out(s[:sp+1])
		b.buf.Reset()
		b.buf.WriteString(s[sp+1:])
		return
	}

	// Try CJK sentence-ending punctuation: 。！？，、；：
	// These are natural break points in Korean/Chinese/Japanese text.
	for i := len(s) - 1; i >= len(s)/2; i-- {
		c := s[i]
		if c == 0xE3 || c == 0xEF { // UTF-8 lead byte for U+3000-U+3FFF (CJK punctuation) or U+FF00-U+FFEF (halfwidth/fullwidth)
			// Check if this is a CJK punctuation character (3-byte UTF-8).
			if i+2 < len(s) {
				r, _ := utf8.DecodeRuneInString(s[i:])
				if r == '。' || r == '！' || r == '？' || r == '，' || r == '、' || r == '；' || r == '：' {
					b.out(s[:i+3])
					b.buf.Reset()
					b.buf.WriteString(s[i+3:])
					return
				}
			}
		}
	}

	// No good split point found — emit the whole buffer.
	b.out(s)
	b.buf.Reset()
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
