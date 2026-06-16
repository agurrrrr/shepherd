package embedded

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultTemperature is the default sampling temperature for the embedded agent.
const DefaultTemperature = 0.7

// DefaultFrequencyPenalty / DefaultPresencePenalty discourage local models from
// looping on the same token/phrase (task #6008). Values kept modest so creative
// tasks aren't overly constrained.
const DefaultFrequencyPenalty = 0.3
const DefaultPresencePenalty = 0.3

// imagePathRe matches image file paths in text (used by extractAttachedImages
// and MarkPreReadImages). Kept at package level to avoid recompiling the same
// regex in two places.
var imagePathRe = regexp.MustCompile(`(/[^\s"\']+?\.(jpg|jpeg|png|gif|webp|bmp|JPG|JPEG|PNG|GIF|WEBP|BMP))`)

// maxRepeatedToolTurns is the number of consecutive turns with an identical
// tool-call signature tolerated before the task is declared stuck. Five
// identical calls in a row (e.g. read_file on an image the model cannot view)
// is never legitimate progress.
const maxRepeatedToolTurns = 4

// replacementCharRatio / minDegenerateRunes gate the degeneration guard. A
// short reply with a stray U+FFFD must not trip it, so require both a minimum
// length and a high replacement-char fraction.
const replacementCharRatio = 0.2
const minDegenerateRunes = 20

