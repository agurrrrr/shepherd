package magi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/envutil"
)

// minConvergenceReserve is the wall-clock time reserved at the tail of a
// proposer's budget to force a final answer once tool exploration must stop.
// Without a reserve, the forced (tools-off) request would race the hard
// deadline and be cut off before producing an answer.
const minConvergenceReserve = 20 * time.Second

// convergenceDirective is appended (as a user turn) when a proposer must stop
// exploring and produce its final answer — because the wall-clock budget is
// nearly spent. Tools are dropped from that request so the model cannot keep
// investigating.
const convergenceDirective = `이제 추가 조사(도구 사용)를 멈추고, 지금까지 확인한 내용만으로 최종 답변을 작성하라.
더 이상 도구를 호출할 수 없다. 완결된 최종 답변을 쓰고, 마지막 줄에 "CONFIDENCE: <0-10 정수>"를 추가하라.`

// finalAnswerNudge re-prompts a proposer whose last turn produced no usable
// answer — an empty response, or tool-call markup emitted as text (which the
// content gate rejects; task #7077 CASPER). Exactly one nudge is allowed before
// the proposer is declared failed, so a single unusable turn must not discard
// the whole deliberation (lesson from task #7066).
const finalAnswerNudge = `직전 응답에는 실질적인 최종 답변이 없다(빈 응답이거나 도구 호출 형식만 반환됨).
더 이상 도구를 호출하지 말고, 지금까지 확인한 맥락만으로 완결된 최종 답변을 산문으로 작성하라.
마지막 줄에 "CONFIDENCE: <0-10 정수>"를 포함하라.`

// chatTurn performs one streaming chat request and returns the assistant
// message plus token usage. It is a package variable so tests can drive the
// proposer mini agent loop (callEndpoint) — including the tool-exploration,
// forced-convergence, and empty-response-nudge paths — without a live LLM.
var chatTurn = func(ctx context.Context, client *embedded.Client, req *embedded.ChatRequest, onToken func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
	// Proposer retry budget (task #7077): short, ctx-bounded — a dead endpoint
	// fails fast instead of exhausting the main-agent's patient retry policy and
	// consuming the whole per-proposer timeout.
	msg, _, usage, err := client.AccumulateStreamProposer(ctx, req, nil, onToken)
	var u embedded.ChatUsage
	if usage != nil {
		u = *usage
	}
	return msg, u, err
}

