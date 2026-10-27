// Package llmcli provides single-shot, no-tool invocations of the agent CLIs
// (claude, opencode, grok, pi). It is the shared implementation behind the
// MAGI proposers' CLI paths and internal/namegen (#638): each call spawns one
// CLI process, feeds it a prompt, reads the final text answer, and exits — no
// session, no resume, no MCP tools.
//
// Callers own timeout handling (pass a ctx with a deadline) and the working
// directory. Pass an empty temp dir when the call must not see project
// context: run inside a repository and the CLI picks up CLAUDE.md, project
// MCP servers, and skills, which slows the call down and adds cost.
//
// The package is a leaf — it imports only config/envutil/procutil/grokstream —
// so magi, worker, namegen, and server can all depend on it without cycles.
package llmcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/envutil"
	"github.com/agurrrrr/shepherd/internal/grokstream"
	"github.com/agurrrrr/shepherd/internal/procutil"
)

// CallOptions configures one single-shot CLI call.
type CallOptions struct {
	// ModelID selects the CLI's model flag. Empty means "use the provider's
	// own default" (no flag emitted).
	ModelID string
	// WorkDir is the child process's working directory. Empty means inherit;
	// callers should pass an empty temp dir for context-free calls.
	WorkDir string
	// SystemPrompt and UserPrompt are joined into the single prompt the CLI
	// receives (system first, blank line between). Either may be empty.
	SystemPrompt string
	UserPrompt   string
	// OnToken (may be nil) receives incremental answer text for live output.
	OnToken func(string)
}

// joinPrompt merges the two prompt parts the way every caller used to.
func joinPrompt(opts CallOptions) string {
	if opts.SystemPrompt == "" {
		return opts.UserPrompt
	}
	if opts.UserPrompt == "" {
		return opts.SystemPrompt
	}
	return opts.SystemPrompt + "\n\n" + opts.UserPrompt
}