// toolCallsSignature builds a stable, order-independent signature for a turn's
// tool calls (name + trimmed args). Used to detect a model stuck repeating the
// exact same call(s) turn after turn. Returns "" for no calls.
func toolCallsSignature(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, len(calls))
	for _, tc := range calls {
		parts = append(parts, tc.Func.Name+"("+strings.TrimSpace(tc.Func.Args)+")")
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// isDegenerateOutput reports whether s is dominated by U+FFFD replacement
// characters — the hallmark of a local model producing broken multi-byte text
// after a silent context shift (task #6145). Short strings are exempt so a
// single stray replacement character does not trip the guard.
func isDegenerateOutput(s string) bool {
	total, bad := 0, 0
	for _, r := range s {
		total++
		if r == '�' {
			bad++
		}
	}
	if total < minDegenerateRunes {
		return false
	}
	return float64(bad)/float64(total) >= replacementCharRatio
}

// isFutureIntention reports whether the text ends with a future-action intention
// declaration like "이제 ~하겠습니다", "let me now ~", "I'll now ~" — patterns
// where the model announces what it *will* do but doesn't actually call a tool.
// This is a key symptom of false-completion (task #6290, mitigation ①).
var futureIntentionKorean = regexp.MustCompile(`(?i)(이제|지금부터).*?(하겠습니다|할게요|해볼게요|해드리겠습니다|다듬겠습니다|채워넣겠습니다|작성하겠습니다)`)

var futureIntentionEnglish = regexp.MustCompile(`(?i)(let me (now )?(finish|complete|implement|build|run|execute|check)|i('ll| will) (now )?(finish|complete|implement|build|run|execute)|now i('ll| will))`)

func isFutureIntention(content string) bool {
	s := strings.TrimSpace(content)
	if s == "" {
		return false
	}
	if futureIntentionKorean.MatchString(s) {
		return true
	}
	if futureIntentionEnglish.MatchString(s) {
		return true
	}
	return false
}

// hasBuildCommandInPrompt reports whether the user prompt mentions a build or
// compilation command that should be verified before task completion. This is
// used by the build-verification gate (task #6290, mitigation ②).
func hasBuildCommandInPrompt(prompt string) bool {
	lower := strings.ToLower(prompt)
	patterns := []string{
		"gradlew", "gradle",
		"go build", "go test", "go run",
		"npm run build", "npm run test", "npm build", "npm test",
		"yarn build", "yarn test",
		"cargo build", "cargo test",
		"make build", "make test",
		"compileDebugKotlin", "compileDebugJava",
		"mvn compile", "mvn build", "mvn test",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}


// Run executes the embedded agent loop: model → tool calls → execute → retry.
func Run(ctx context.Context, opts ExecuteOptions) (*ExecuteResult, error) {
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = DefaultMaxIterations
	}
	if opts.ContextTokens <= 0 {
		opts.ContextTokens = DefaultContextTokens
	}

	// Build initial messages
	messages := []ChatMessage{
		{Role: ChatRoleSystem, Content: opts.SystemPrompt},
	}

	// If the user prompt has "[Attached files]", extract image paths and load
	// them as actual image_url content parts so the vision model can see them.
	// Without this, the model only sees file paths as text and cannot analyze
	// the image content, leading to incorrect responses.
	userMsg := ChatMessage{Role: ChatRoleUser}
	if strings.Contains(opts.UserPrompt, "[Attached files]") {
		parts := extractAttachedImages(opts.UserPrompt)
		if len(parts) > 0 {
			userMsg.ContentParts = parts
			// Strip the [Attached files] block from the text content so the
			// model doesn't see redundant file paths alongside the actual images.
			userMsg.Content = stripAttachedFilesBlock(opts.UserPrompt)
		} else {
			// No valid images found; fall back to plain text.
			userMsg.Content = opts.UserPrompt
		}
	} else {
		userMsg.Content = opts.UserPrompt
	}
	messages = append(messages, userMsg)

	// Ensure base_url has /v1 suffix
	baseURL := opts.BaseURL
	if !strings.HasSuffix(strings.ToLower(baseURL), "/v1") {
		baseURL = strings.TrimRight(baseURL, "/") + "/v1"
	}

	client := NewClient(baseURL, opts.APIKey, opts.Model)
	toolRegistry := NewToolRegistry(opts.ProjectPath, opts.SheepName, opts.MCPDefs, opts.MCPDispatch)

	// Vision is enabled either by the endpoint's model capability (opts.Vision)
	// or because the task prompt carries attached files (the web UI prepends an
	// "[Attached files]" block). The capability flag is the important case: a
	// vision model running a task with no attachments (e.g. "capture a screenshot
	// and check the screen") must still be able to view images it produces at
	// runtime via read_file. The marker is kept as an OR so existing
	// attachment-based tasks on non-vision-flagged endpoints keep working (task
	// #6145: a vision model was blinded because only the marker gated vision).
	hasAttachedFiles := strings.Contains(opts.UserPrompt, "[Attached files]")
	toolRegistry.SetVision(opts.Vision || hasAttachedFiles)

	// Pre-register any image paths mentioned in the initial prompt as already
	// read. This prevents the model from calling read_file on them — they are
	// already provided in the context. Without this, the model may enter an
	// infinite loop of calling read_file → image loaded → call read_file again.
	if hasAttachedFiles {
		toolRegistry.MarkPreReadImages(opts.UserPrompt)
	}

	// Override tool definitions if provided
	var toolDefs []OpenAIToolDef
	if len(opts.Tools) > 0 {
		toolDefs = opts.Tools
	} else {
		toolDefs = toolRegistry.OpenAIToolDefs()
	}

	var (
		totalPromptTokens     int64
		totalCompletionTokens int64
		consecutiveEmpty      int
		// Stuck-guard state: a confused local model (e.g. one blinded to an
		// image it keeps trying to read) can repeat the exact same tool call
		// turn after turn, making no progress while the context grows until it
		// degenerates (task #6145). lastToolSig holds the previous turn's tool
		// signature; repeatedToolTurns counts consecutive identical repeats.
		lastToolSig       string
		repeatedToolTurns int
		// False-completion guard: tracks whether bash was called during execution
		// so the build-verification gate can detect missing build steps (task #6290, mitigation ②).
		bashCalled        bool
	)

	for iteration := 0; iteration < opts.MaxIterations; iteration++ {
		// Poll for injected user prompts (non-blocking). Each injected prompt is
		// appended as a {role: user} message so the model sees it as a natural
		// continuation of the conversation. This is checked at the top of each
		// loop iteration so injected messages are included in the next LLM call.
		if opts.InjectCh != nil {
		pollLoop:
			for {
				select {
				case injected, ok := <-opts.InjectCh:
					if !ok {
						opts.InjectCh = nil // channel closed; stop polling
						break
					}
					messages = append(messages, ChatMessage{
						Role:    ChatRoleUser,
						Content: injected,
					})
					if opts.OnOutput != nil {
						opts.OnOutput("💬 [주입된 메시지]: " + injected)
					}
				default:
					break pollLoop
				}
			}
		}

		// Trim messages to fit context window. If trimming would actually drop
		// turns and a handoff is allowed (queue empty), finish this task with a
		// summary + queue the remaining work as a follow-up task instead —
		// trimming destroys context and tends to degrade the model.
		trimmed := trimMessages(messages, opts.ContextTokens)
		if len(trimmed) < len(messages) &&
			opts.EnqueueFollowUp != nil &&
			(opts.ShouldHandoff == nil || opts.ShouldHandoff()) {
			if res, ok := attemptHandoff(ctx, client, opts, trimmed, totalPromptTokens, totalCompletionTokens); ok {
				return res, nil
			}
		}
		messages = trimmed

		// Build request
		req := &ChatRequest{
			Model:       opts.Model,
			Messages:    messages,
			Tools:       toolDefs,
			ToolChoice:  "auto",
			Temperature: DefaultTemperature,
			// Mild penalties to steer local models away from looping on the same
			// phrase. The streaming repetition guard (AccumulateStream) is the hard
			// backstop; these just make the loop less likely in the first place.
			FrequencyPenalty: DefaultFrequencyPenalty,
			PresencePenalty:  DefaultPresencePenalty,
			MaxTokens:        opts.ContextTokens / 4,
			Stream:           true,
			StreamOptions:    &StreamOptions{IncludeUsage: true},
		}

		// Accumulate streaming response
		msg, finishReason, usage, err := client.AccumulateStream(ctx, req)
		if err != nil {
			return &ExecuteResult{
				Result:           "",
				Incomplete:       true,
				IncompleteReason: fmt.Sprintf("API error: %v", err),
			}, nil
		}

		// Accumulate token usage
		if usage != nil {
			totalPromptTokens += usage.PromptTokens
			totalCompletionTokens += usage.CompletionTokens
		}

		// Surface the model's "thinking" (reasoning_content) for this turn so the
		// live output shows what the model is reasoning about, like Claude does.
		if opts.OnOutput != nil {
			if think := strings.TrimSpace(msg.ReasoningContent); think != "" {
				opts.OnOutput("💭 " + think)
			}
		}

		// The stream was aborted because the model degenerated into repeating the
		// same phrase (task #6008). Stop the whole task: once a local model starts
		// looping it does not recover, and feeding the garbage back only spreads it.
		// Checked before tool handling so a stray parse of the repeated text can't
		// trigger a bogus tool call.
		if finishReason == "repetition" {
			return &ExecuteResult{
				Result:           "",
				Incomplete:       true,
				IncompleteReason: "degenerate repetition detected (model looping)",
				PromptTokens:     totalPromptTokens,
				CompletionTokens: totalCompletionTokens,
			}, nil
		}

		// Degeneration guard: after a SILENT context shift (llama.cpp truncates
		// the prompt instead of returning an error), local models often emit
		// text dense with U+FFFD replacement characters (broken multi-byte /
		// 깨진 한글) and loop on it until max iterations (task #6145). The
		// empty-response guard never fires because the turns are non-empty.
		// Detect the high replacement-char ratio and stop now rather than
		// feeding the garbage back into the context.
		if isDegenerateOutput(msg.Content) || isDegenerateOutput(msg.ReasoningContent) {
			return &ExecuteResult{
				Result:           "",
				Incomplete:       true,
				IncompleteReason: "model output degenerated (likely silent context overflow)",
				PromptTokens:     totalPromptTokens,
				CompletionTokens: totalCompletionTokens,
			}, nil
		}

		// Handle tool calls (native function-calling)
		if len(msg.ToolCalls) > 0 {
			// Stuck guard: if the model repeats the exact same tool call(s)
			// (identical name + args) for several turns running, it is making no
			// progress — kill the task instead of letting the context grow until
			// it degenerates. Updated before execution so a repeated rejection
			// (e.g. read_file on an unviewable image) is caught.
			sig := toolCallsSignature(msg.ToolCalls)
			if sig != "" && sig == lastToolSig {
				repeatedToolTurns++
			} else {
				repeatedToolTurns = 0
				lastToolSig = sig
			}
			if repeatedToolTurns >= maxRepeatedToolTurns {
				return &ExecuteResult{
					Result:           "",
					Incomplete:       true,
					IncompleteReason: "stuck: repeated identical tool calls with no progress",
					PromptTokens:     totalPromptTokens,
					CompletionTokens: totalCompletionTokens,
				}, nil
			}

			// Add assistant message with tool calls to history.
			// Sanitize args first: malformed JSON in tool_calls causes llama.cpp to
			// return HTTP 500 on the very next request (its grammar engine rejects them).
			sanitized := sanitizeToolCallArgs(*msg)
			messages = append(messages, sanitized)

			// Surface any narration the model wrote alongside its tool calls (the
			// "말하는 거" — e.g. "Let me check the docs first.").
			if opts.OnOutput != nil {
				if narration := strings.TrimSpace(msg.Content); narration != "" {
					opts.OnOutput(narration)
				}
			}

			// Execute each tool call
			for idx, tc := range msg.ToolCalls {
				// B2: Assign a fallback ID to tool calls that arrived without one.
				// Some servers (llama.cpp) return empty tc.ID in streaming mode,
				// which causes tool result messages to fail validation on the next turn.
				if tc.ID == "" {
					tc.ID = fmt.Sprintf("call_%d_%d", iteration, idx)
				}

				// Show the tool call (name + command/args) BEFORE running it, in the
				// "🔧 name → detail" format the web UI parses (OutputViewer.svelte).
				if opts.OnOutput != nil {
					parsedArgs, _ := normalizeJSON(tc.Func.Args)
					opts.OnOutput(toolCallHeader(tc.Func.Name, parsedArgs))
				}

				result, err := dispatchTool(ctx, toolRegistry, tc, opts)
				if tc.Func.Name == "bash" {
					bashCalled = true
				}
				var resultStr string
				if err != nil {
					resultStr = fmt.Sprintf("Error: %v", err)
				} else {
					resultStr = result
				}

				// Stream the result preview as an indented block (rendered as a
				// monospace result box by the web UI).
				if opts.OnOutput != nil {
					if out := indentResult(resultStr); out != "" {
						opts.OnOutput(out)
					}
				}

				messages = append(messages, ChatMessage{
					Role:       ChatRoleTool,
					Content:    truncateToolResult(resultStr),
					ToolCallID: tc.ID,
				})
			}
			// All tool results are now appended; surface any images read_file
			// produced as a following user message (OpenAI requires tool results
			// to immediately follow the assistant's tool_calls, so images cannot
			// be interleaved above).
			messages = appendPendingImages(messages, toolRegistry)
			continue
		}

		// No native tool calls — check for leaked tool calls in text
		if msg.Content != "" && looksLikeLeakedToolCall(msg.Content) {
			// Try to parse and execute leaked tool calls
			leaked := parseLeakedToolCalls(msg.Content)
			if len(leaked) > 0 {
				// Reconstruct the assistant message so the history carries proper
				// tool_calls instead of raw leaked marker text. Without this, servers
				// that validate tool_call_id matching (vLLM, llama.cpp) reject the
				// next request with 400/500 because role:tool messages don't match
				// any tool_calls in the preceding assistant message (A3/A4).
				toolCalls := make([]ToolCall, 0, len(leaked))
				for _, tc := range leaked {
					toolCalls = append(toolCalls, tc.ToolCall)
				}
				// Sanitize args so malformed JSON doesn't cause HTTP 500 on next request.
				toolCalls = sanitizeLeakedToolCalls(toolCalls)

				// Strip leaked marker blocks from content, keeping only surrounding prose.
				cleanContent := removeLeakedMarkers(msg.Content)

				assistantMsg := ChatMessage{
					Role:      ChatRoleAssistant,
					Content:   cleanContent,
					ToolCalls: toolCalls,
				}
				messages = append(messages, assistantMsg)

				// Surface any narration the model wrote alongside its leaked tool calls.
				if opts.OnOutput != nil {
					if narration := strings.TrimSpace(cleanContent); narration != "" {
						opts.OnOutput(narration)
					}
				}

				for _, tc := range leaked {
					args, parseErr := normalizeJSON(tc.Func.Args)
					if parseErr != nil {
						// Feed error back to model for self-repair.
						messages = append(messages, ChatMessage{
							Role:       ChatRoleTool,
							Content:    fmt.Sprintf("JSON parse error in arguments for %s: %v. Please retry with valid JSON.", tc.Func.Name, parseErr),
							ToolCallID: tc.ID,
						})
						continue
					}

					// Show the recovered tool call before running it.
					if opts.OnOutput != nil {
						opts.OnOutput(toolCallHeader(tc.Func.Name, args))
					}

					// Route leaked tool calls through dispatchTool so they get the same
					// protections as native tool calls: 5-minute timeout, ctx cancel
					// propagation (task stop), and sheep_name injection. Previously this
					// path called toolRegistry.Dispatch directly, bypassing all guards.
					result, err := dispatchTool(ctx, toolRegistry, tc.ToolCall, opts)
					if tc.ToolCall.Func.Name == "bash" {
						bashCalled = true
					}
					var resultStr string
					if err != nil {
						resultStr = fmt.Sprintf("Error: %v", err)
					} else {
						resultStr = result
					}

					if opts.OnOutput != nil {
						if out := indentResult(resultStr); out != "" {
							opts.OnOutput(out)
						}
					}

					messages = append(messages, ChatMessage{
						Role:       ChatRoleTool,
						Content:    truncateToolResult(resultStr),
						ToolCallID: tc.ID,
					})
				}
				messages = appendPendingImages(messages, toolRegistry)
				continue
			}
		}

		// Pure text response — check for empty consecutive responses.
		// Do NOT add the empty message to history: it wastes context and does not
		// help the model recover. Instead, inject a nudge so the model knows it
		// should produce output.
		contentEmpty := strings.TrimSpace(msg.Content) == ""
		reasoningPresent := strings.TrimSpace(msg.ReasoningContent) != ""

		// Check for length truncation FIRST, before empty response detection.
		// When the model hits context length limit with finish_reason: "length"
		// and returns empty content, we should report it as a truncation error
		// rather than counting it toward the empty response loop counter.
		if finishReason == "length" && contentEmpty {
			return &ExecuteResult{
				Result:           "",
				Incomplete:       true,
				IncompleteReason: "response truncated (max tokens reached)",
				PromptTokens:     totalPromptTokens,
				CompletionTokens: totalCompletionTokens,
			}, nil
		}

		if contentEmpty {
			consecutiveEmpty++

			// A turn with reasoning_content (model still thinking) or an explicit
			// finish_reason "stop" deserves more patience than a hard-empty turn,
			// but must NOT reset the counter to zero: a degenerate model (e.g.
			// after context overflow) can emit garbage reasoning-only turns
			// forever, looping until max iterations while spamming 💭 output.
			limit := 3
			if reasoningPresent || finishReason == "stop" {
				limit = 6
			}

			if consecutiveEmpty >= limit {
				return &ExecuteResult{
					Result:           "",
					Incomplete:       true,
					IncompleteReason: "empty response loop detected",
					PromptTokens:     totalPromptTokens,
					CompletionTokens: totalCompletionTokens,
				}, nil
			}
			// B7: Skip adding a nudge if the last message is already one — prevents
			// stacking duplicate "Please continue" messages when the model keeps
			// returning empty responses.
			lastNudge := len(messages) > 0 &&
				messages[len(messages)-1].Role == ChatRoleUser &&
				messages[len(messages)-1].Content == "Please continue with the task."
			if !lastNudge {
				messages = append(messages, ChatMessage{
					Role:    ChatRoleUser,
					Content: "Please continue with the task.",
				})
			}
			continue
		}
		consecutiveEmpty = 0

		// ── Mitigation ①: Future-intention nudge (task #6290) ──
		// If there are no tool calls AND the content looks like a future-action
		// declaration ("이제 ~하겠습니다", "let me now ~"), do NOT treat it as a
		// completion. Instead, inject a nudge and ask the model to actually do it.
		if isFutureIntention(msg.Content) {
			if opts.OnOutput != nil {
				opts.OnOutput("⚠️ [미래형 선언 감지]: 선언한 작업을 실제 도구 호출로 완료해주세요.")
			}
			messages = append(messages, ChatMessage{
				Role:    ChatRoleAssistant,
				Content: msg.Content,
			})
			messages = append(messages, ChatMessage{
				Role: ChatRoleUser,
				Content: "위에서 선언한 작업을 실제로 도구 호출(bash, write_file 등)로 완료해주세요. " +
					"단순히 '하겠습니다'라고 말하는 대신, 실제 파일 수정이나 빌드 실행을 해주세요.",
			})
			continue
		}

		// ── Mitigation ②: Build-verification gate (task #6290) ──
		// If the user prompt mentions a build/compile command but bash was never
		// called during the entire execution, mark as incomplete. This catches
		// false completions where the model wrote files but never verified them.
		if hasBuildCommandInPrompt(opts.UserPrompt) && !bashCalled {
			if opts.OnOutput != nil {
				opts.OnOutput("⚠️ [빌드 검증 게이트]: 프롬프트에 빌드 명령이 있지만 bash가 호출되지 않았습니다.")
			}
			return &ExecuteResult{
				Result:           msg.Content,
				Incomplete:       true,
				IncompleteReason: "required build verification was never run (bash was not called)",
				PromptTokens:     totalPromptTokens,
				CompletionTokens: totalCompletionTokens,
			}, nil
		}

		// Successful completion
		if opts.OnOutput != nil {
			opts.OnOutput(msg.Content)
		}

		return &ExecuteResult{
			Result:           msg.Content,
			PromptTokens:     totalPromptTokens,
			CompletionTokens: totalCompletionTokens,
		}, nil
	}

	return &ExecuteResult{
		Result:           "",
		Incomplete:       true,
		IncompleteReason: fmt.Sprintf("max iterations (%d) exceeded", opts.MaxIterations),
		PromptTokens:     totalPromptTokens,
		CompletionTokens: totalCompletionTokens,
	}, nil
}

// appendPendingImages drains any images read_file buffered during the turn and
// appends them as a single user message with image_url content parts, so a
// vision-capable model can view them. Returns messages unchanged when there are
// no pending images.
func appendPendingImages(messages []ChatMessage, tr *ToolRegistry) []ChatMessage {
	imgs := tr.DrainPendingImages()
	if len(imgs) == 0 {
		return messages
	}
	parts := make([]ContentPart, 0, len(imgs)+1)
	parts = append(parts, ContentPart{Type: "text", Text: "Attached image(s) loaded by read_file:"})
	for _, img := range imgs {
		parts = append(parts, ContentPart{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: img.dataURL},
		})
	}
	return append(messages, ChatMessage{Role: ChatRoleUser, ContentParts: parts})
}