// callEndpoint sends a chat request — with optional read-only tools — and runs
// a mini agent loop until the model produces a final text answer. It is a
// package variable so higher-level tests can inject a fake wholesale.
//
// Boundary (design: 라운드 카운트 제거 — 타임아웃만으로 경계): there is no
// tool-round count. Tool exploration runs until the per-proposer wall-clock
// budget (the ctx deadline set in RunProposers) is nearly spent. A tail of that
// budget (see minConvergenceReserve) is reserved so that, just before the
// deadline, a forced convergence request — tools removed — can demand a final
// answer from whatever context has accumulated. This replaces the old
// "exceeded max tool rounds → hard failure → discard all work" behavior: the
// boundary is now a soft convergence trigger, never a discard (task #7066).
//
// Empty answers get exactly one nudge before the proposer is declared failed,
// so a single empty turn does not collapse the deliberation.
//
// onToken (may be nil) receives live content deltas for streaming UI.
var callEndpoint = func(
	ctx context.Context,
	ep EndpointRef,
	systemPrompt, userPrompt string,
	temperature float32,
	maxTokens int,
	onToken func(string),
	tools []embedded.OpenAIToolDef,
	dispatch embedded.MCPDispatcher,
	projectPath, sheepName string,
) (string, embedded.ChatUsage, error) {
	client := embedded.NewClient(ep.BaseURL, ep.APIKey, ep.Model)

	messages := []embedded.ChatMessage{
		{Role: embedded.ChatRoleSystem, Content: systemPrompt},
		{Role: embedded.ChatRoleUser, Content: userPrompt},
	}

	var totalUsage embedded.ChatUsage

	// No tools → single-shot request; nudge once if the answer comes back empty.
	if len(tools) == 0 {
		content, u, err := forceFinalAnswer(ctx, client, ep, messages, temperature, maxTokens, onToken, false)
		addUsage(&totalUsage, u)
		return content, totalUsage, err
	}

	// Create a per-proposer ToolRegistry for native tools (read_file, grep,
	// glob). MCP tools are routed through the shared dispatch function.
	var toolRegistry *embedded.ToolRegistry
	if projectPath != "" {
		// Build MCPToolDefs from the OpenAIToolDef list so the ToolRegistry
		// knows about MCP tools for WantsSheepName checks.
		var mcpDefs []embedded.MCPToolDef
		for _, td := range tools {
			if td.Function.Name != "read_file" && td.Function.Name != "grep" && td.Function.Name != "glob" &&
				td.Function.Name != "write_file" && td.Function.Name != "edit_file" && td.Function.Name != "bash" {
				mcpDefs = append(mcpDefs, embedded.MCPToolDef{
					Name:        td.Function.Name,
					Description: td.Function.Description,
					Parameters:  td.Function.Parameters,
				})
			}
		}
		toolRegistry = embedded.NewToolRegistry(projectPath, sheepName, mcpDefs, dispatch)
	}

	// Compute the convergence cutoff and reserve from the ctx deadline. Tool
	// exploration runs until convergeAt; the reserved tail (reserve) funds a
	// forced final-answer request. In production RunProposers always sets a
	// deadline, so hasCutoff is true; the no-deadline branch exists only for
	// tests (which return a final answer promptly and never loop unbounded).
	convergeAt, reserve, hasCutoff := convergenceCutoff(ctx)

	// ── Tool exploration phase (bounded by wall clock, not a round count) ──
	turn := 0
	var exploreErr error // a non-cancel error mid-exploration → salvage, don't discard
	for {
		// Approaching the deadline → stop exploring and force a final answer.
		if hasCutoff && !time.Now().Before(convergeAt) {
			break
		}

		turn++
		req := &embedded.ChatRequest{
			Model:       ep.Model,
			Messages:    messages,
			Temperature: temperature,
			MaxTokens:   maxTokens,
			Stream:      true,
			Tools:       tools,
			ToolChoice:  "auto",
		}

		msg, usage, err := chatTurn(ctx, client, req, onToken)
		addUsage(&totalUsage, usage)
		if err != nil {
			// User cancellation → abort immediately, no salvage.
			if ctx.Err() == context.Canceled {
				return "", totalUsage, err
			}
			// A transient send error mid-loop must not discard the accumulated
			// tool context (task #7077 MELCHIOR — previously any send error
			// returned instantly, throwing away useful exploration). Fall through
			// to forced convergence to salvage a final answer; remember the error
			// in case convergence also fails.
			//
			// The exploration deadline is the *expected* convergence trigger, not
			// a transport failure — never record it as exploreErr. Otherwise a
			// deadline that fires mid-turn followed by a failed convergence would
			// surface a bare "context deadline exceeded" instead of the far more
			// diagnostic "no substantive answer after convergence nudge" (task
			// #7081 review).
			if !errors.Is(err, context.DeadlineExceeded) {
				exploreErr = err
			}
			break
		}

		// A nil message, or an answer that carries no substantive content (empty
		// or tool-call markup), falls through to forced convergence, which
		// nudges once for a real answer.
		if msg == nil {
			break
		}
		if len(msg.ToolCalls) == 0 {
			// Only a gate-passing answer ends exploration here. Tool-call markup
			// emitted as text (task #7077 CASPER) has non-empty Content yet no
			// substance — returning it here would let the RunProposers gate
			// reject it with no chance to recover, so break to a nudged forced
			// convergence instead.
			if msg.Content != "" && CheckAnswerContent(msg.Content) == nil {
				return msg.Content, totalUsage, nil
			}
			break
		}

		// Tool calls present — execute each one and feed the results back.
		messages = append(messages, *msg)
		for idx, tc := range msg.ToolCalls {
			if tc.ID == "" {
				tc.ID = fmt.Sprintf("call_%d_%d", turn, idx)
			}
			messages = append(messages, executeProposerToolCall(ctx, tc, toolRegistry, dispatch, sheepName))
		}
	}

	// ── Forced convergence: drop tools, demand a final answer ──
	//
	// Run it on an independent budget detached from the exploration deadline
	// (task #7077 BALTHASAR). Sharing ctx was the defect: an exploration turn
	// that ran up to — or past — the deadline left the forced request no time to
	// produce an answer, so the reserve meant to *save* convergence was instead
	// consumed by exploration and convergence died with "context deadline
	// exceeded". WithoutCancel guarantees a fresh `reserve` budget regardless of
	// how exploration ended (deadline hit, transient error, or clean cutoff).
	//
	// Trade-off: WithoutCancel also detaches parent *cancellation*, so a user
	// stop issued after exploration ends is ignored for up to `reserve` while
	// convergence runs. This is deliberate — MAGI's completeness requirement
	// (always attempt a final answer from accumulated context) is favored over
	// instant cancellation at the tail of the budget (task #7081 review).
	fcCtx := ctx
	if hasCutoff {
		var fcCancel context.CancelFunc
		fcCtx, fcCancel = context.WithTimeout(context.WithoutCancel(ctx), reserve)
		defer fcCancel()
	}

	content, u, err := forceFinalAnswer(fcCtx, client, ep, messages, temperature, maxTokens, onToken, true)
	addUsage(&totalUsage, u)
	// Convergence could not rescue an answer after a mid-exploration transport
	// failure → surface the original transport error; it is more diagnostic than
	// "no substantive answer after nudge" for the failure line.
	if err != nil && exploreErr != nil {
		return "", totalUsage, exploreErr
	}
	return content, totalUsage, err
}

