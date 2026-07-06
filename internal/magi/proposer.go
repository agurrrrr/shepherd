package magi

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
)

// maxProposerToolRounds limits how many tool-call iterations a single proposer
// can make before we force a final answer. Each round is one LLM call that
// returns tool_calls; the proposer reads files / queries state and then
// produces its deliberation answer.
const maxProposerToolRounds = 10

// callEndpoint sends a chat request — with optional read-only tools — and
// runs a mini agent loop until the model produces a final text answer (no
// tool_calls). It is the single seam for tests — override via the package
// variable to inject fakes.
//
// Phase 1.5: when tools and dispatch are non-empty, the request includes
// them and the model may return tool_calls. Each tool call is validated
// via IsReadOnlyTool (write tools are rejected), executed via the
// ToolRegistry (native) or dispatch (MCP), and the result is fed back as
// a tool-role message. The loop terminates when the model returns a
// content-only message (no tool_calls) or maxProposerToolRounds is hit.
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

	// Build initial messages.
	messages := []embedded.ChatMessage{
		{Role: embedded.ChatRoleSystem, Content: systemPrompt},
		{Role: embedded.ChatRoleUser, Content: userPrompt},
	}

	// Create a per-proposer ToolRegistry for native tools (read_file, grep,
	// glob). MCP tools are routed through the shared dispatch function.
	var toolRegistry *embedded.ToolRegistry
	if len(tools) > 0 && projectPath != "" {
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

	var totalUsage embedded.ChatUsage

	for round := 0; round <= maxProposerToolRounds; round++ {
		req := &embedded.ChatRequest{
			Model:       ep.Model,
			Messages:    messages,
			Temperature: temperature,
			MaxTokens:   maxTokens,
			Stream:      true,
		}
		if len(tools) > 0 {
			req.Tools = tools
			req.ToolChoice = "auto"
		}

		msg, _, usage, err := client.AccumulateStreamWithRetry(ctx, req, nil, onToken)
		if err != nil {
			return "", totalUsage, err
		}

		if usage != nil {
			totalUsage.PromptTokens += usage.PromptTokens
			totalUsage.CompletionTokens += usage.CompletionTokens
			totalUsage.TotalTokens += usage.TotalTokens
		}

		// Guard: nil message or empty content with no tool calls is a failure.
		if msg == nil {
			return "", totalUsage, fmt.Errorf("empty response from %s", ep.ID)
		}

		// No tool calls → final text answer.
		if len(msg.ToolCalls) == 0 {
			if msg.Content == "" {
				return "", totalUsage, fmt.Errorf("empty response from %s", ep.ID)
			}
			return msg.Content, totalUsage, nil
		}

		// Tool calls present — execute each one.
		// Add the assistant message with tool calls to history.
		messages = append(messages, *msg)

		for idx, tc := range msg.ToolCalls {
			// Assign fallback ID if missing.
			if tc.ID == "" {
				tc.ID = fmt.Sprintf("call_%d_%d", round, idx)
			}

			toolName := tc.Func.Name

			// Validate: only read-only tools are allowed.
			if !IsReadOnlyTool(toolName) {
				messages = append(messages, embedded.ChatMessage{
					Role:       embedded.ChatRoleTool,
					Content:    fmt.Sprintf("Error: tool %q is not allowed in MAGI deliberation (write tools are prohibited). Use only read-only tools.", toolName),
					ToolCallID: tc.ID,
				})
				continue
			}

			// Parse arguments.
			var args map[string]interface{}
			if tc.Func.Args != "" {
				if err := json.Unmarshal([]byte(tc.Func.Args), &args); err != nil {
					messages = append(messages, embedded.ChatMessage{
						Role:       embedded.ChatRoleTool,
						Content:    fmt.Sprintf("JSON parse error for %s: %v", toolName, err),
						ToolCallID: tc.ID,
					})
					continue
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
			if toolRegistry != nil {
				resultStr, execErr = toolRegistry.Dispatch(ctx, toolName, args)
			} else if dispatch != nil {
				resultStr, _, execErr = dispatch(toolName, args)
			} else {
				execErr = fmt.Errorf("no tool dispatcher available for %s", toolName)
			}

			if execErr != nil {
				resultStr = fmt.Sprintf("Error: %v", execErr)
			}

			messages = append(messages, embedded.ChatMessage{
				Role:       embedded.ChatRoleTool,
				Content:    truncateToolResult(resultStr),
				ToolCallID: tc.ID,
			})
		}

		// Loop continues — model will see tool results and produce next response.
	}

	// Exceeded max rounds — return whatever content we have from the last
	// message (if any), or an error.
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if last.Role == embedded.ChatRoleAssistant && last.Content != "" {
			return last.Content, totalUsage, nil
		}
	}
	return "", totalUsage, fmt.Errorf("proposer exceeded max tool rounds (%d) without final answer", maxProposerToolRounds)
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

			// Compute max tokens: ContextTokens / 4 (same rule as the
			// embedded agent loop). Fall back to DefaultContextTokens.
			ctxTokens := sp.Endpoint.ContextTokens
			if ctxTokens == 0 {
				ctxTokens = embedded.DefaultContextTokens
			}
			maxTokens := ctxTokens / 4

			content, usage, err := callEndpoint(callCtx, sp.Endpoint, systemPrompt, userPrompt, temp, maxTokens, func(token string) {
				if opts.OnProposerToken != nil {
					opts.OnProposerToken(slot, token)
				}
			}, opts.ToolDefs, opts.ToolDispatch, opts.ProjectPath, opts.SheepName)
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
	model := spec.Endpoint.Model
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