// dispatchTool executes a tool call and returns the result.
func dispatchTool(ctx context.Context, tr *ToolRegistry, tc ToolCall, opts ExecuteOptions) (string, error) {
	args, err := normalizeJSON(tc.Func.Args)
	if err != nil {
		return "", fmt.Errorf("JSON parse error for %s: %w", tc.Func.Name, err)
	}

	// Inject sheep_name only for MCP tools whose schema declares it (shepherd's
	// own browser_* tools). External MCP servers with strict unmarshaling reject
	// unknown fields, so blanket injection broke them (task #6142).
	if tr.WantsSheepName(tc.Func.Name) {
		args["sheep_name"] = opts.SheepName
	}

	// Run the tool in a goroutine so a hung tool (e.g. a CDP call stuck
	// mid-navigation) cannot freeze the agent loop forever (task #5985), and so
	// task stop (ctx cancel) interrupts the wait. The ctx is also forwarded to
	// Dispatch so native tools (notably bash) can react to cancellation. On
	// timeout the goroutine may leak, but the loop reports the error and keeps going.
	type toolResult struct {
		result string
		err    error
	}
	done := make(chan toolResult, 1)
	go func() {
		result, err := tr.Dispatch(ctx, tc.Func.Name, args)
		done <- toolResult{result, err}
	}()

	select {
	case r := <-done:
		return r.result, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("tool %s aborted: %w", tc.Func.Name, ctx.Err())
	case <-time.After(toolDispatchTimeout):
		return "", fmt.Errorf("tool %s timed out after %s", tc.Func.Name, toolDispatchTimeout)
	}
}