// convergenceCutoff returns the instant at which a proposer must stop tool
// exploration and force a final answer, plus the reserve — the tail of the
// remaining budget set aside to fund that forced request (used as an
// independent, detached timeout by callEndpoint). ok is false when ctx has no
// deadline, in which case exploration is unbounded (tests only — production
// always sets a per-proposer timeout).
func convergenceCutoff(ctx context.Context) (convergeAt time.Time, reserve time.Duration, ok bool) {
	dl, has := ctx.Deadline()
	if !has {
		return time.Time{}, 0, false
	}
	remaining := time.Until(dl)
	if remaining <= 0 {
		return dl, minConvergenceReserve, true // already past — converge immediately
	}
	reserve = remaining / 4
	if reserve < minConvergenceReserve {
		reserve = minConvergenceReserve
	}
	if reserve > remaining/2 {
		reserve = remaining / 2 // never spend more than half on convergence
	}
	return dl.Add(-reserve), reserve, true
}

// forceFinalAnswer performs the terminal, tools-off request(s) that produce a
// proposer's final answer. When appendDirective is set, a convergence
// instruction is added first (used when tool exploration was cut short). An
// empty answer triggers exactly one nudge before the proposer is declared
// failed, so a single empty response never collapses the deliberation.
func forceFinalAnswer(
	ctx context.Context,
	client *embedded.Client,
	ep EndpointRef,
	messages []embedded.ChatMessage,
	temperature float32,
	maxTokens int,
	onToken func(string),
	appendDirective bool,
) (string, embedded.ChatUsage, error) {
	msgs := messages
	if appendDirective {
		msgs = append(msgs, embedded.ChatMessage{
			Role:    embedded.ChatRoleUser,
			Content: convergenceDirective,
		})
	}

	var usage embedded.ChatUsage
	var lastGateErr error

	// Two attempts: the initial request plus one nudge on an unusable answer.
	for attempt := 0; attempt < 2; attempt++ {
		req := &embedded.ChatRequest{
			Model:       ep.Model,
			Messages:    msgs,
			Temperature: temperature,
			MaxTokens:   maxTokens,
			Stream:      true,
			// No tools — this request must yield a text answer.
		}

		msg, u, err := chatTurn(ctx, client, req, onToken)
		addUsage(&usage, u)
		if err != nil {
			return "", usage, err
		}

		// Success = a gate-passing answer, not merely non-empty content. A
		// tool-call-markup response has non-empty Content yet no substance, so a
		// bare Content != "" check let it slip past unnudged and then fail the
		// RunProposers gate (task #7077 CASPER). Align the nudge trigger with the
		// gate so an unusable answer earns its one retry.
		var content string
		if msg != nil {
			content = msg.Content
		}
		if gateErr := CheckAnswerContent(content); gateErr == nil {
			return content, usage, nil
		} else {
			lastGateErr = gateErr
		}

		// Unusable answer — nudge once more.
		if msg != nil {
			msgs = append(msgs, *msg)
		}
		msgs = append(msgs, embedded.ChatMessage{
			Role:    embedded.ChatRoleUser,
			Content: finalAnswerNudge,
		})
	}

	return "", usage, fmt.Errorf("%s: no substantive answer after convergence nudge: %w", ep.ID, lastGateErr)
}