// ClaudeCLI runs `claude --print` with an optional model flag and the prompt on
// stdin. Returns the trimmed final text. Same shape as the MAGI proposer call
// path it was extracted from (issue #638: shared helper, no code copies).
func ClaudeCLI(ctx context.Context, opts CallOptions) (string, error) {
	args := []string{"--print"}
	if opts.ModelID != "" {
		args = append(args, "--model", opts.ModelID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = opts.WorkDir
	cmd.Stdin = strings.NewReader(joinPrompt(opts))
	envutil.SetCleanEnv(cmd)
	procutil.HideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("claude pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude start: %w", err)
	}

	// ReadCappedLine instead of a 1MB Scanner: an oversized line is cut,
	// not fatal, so the pipe keeps draining and Wait can return.
	var buf bytes.Buffer
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, _, err := procutil.ReadCappedLine(reader, procutil.DefaultMaxLineBytes)
		if err != nil {
			break
		}
		buf.WriteString(line + "\n")
		if opts.OnToken != nil {
			opts.OnToken(line + "\n")
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("claude wait: %w", err)
	}

	output := strings.TrimSpace(buf.String())
	if output == "" {
		return "", fmt.Errorf("claude returned empty output")
	}
	return output, nil
}

// OpenCodeCLI runs `opencode run --format json` with an optional model flag and
// the prompt on stdin, then extracts the final assistant text from the JSON
// event stream.
func OpenCodeCLI(ctx context.Context, opts CallOptions) (string, error) {
	args := []string{"run", "--format", "json"}
	if opts.ModelID != "" {
		args = append(args, "-m", opts.ModelID)
	}

	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = opts.WorkDir
	cmd.Stdin = strings.NewReader(joinPrompt(opts))
	envutil.SetCleanEnv(cmd)
	procutil.HideWindow(cmd)
	cmd.Env = append(cmd.Env, `OPENCODE_PERMISSION={"*":"allow"}`)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("opencode pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("opencode start: %w", err)
	}

	// Oversized events (large tool output) are skipped rather than ending
	// the read loop; a cut JSON line is unusable anyway.
	var buf bytes.Buffer
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, truncated, err := procutil.ReadCappedLine(reader, procutil.DefaultMaxLineBytes)
		if err != nil {
			break
		}
		if truncated {
			continue
		}
		buf.WriteString(line + "\n")

		// Parse OpenCode JSON events to extract text content for streaming.
		parsed := parseOpenCodeEvent(line)
		if parsed != "" && opts.OnToken != nil {
			opts.OnToken(parsed + "\n")
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("opencode wait: %w", err)
	}

	output := strings.TrimSpace(buf.String())
	if output == "" {
		return "", fmt.Errorf("opencode returned empty output")
	}

	// Extract the final text from OpenCode JSON event stream.
	finalText := ExtractOpenCodeFinalText(output)
	if finalText == "" {
		finalText = output // fallback to raw output
	}
	return finalText, nil
}

// GrokCLI runs `grok --output-format streaming-json --always-approve -p <prompt>`
// with an optional model flag. Grok 1.0.3 emits ACP session/update NDJSON
// (older builds emit {"type":"text","data":".."} deltas). The final answer is
// the concatenation of every text / agent_message_chunk.
func GrokCLI(ctx context.Context, opts CallOptions) (string, error) {
	args := []string{"--output-format", "streaming-json", "--always-approve"}
	if opts.ModelID != "" {
		args = append(args, "-m", opts.ModelID)
	}
	// The single-turn prompt goes last, via the -p flag.
	args = append(args, "-p", joinPrompt(opts))

	cmd := exec.CommandContext(ctx, config.GetGrokBinary(), args...)
	cmd.Dir = opts.WorkDir
	cmd.Stdin = strings.NewReader("")
	envutil.SetCleanEnv(cmd)
	procutil.HideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("grok pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("grok start: %w", err)
	}

	var answer strings.Builder
	reader := bufio.NewReaderSize(stdout, 64*1024)

	// Grok 1.0.3 streaming-json is ACP session/update (or legacy type=text
	// deltas). parse via grokstream so callers see the same events as the
	// worker, and ReadCappedLine never dies on 1MB+ tool_call_update lines.
	// Buffer text deltas and flush on newline / 120B so live output stays
	// line-granular (#7086).
	var liveBuf strings.Builder
	flushLive := func() {
		if opts.OnToken != nil && liveBuf.Len() > 0 {
			opts.OnToken(liveBuf.String())
			liveBuf.Reset()
		}
	}

	for {
		line, _, err := procutil.ReadCappedLine(reader, grokstream.MaxLineBytes)
		if err != nil {
			break
		}
		ev := grokstream.ParseLine(line)
		if ev == nil || ev.Type != "text" || ev.Data == "" {
			continue
		}
		answer.WriteString(ev.Data)
		if opts.OnToken != nil {
			liveBuf.WriteString(ev.Data)
			nlIdx := strings.IndexByte(liveBuf.String(), '\n')
			for nlIdx >= 0 {
				s := liveBuf.String()
				opts.OnToken(s[:nlIdx+1])
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
		}
	}

	// Flush any remaining buffered text after the stream ends.
	flushLive()

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("grok wait: %w", err)
	}

	output := strings.TrimSpace(answer.String())
	if output == "" {
		return "", fmt.Errorf("grok returned empty output")
	}
	return output, nil
}

// PiCLI runs `pi --print --mode json` with an optional model flag; the prompt
// is a positional argument. --print is mandatory: without it pi stays
// interactive after answering and cmd.Wait() blocks forever (see worker/pi.go).
func PiCLI(ctx context.Context, opts CallOptions) (string, error) {
	args := []string{"--print", "--mode", "json"}
	if opts.ModelID != "" {
		args = append(args, "--model", opts.ModelID)
	}
	args = append(args, joinPrompt(opts))

	cmd := exec.CommandContext(ctx, config.GetPiBinary(), args...)
	cmd.Dir = opts.WorkDir
	// Empty, already-closed stdin: pi must never inherit the daemon's stdin
	// (a TTY when started from a terminal). Belt-and-suspenders alongside --print.
	cmd.Stdin = strings.NewReader("")
	envutil.SetCleanEnv(cmd)
	procutil.HideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("pi pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("pi start: %w", err)
	}

	var answer strings.Builder
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, truncated, err := procutil.ReadCappedLine(reader, procutil.DefaultMaxLineBytes)
		if err != nil {
			break
		}
		if truncated {
			continue
		}
		text := parsePiAnswerLine(line)
		if text == "" {
			continue
		}
		answer.WriteString(text + "\n")
		if opts.OnToken != nil {
			opts.OnToken(text + "\n")
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("pi wait: %w", err)
	}

	output := strings.TrimSpace(answer.String())
	if output == "" {
		return "", fmt.Errorf("pi returned empty output")
	}
	return output, nil
}

// parsePiAnswerLine extracts the assistant text from one pi JSON event line.
// Only message_end events with an assistant message carry the final answer;
// tool/thinking blocks are ignored because single-shot calls ask for text only.
func parsePiAnswerLine(line string) string {
	if !strings.HasPrefix(line, "{") {
		return ""
	}
	var ev struct {
		Type    string `json:"type"`
		Message *struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return ""
	}
	if ev.Type != "message_end" || ev.Message == nil || ev.Message.Role != "assistant" {
		return ""
	}
	var parts []string
	for _, block := range ev.Message.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

// parseOpenCodeEvent extracts displayable text from a single OpenCode JSON line.
//
// OpenCode v1.16+ emits events in the AI SDK streaming format:
//
//	{"type":"text","part":{"type":"text","text":"Hello!"}}
//	{"type":"step_start","part":{"type":"step-start"}}
//	{"type":"step_finish","part":{"type":"step-finish","reason":"stop"}}
//
// The text content lives in part.text (not a top-level "content" field).
// Older versions used {"type":"message","content":"..."} — we keep that as
// a fallback for compatibility.
func parseOpenCodeEvent(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return ""
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return ""
	}
	eventType, _ := event["type"].(string)

	// v1.16+ format: text lives in part.text.
	if eventType == "text" {
		if part, ok := event["part"].(map[string]interface{}); ok {
			if text, ok := part["text"].(string); ok {
				return text
			}
		}
	}

	// Legacy format fallback: top-level content field.
	switch eventType {
	case "message", "text", "assistant":
		if content, ok := event["content"].(string); ok {
			return content
		}
	}
	return ""
}

// extractOpenCodeFinalText extracts the final assistant message from a
// sequence of OpenCode JSON events.
//
// It concatenates all text parts from "text"-type events (v1.16+ format).
// For legacy format (top-level "content"), it takes the last non-empty value.
func ExtractOpenCodeFinalText(raw string) string {
	lines := strings.Split(raw, "\n")
	var parts []string
	var lastLegacyText string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		eventType, _ := event["type"].(string)

		// v1.16+ format: accumulate all text parts.
		if eventType == "text" {
			if part, ok := event["part"].(map[string]interface{}); ok {
				if text, ok := part["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}

		// Legacy format fallback.
		if eventType == "message" || eventType == "assistant" {
			if content, ok := event["content"].(string); ok && content != "" {
				lastLegacyText = content
			}
		}
	}

	if len(parts) > 0 {
		return strings.Join(parts, "")
	}
	return lastLegacyText
}
