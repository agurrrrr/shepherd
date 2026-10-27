package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/envutil"
	"github.com/agurrrrr/shepherd/internal/grokstream"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/procutil"
)

// grok (Grok Build TUI / xAI, ~/.grok/bin/grok) is a Claude-class general coding
// harness. We drive it non-interactively with `grok -p <prompt>
// --output-format streaming-json --always-approve`.
//
// Grok CLI 1.0.3 made streaming-json ACP session/update NDJSON
// (agent_thought_chunk / agent_message_chunk / tool_call / turn_completed).
// Older builds emitted token-delta lines:
//
//	{"type":"thought","data":"..."}  reasoning token delta
//	{"type":"text","data":"..."}     answer token delta
//	{"type":"end","stopReason":"EndTurn","sessionId":"..."}
//
// parseGrokLine accepts both. The final answer is the concatenation of every
// text / agent_message_chunk, and the terminal state lives on end /
// turn_completed. --always-approve auto-approves tools (OPENCODE_PERMISSION=allow /
// claude --dangerously-skip-permissions).

// grokEvent is the normalized stream event (legacy token-delta or ACP).
type grokEvent = grokstream.Event

// executeWithGrok runs a task via the grok CLI in streaming-json mode.
func executeWithGrok(ctx context.Context, sheepName, projectPath, sessionID, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	// -p (--single)          → headless single-turn: print the response and exit.
	// --output-format         → streaming-json: ACP session/update NDJSON
	//                            (1.0.3+) or legacy thought/text/end deltas.
	// --always-approve        → auto-approve all tool executions (no prompts).
	args := []string{"--output-format", "streaming-json", "--always-approve"}
	args = append(args, grokModelArgs(opts.Model)...)
	// Background-wait policy (#619): grok's built-in 10-minute cap stops the
	// background work a long task is waiting on and exits 1, so we always pass
	// an explicit limit from config.
	args = append(args, grokBackgroundWaitArgs()...)

	// Resume a prior grok session when one is recorded and session reuse is
	// enabled (grok is a large-context agent, so reuse is honored like Claude/Pi).
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	// grok is a Claude-class harness, so feed it the same full system context
	// Claude gets (MCP guide, task history, skills, wiki, sheep memory) with
	// grok's own custom-instructions key. The execution-limit note is prepended
	// as its own bracketed section (before the context blocks) so it reads as
	// harness info, not as part of the user's request.
	fullPrompt := buildPromptWithContextUsing(sheepName, prompt, "custom_prompt_grok")
	if note := grokBackgroundWaitNote(); note != "" {
		fullPrompt = note + "\n\n" + fullPrompt
	}
	// The single-turn prompt is passed via the -p flag (last, so it does not
	// swallow following flags).
	args = append(args, "-p", fullPrompt)

	cmd := exec.CommandContext(ctx, config.GetGrokBinary(), args...)
	cmd.Dir = projectPath
	// Give grok an empty, already-closed stdin so it never inherits the daemon's
	// stdin (a TTY when started from a terminal) and never blocks reading it.
	cmd.Stdin = strings.NewReader("")
	envutil.SetCleanEnv(cmd)
	applyTaskIDEnv(cmd, opts.TaskID)

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

	// Read stdout. ACP tool_call_update lines can exceed 1MB; a Scanner with
	// a 1MB cap dies (ErrTooLong) and grok blocks in pipe_write (#8165).
	// ReadCappedLine keeps the reader alive and parseGrokLine maps both
	// legacy token-delta and ACP session/update into grokEvent.
	go func() {
		defer wg.Done()
		reader := bufio.NewReaderSize(stdout, 64*1024)
		stream := newGrokStreamState(opts.OnOutput)

		for {
			if ctx.Err() != nil {
				return
			}
			line, _, err := procutil.ReadCappedLine(reader, grokstream.MaxLineBytes)
			if err != nil {
				break
			}
			ev := parseGrokLine(line)
			if ev == nil {
				continue
			}
			stream.handle(ev)
			if compact := compactGrokEvent(ev); compact != "" {
				mu.Lock()
				outputBuilder.WriteString(compact + "\n")
				mu.Unlock()
			}
		}

		stream.flush()
	}()

	// Read stderr. Lines are mirrored into a capped buffer so the error
	// classification below can scan them too — grok's background-wait notice
	// can land on stderr instead of the streaming-json stdout (#619).
	stderrBuf := NewCappedBuffer(maxGrokStderrBytes)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()
			stderrBuf.WriteString(line + "\n")
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
		// Background-wait timeout (#619) is checked BEFORE the rate-limit scan:
		// grok stopped background work we were waiting on, so the task is failed,
		// not rate-limited. Checked before the salvage-success path too — salvaged
		// text from a cut-off run must not turn it into a completion. The model's
		// own text is deliberately NOT embedded in the error string: the queue
		// retries on rate-limit substrings found anywhere in it, and a "429" in
		// tool output would re-run the whole task. The last turn's text still
		// reaches the task row through the streamed output lines (the processor
		// saves them and builds the summary from the tail on failure).
		if secs, ok := grokBackgroundWaitRanOut(fullOutput + " " + stderrBuf.String() + " " + err.Error()); ok {
			return nil, grokBackgroundWaitError(secs)
		}
		errStr := strings.ToLower(fullOutput + " " + stderrBuf.String() + " " + err.Error())
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
		result.Result = i18n.T().Worker.NoTextResponse
	}

	return result, nil
}