// toolDispatchTimeout is the hard upper bound for a single tool call. Browser
// CDP calls are bounded tighter inside internal/browser; this is the backstop
// for anything that slips through (external MCP servers, long shell commands).
const toolDispatchTimeout = 5 * time.Minute

// sanitizeToolCallArgs ensures every tool call in the message carries valid JSON
// arguments. If the args are truncated/malformed (a Qwen3 streaming artifact),
// this repairs or replaces them so the next API request does not embed broken
// JSON that causes llama.cpp to return HTTP 500.
func sanitizeToolCallArgs(msg ChatMessage) ChatMessage {
	if len(msg.ToolCalls) == 0 {
		return msg
	}
	sanitized := msg
	sanitized.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
	copy(sanitized.ToolCalls, msg.ToolCalls)

	for i, tc := range sanitized.ToolCalls {
		if tc.Func.Args == "" {
			sanitized.ToolCalls[i].Func.Args = "{}"
			continue
		}
		var probe map[string]interface{}
		if json.Unmarshal([]byte(tc.Func.Args), &probe) == nil {
			continue // already valid JSON
		}
		// Attempt structural repair
		repaired := repairTruncatedJSON(tc.Func.Args)
		if json.Unmarshal([]byte(repaired), &probe) == nil {
			sanitized.ToolCalls[i].Func.Args = repaired
			continue
		}
		// Last resort: empty object (avoids HTTP 500 from llama.cpp grammar parser)
		sanitized.ToolCalls[i].Func.Args = "{}"
	}
	return sanitized
}