// executeProposerToolCall validates and runs a single tool call from a
// proposer, returning the tool-role result message. Only read-only tools are
// permitted; write tools are rejected with an error fed back to the model so
// proposers may read but never mutate shared state (design §Phase 1.5).
func executeProposerToolCall(
	ctx context.Context,
	tc embedded.ToolCall,
	toolRegistry *embedded.ToolRegistry,
	dispatch embedded.MCPDispatcher,
	sheepName string,
) embedded.ChatMessage {
	toolName := tc.Func.Name

	// Validate: only read-only tools are allowed.
	if !IsReadOnlyTool(toolName) {
		return embedded.ChatMessage{
			Role:       embedded.ChatRoleTool,
			Content:    fmt.Sprintf("Error: tool %q is not allowed in MAGI deliberation (write tools are prohibited). Use only read-only tools.", toolName),
			ToolCallID: tc.ID,
		}
	}

	// Parse arguments.
	var args map[string]interface{}
	if tc.Func.Args != "" {
		if err := json.Unmarshal([]byte(tc.Func.Args), &args); err != nil {
			return embedded.ChatMessage{
				Role:       embedded.ChatRoleTool,
				Content:    fmt.Sprintf("JSON parse error for %s: %v", toolName, err),
				ToolCallID: tc.ID,
			}
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	// Inject sheep_name for MCP tools that need it.
	if toolRegistry != nil && toolRegistry.WantsSheepName(toolName) {
		args["sheep_name"] = sheepName
	}

	// Execute the tool.
	var resultStr string
	var execErr error
	switch {
	case toolRegistry != nil:
		resultStr, execErr = toolRegistry.Dispatch(ctx, toolName, args)
	case dispatch != nil:
		resultStr, _, execErr = dispatch(toolName, args)
	default:
		execErr = fmt.Errorf("no tool dispatcher available for %s", toolName)
	}
	if execErr != nil {
		resultStr = fmt.Sprintf("Error: %v", execErr)
	}

	return embedded.ChatMessage{
		Role:       embedded.ChatRoleTool,
		Content:    truncateToolResult(resultStr),
		ToolCallID: tc.ID,
	}
}

// addUsage accumulates token usage in place.
func addUsage(dst *embedded.ChatUsage, u embedded.ChatUsage) {
	dst.PromptTokens += u.PromptTokens
	dst.CompletionTokens += u.CompletionTokens
	dst.TotalTokens += u.TotalTokens
}

// truncateToolResult caps a tool result to a reasonable size for the chat
// context. Large outputs (e.g. reading a big file) would blow up the context
// window and degrade the model's deliberation.
func truncateToolResult(s string) string {
	const maxRunes = 8000
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "\n... [truncated]"
}

// RunProposersOptions bundles inputs for one blind parallel round.
type RunProposersOptions struct {
	Proposers       []ProposerSpec
	BaseSystem      string                      // base system prompt from the wiring layer
	UserPrompts     []string                    // per-slot user prompt (round 1: all identical; debate round: per-slot)
	Timeout         time.Duration               // per-proposer timeout (design: default 120s, set by caller)
	Temperature     float32                     // 0 → default 0.7 (diversity)
	OnOutput        func(string)                // live output sink, may be nil
	OnProposerToken func(slot int, text string) // live token stream, may be nil

	// Phase 1.5: read-only tools shared by all proposers.
	ToolDefs     []embedded.OpenAIToolDef
	ToolDispatch embedded.MCPDispatcher
	ProjectPath  string
	SheepName    string
}
// RunProposers calls every proposer in parallel and returns one result per
// slot, in slot order. Individual failures are recorded in Result.Err —
// callers decide whether enough succeeded (design §5.1).
//
// Uses sync.WaitGroup (not errgroup) so that one failure does not cancel
// the context for the others. Each proposer gets its own per-call timeout
// so the slowest model cannot hold the round hostage.
func RunProposers(ctx context.Context, opts RunProposersOptions) []ProposerResult {
	results := make([]ProposerResult, len(opts.Proposers))

	temp := opts.Temperature
	if temp == 0 {
		temp = 0.7 // diversity default (design §5.1)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, spec := range opts.Proposers {
		wg.Add(1)
		go func(slot int, sp ProposerSpec) {
			defer wg.Done()

			result := ProposerResult{Spec: sp}

			// Per-proposer timeout (design §5.1).
			callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()

			// Build the persona-augmented system prompt.
			systemPrompt := BuildProposerSystemPrompt(opts.BaseSystem, sp, slot)

			// Select the user prompt for this slot.
			userPrompt := ""
			if slot < len(opts.UserPrompts) {
				userPrompt = opts.UserPrompts[slot]
			}

			// Dispatch to the appropriate backend based on Provider.
			provider := sp.Provider
			if provider == "" {
				provider = ProviderEmbedded
			}

			var content string
			var usage embedded.ChatUsage
			var err error

			tokenCb := func(token string) {
				if opts.OnProposerToken != nil {
					opts.OnProposerToken(slot, token)
				}
			}

			switch provider {
			case ProviderClaudeCLI:
				content, usage, err = callClaudeCLI(callCtx, sp, systemPrompt, userPrompt, opts.ProjectPath, tokenCb)
			case ProviderOpenCodeCLI:
				content, usage, err = callOpenCodeCLI(callCtx, sp, systemPrompt, userPrompt, opts.ProjectPath, tokenCb)
			default: // ProviderEmbedded
				// Compute max tokens: ContextTokens / 4 (same rule as the
				// embedded agent loop). Fall back to DefaultContextTokens.
				ctxTokens := sp.Endpoint.ContextTokens
				if ctxTokens == 0 {
					ctxTokens = embedded.DefaultContextTokens
				}
				maxTokens := ctxTokens / 4

				content, usage, err = callEndpoint(callCtx, sp.Endpoint, systemPrompt, userPrompt, temp, maxTokens, tokenCb,
					opts.ToolDefs, opts.ToolDispatch, opts.ProjectPath, opts.SheepName)
			}

			if err != nil {
				result.Err = err
				emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, false, 0, err))
				results[slot] = result
				return
			}

			// Separate the self-reported confidence from the answer body.
			cleaned, conf := ExtractConfidence(content)

			// Content gate: leaked tool-call text or empty prose is a failure,
			// not an answer — record it like a transport error so the wiring
			// fallback can engage (lesson from task #7031).
			if gateErr := CheckAnswerContent(cleaned); gateErr != nil {
				result.Err = gateErr
				emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, false, 0, gateErr))
				results[slot] = result
				return
			}

			result.Answer = cleaned
			result.Confidence = conf
			result.Usage = usage

			emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, true, conf, nil))
			results[slot] = result
		}(i, spec)
	}

	wg.Wait()
	return results
}