// grokModelArgs returns ["-m", "<id>"] from the per-task override or the global
// model_grok config. Returns nil when neither is set so grok falls back to its
// own account default — the "Default model:" line of `grok models`, which the
// Settings model list shows as "grok default (<id>)".
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

// maxGrokStderrBytes bounds the mirrored stderr kept for error classification.
// grok's headless notices (including the background-wait one, #619) can land on
// stderr; only the tail matters, and a runaway CLI must not grow the daemon.
const maxGrokStderrBytes = 512 * 1024

// grokBackgroundWaitArgs translates config.GetGrokBackgroundWait() into the
// grok CLI flags that bound how long a headless run waits for the background
// work it started after its latest turn ends (#619). Without them grok uses its
// built-in 10-minute cap, stops the work, and exits 1 — which failed tasks
// whose real work (remote builds, model downloads) simply runs longer.
func grokBackgroundWaitArgs() []string {
	wait := config.GetGrokBackgroundWait()
	if wait <= 0 {
		// "off": exit as soon as the first turn ends; stopped background work
		// does not affect the exit code.
		return []string{"--no-wait-for-background"}
	}
	secs := int64(wait / time.Second)
	if secs < 1 {
		// Sub-second configs (e.g. "500ms") must not become "0", which grok
		// would treat as an immediate wait-out with exit 1.
		secs = 1
	}
	return []string{"--background-wait-timeout", strconv.FormatInt(secs, 10)}
}

// grokBackgroundWaitNote is the one-line hint prepended to grok's prompt so the
// agent knows the real limit and can plan around it (e.g. prefer foreground
// blocking commands, or a fresh follow-up task, over an indefinitely-watched
// monitor). Empty only when the wait is unbounded — which we never produce.
func grokBackgroundWaitNote() string {
	wait := config.GetGrokBackgroundWait()
	if wait <= 0 {
		return "[Execution limit] This headless grok run exits as soon as your first turn ends (--no-wait-for-background). Background work you start (bash/monitor, background subagents) is stopped at that point and its results never reach you: finish the task with foreground commands, or end the turn with a clear status of what is still running."
	}
	return fmt.Sprintf("[Execution limit] This headless grok run waits at most %v after your latest turn ends for background work (bash/monitor, background subagents) to finish; when the wait runs out grok stops that work and exits 1. Prefer foreground blocking commands for long steps, or end the turn with a clear status of what is still running.", wait)
}

// grokBackgroundWaitRanOut detects grok's background-wait exhaustion notice in
// combined stdout+stderr+exit text and extracts the waited seconds when present.
// grok 1.0.67 emits one of:
//
//	the wait for background work ran out after 600 s and exit stopped 1 background task (...)
//	<task> was stopped after 600 s: the wait for background work ran out.
//
// The seconds regexes are anchored to the notice itself, not just "after Ns"
// anywhere in a long tool log — an unrelated "downloaded after 12 s" line must
// not become the reported timeout. Detection is case-insensitive because the
// notice can arrive as a streaming error event, a stderr line, or inside a
// tool output echo.
func grokBackgroundWaitRanOut(combined string) (int, bool) {
	lower := strings.ToLower(combined)
	if !strings.Contains(lower, "wait for background work ran out") {
		return 0, false
	}
	for _, re := range grokBackgroundWaitSecsRes {
		if m := re.FindStringSubmatch(lower); m != nil {
			for _, g := range m[1:] {
				if g != "" {
					secs, _ := strconv.Atoi(g)
					return secs, true
				}
			}
		}
	}
	return 0, true
}

var grokBackgroundWaitSecsRes = []*regexp.Regexp{
	regexp.MustCompile(`wait for background work ran out after (\d+)\s*s`),
	regexp.MustCompile(`stopped after (\d+)\s*s[:.\s]*the wait for background work ran out`),
}

// grokBackgroundWaitError is the classified failure for a background-wait
// timeout. The task stays failed (the stopped work means the request did not
// actually finish) but the message says so explicitly instead of the generic
// "grok execution failed: exit status 1", and it deliberately avoids every
// substring the queue's IsRateLimitError matches, so it is never misclassified
// as a retryable rate limit.
func grokBackgroundWaitError(secs int) error {
	if secs > 0 {
		return fmt.Errorf("grok background wait timeout (%ds): background task stopped before completion — raise grok_background_wait or set it to off for --no-wait-for-background", secs)
	}
	return fmt.Errorf("grok background wait timeout: background task stopped before completion — raise grok_background_wait or set it to off for --no-wait-for-background")
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
	case "tool":
		if ev.Data == "" {
			return
		}
		s.live.Flush()
		if s.section != "" && s.section != "tool" {
			s.emit("\n")
		}
		s.section = "tool"
		s.emit("🔧 " + ev.Data + "\n")
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

// parseGrokLine decodes a single grok streaming-json or ACP line.
func parseGrokLine(line string) *grokEvent {
	return grokstream.ParseLine(line)
}

// compactGrokEvent re-encodes a normalized event for the salvage buffer.
// Raw ACP lines can be megabytes; we only keep thought/text/end/error.
func compactGrokEvent(ev *grokEvent) string {
	if ev == nil {
		return ""
	}
	switch ev.Type {
	case "thought", "text", "end", "error":
		b, err := json.Marshal(ev)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return ""
	}
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