// handoffMarker separates the handoff summary from the follow-up task prompt
// in the model's final answer. Kept ASCII-only so any model can reproduce it.
const handoffMarker = "===NEXT_TASK==="

// attemptHandoff is called when the conversation has outgrown the context
// window and the queue is idle. It asks the model (with the trimmed history,
// no tools) for a completion summary plus a self-contained follow-up prompt,
// queues the follow-up via opts.EnqueueFollowUp, and returns a completed
// ExecuteResult. Returns ok=false on any failure so the caller falls back to
// plain trimming.
func attemptHandoff(ctx context.Context, client *Client, opts ExecuteOptions, trimmed []ChatMessage, promptTokens, completionTokens int64) (*ExecuteResult, bool) {
	instruction := "컨텍스트 한계에 도달했다. 이번 작업은 여기서 마무리한다.\n" +
		"1) 지금까지 수행한 작업과 결과를 요약하라.\n" +
		"2) 아직 남은 작업이 있으면, 마지막에 '" + handoffMarker + "' 한 줄을 쓰고 그 아래에 남은 작업을 새 작업 프롬프트로 작성하라. " +
		"새 작업은 이 대화 내용을 볼 수 없으므로 필요한 파일 경로, 지금까지의 결정사항, 주의점을 빠짐없이 포함하라.\n" +
		"남은 작업이 없으면 '" + handoffMarker + "' 섹션을 생략하라. 도구는 호출하지 마라."
	msgs := append(append([]ChatMessage{}, trimmed...), ChatMessage{
		Role:    ChatRoleUser,
		Content: instruction,
	})

	req := &ChatRequest{
		Model:         opts.Model,
		Messages:      msgs,
		Temperature:   DefaultTemperature,
		MaxTokens:     opts.ContextTokens / 4,
		Stream:        true,
		StreamOptions: &StreamOptions{IncludeUsage: true},
	}
	msg, _, usage, err := client.AccumulateStream(ctx, req)
	if err != nil || msg == nil || strings.TrimSpace(msg.Content) == "" {
		return nil, false
	}
	if usage != nil {
		promptTokens += usage.PromptTokens
		completionTokens += usage.CompletionTokens
	}

	summary := strings.TrimSpace(msg.Content)
	followUp := ""
	if i := strings.Index(summary, handoffMarker); i >= 0 {
		followUp = strings.TrimSpace(summary[i+len(handoffMarker):])
		summary = strings.TrimSpace(summary[:i])
	}
	if summary == "" {
		return nil, false
	}

	if opts.OnOutput != nil {
		opts.OnOutput("⚠️ 컨텍스트 한계 도달 — 작업을 요약하고 마무리합니다.")
		opts.OnOutput(summary)
	}
	if followUp != "" {
		if err := opts.EnqueueFollowUp(followUp); err != nil {
			if opts.OnOutput != nil {
				opts.OnOutput("⚠️ 후속 작업 큐 추가 실패: " + err.Error())
			}
			// Surface the follow-up in the result so the work isn't lost.
			summary += "\n\n[후속 작업 (큐 추가 실패, 수동 등록 필요)]\n" + followUp
		} else if opts.OnOutput != nil {
			opts.OnOutput("📋 남은 작업을 후속 작업으로 큐에 추가했습니다.")
		}
	}

	return &ExecuteResult{
		Result:           summary,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}, true
}