// SuccessfulResults filters out failed slots, preserving order.
func SuccessfulResults(results []ProposerResult) []ProposerResult {
	out := make([]ProposerResult, 0, len(results))
	for _, r := range results {
		if r.Err == nil {
			out = append(out, r)
		}
	}
	return out
}

// emitOutput safely calls OnOutput under mutex. No-op when OnOutput is nil.
func emitOutput(mu *sync.Mutex, onOutput func(string), line string) {
	if onOutput == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	onOutput(line)
}

// formatProposerLine builds the live-output line for one proposer's completion.
// Format (design §5.2):
//
//	success: "[MAGI:0]   🔬 MELCHIOR-1 (qwen3-27b) 응답 완료 — 신뢰도 8/10\n"
//	failure: "[MAGI:0]   🔬 MELCHIOR-1 (qwen3-27b) 응답 실패 — <err>\n"
//
// When confidence is -1 (not reported), shows "신뢰도 미보고".
// The [MAGI:N] prefix allows the frontend to route the line to the correct
// proposer panel (slot N = 0, 1, or 2).
func formatProposerLine(spec ProposerSpec, slot int, success bool, confidence int, err error) string {
	emoji := PersonaEmoji(spec)
	displayName := PersonaDisplayName(spec, slot)
	model := proposerModelLabel(spec)
	prefix := fmt.Sprintf("[MAGI:%d] ", slot)

	if success {
		confStr := "신뢰도 미보고"
		if confidence >= 0 {
			confStr = fmt.Sprintf("신뢰도 %d/10", confidence)
		}
		return fmt.Sprintf("%s %s %s (%s) 응답 완료 — %s\n", prefix, emoji, displayName, model, confStr)
	}

	return fmt.Sprintf("%s %s %s (%s) 응답 실패 — %v\n", prefix, emoji, displayName, model, err)
}

// proposerModelLabel returns a display string for the model used by a proposer.
func proposerModelLabel(spec ProposerSpec) string {
	provider := spec.Provider
	if provider == "" {
		provider = ProviderEmbedded
	}
	switch provider {
	case ProviderClaudeCLI:
		if spec.ModelID != "" {
			return "claude:" + spec.ModelID
		}
		return "claude:default"
	case ProviderOpenCodeCLI:
		if spec.ModelID != "" {
			return "opencode:" + spec.ModelID
		}
		return "opencode:default"
	default:
		return spec.Endpoint.Model
	}
}

// ── CLI-based proposer backends ──────────────────────────────────────
//
// claude_cli and opencode_cli proposers run as subprocesses (like the
// aggregator's claude_cli path). They do NOT support read-only tools —
// the CLI subprocess owns its own tool loop. This is acceptable because
// MAGI Phase 1.5 tools are advisory (read-only exploration); a CLI
// proposer with its own agentic capabilities is a valid alternative.
//
// Streaming: CLI stdout is line-buffered; each line is forwarded to the
// onToken callback so the frontend can render live output.

// callClaudeCLI runs `claude --print` with an optional model flag.
func callClaudeCLI(ctx context.Context, spec ProposerSpec, systemPrompt, userPrompt, workDir string, onToken func(string)) (string, embedded.ChatUsage, error) {
	args := []string{"--print"}
	if spec.ModelID != "" {
		args = append(args, "--model", spec.ModelID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(systemPrompt + "\n\n" + userPrompt)
	envutil.SetCleanEnv(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("claude pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("claude start: %w", err)
	}

	var buf bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line + "\n")
		if onToken != nil {
			onToken(line + "\n")
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("claude wait: %w", err)
	}

	output := strings.TrimSpace(buf.String())
	if output == "" {
		return "", embedded.ChatUsage{}, fmt.Errorf("claude returned empty output")
	}
	return output, embedded.ChatUsage{}, nil
}

// callOpenCodeCLI runs `opencode run --format json` with an optional model flag.
func callOpenCodeCLI(ctx context.Context, spec ProposerSpec, systemPrompt, userPrompt, workDir string, onToken func(string)) (string, embedded.ChatUsage, error) {
	args := []string{"run", "--format", "json"}
	if spec.ModelID != "" {
		args = append(args, "-m", spec.ModelID)
	}

	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(systemPrompt + "\n\n" + userPrompt)
	envutil.SetCleanEnv(cmd)
	cmd.Env = append(cmd.Env, `OPENCODE_PERMISSION={"*":"allow"}`)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("opencode pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("opencode start: %w", err)
	}

	var buf bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line + "\n")

		// Parse OpenCode JSON events to extract text content for streaming.
		parsed := parseOpenCodeEvent(line)
		if parsed != "" && onToken != nil {
			onToken(parsed + "\n")
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("opencode wait: %w", err)
	}

	output := strings.TrimSpace(buf.String())
	if output == "" {
		return "", embedded.ChatUsage{}, fmt.Errorf("opencode returned empty output")
	}

	// Extract the final text from OpenCode JSON event stream.
	finalText := extractOpenCodeFinalText(output)
	if finalText == "" {
		finalText = output // fallback to raw output
	}
	return finalText, embedded.ChatUsage{}, nil
}

// parseOpenCodeEvent extracts displayable text from a single OpenCode JSON line.
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
	switch eventType {
	case "message":
		if content, ok := event["content"].(string); ok {
			return content
		}
	case "text":
		if content, ok := event["content"].(string); ok {
			return content
		}
	case "assistant":
		if content, ok := event["content"].(string); ok {
			return content
		}
	}
	return ""
}

// extractOpenCodeFinalText extracts the final assistant message from a
// sequence of OpenCode JSON events.
func extractOpenCodeFinalText(raw string) string {
	lines := strings.Split(raw, "\n")
	var lastText string
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
		if eventType == "message" || eventType == "text" || eventType == "assistant" {
			if content, ok := event["content"].(string); ok && content != "" {
				lastText = content
			}
		}
	}
	return lastText
}