// truncate cuts a string to maxLen runes and adds "..." if truncated.
// Uses rune-based truncation to avoid cutting multi-byte UTF-8 characters
// (like Korean hangul) mid-byte, which would produce replacement characters (�).
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// truncateToolResult limits tool output stored in message history to 8 000
// characters. Very large outputs (e.g. full file contents printed by bash)
// blow up the context window quickly; truncating here keeps the conversation
// manageable while still giving the model the most important prefix.
func truncateToolResult(s string) string {
	const maxToolResultChars = 8000
	runes := []rune(s)
	if len(runes) <= maxToolResultChars {
		return s
	}
	return string(runes[:maxToolResultChars]) + fmt.Sprintf("\n...[truncated %d chars]", len(runes)-maxToolResultChars)
}

// toolArgSummary extracts a short, human-readable summary of a tool call's
// arguments for live output — e.g. the bash command, search pattern, or target
// file path. Returns "" when no well-known argument is present.
func toolArgSummary(args map[string]interface{}) string {
	if args == nil {
		return ""
	}
	for _, key := range []string{"command", "pattern", "path", "file_path", "query", "url"} {
		if v, ok := args[key].(string); ok && v != "" {
			return truncate(v, 80)
		}
	}
	return ""
}

// toolCallHeader builds the "🔧 name → detail" header line that the web UI
// (OutputViewer.svelte) parses to display a tool call. The arrow separator and
// detail are omitted when there is no summarizable argument.
func toolCallHeader(name string, args map[string]interface{}) string {
	if summary := toolArgSummary(args); summary != "" {
		return fmt.Sprintf("🔧 %s → %s", name, summary)
	}
	return fmt.Sprintf("🔧 %s", name)
}

// indentResult formats a tool result preview as an indented block so the web UI
// renders it as a monospace result box (it classifies lines starting with 2+
// spaces as "result"). Returns "" when there is no visible output.
func indentResult(s string) string {
	s = strings.TrimRight(s, "\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	s = truncate(s, 500)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

// extractAttachedImages scans the user prompt for image file paths in the
// "[Attached files]" block and loads them as base64 data URLs. It returns
// ContentParts with a text part (the prompt text without the attached block)
// followed by image_url parts for each valid image. Returns nil if no images
// are found.
func extractAttachedImages(prompt string) []ContentPart {
	// Match image file paths in the prompt (same regex as MarkPreReadImages).
	matches := imagePathRe.FindAllStringSubmatch(prompt, -1)

	var parts []ContentPart
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		path := m[1]

		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		mime := http.DetectContentType(data)
		if !strings.HasPrefix(mime, "image/") {
			continue
		}

		dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		parts = append(parts, ContentPart{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: dataURL},
		})
	}

	if len(parts) == 0 {
		return nil
	}

	// Prepend text part with the prompt text (attached files block stripped)
	parts = append([]ContentPart{{Type: "text", Text: stripAttachedFilesBlock(prompt)}}, parts...)
	return parts
}

// stripAttachedFilesBlock removes the "[Attached files]" block from the prompt
// so the model sees clean text alongside the actual image content parts.
func stripAttachedFilesBlock(prompt string) string {
	// Match the [Attached files] header and all subsequent "- /path/..." lines
	re := regexp.MustCompile(`(?s)\[Attached files\]\s*\n((?:\s*-\s+.+\n?)+)`)
	stripped := re.ReplaceAllString(prompt, "")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		return prompt // fallback if stripping removes everything
	}
	return stripped
}
