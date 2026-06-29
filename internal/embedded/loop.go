package embedded

import (
	"context"
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

// maxFutureIntentionNudges bounds how many times the future-intention guard
// (task #6290, mitigation ①) will nudge a model that keeps *declaring* work
// ("이제 ~하겠습니다") without actually doing it. Once exceeded, the task is
// marked incomplete instead of being nudged forever — this stops the
// token-burning loop a confused model can fall into (task #6294). State-changing
// tool calls (bash/write_file/edit_file) reset the counter, so a model that
// declares then actually acts never exhausts the budget.
const maxFutureIntentionNudges = 2

// maxPauseSummaryNudges bounds how many times the pause-summary guard (task
// #6690) nudges a model that voluntarily writes a "paused, continue later"
// handoff-style summary ("진행 상황 요약 (중단 시점)", "다음 라운드에서 계속")
// instead of finishing. Once exceeded, the remaining work is routed through the
// real context handoff (queued as a follow-up) so it isn't silently abandoned;
// if no handoff is available the task is reported incomplete rather than being
// falsely marked complete.
const maxPauseSummaryNudges = 2

// replacementCharRatio / minDegenerateRunes gate the degeneration guard. A
// short reply with a stray U+FFFD must not trip it, so require both a minimum
// length and a high replacement-char fraction.
const replacementCharRatio = 0.2
const minDegenerateRunes = 20

// toolCallsSignature builds a stable, order-independent signature for a turn's
// tool calls (name + trimmed args). Used to detect a model stuck repeating the
// exact same call(s) turn after turn. Returns "" for no calls.
func toolCallsSignature(calls []ToolCall) string {
	return toolCallsSignatureWithRegistry(calls, nil)
}

// toolCallsSignatureWithRegistry is toolCallsSignature with awareness of
// read_file paging progress. Consecutive read_file calls that omit the offset
// auto-advance through the file (see ToolRegistry.readfile), so their args stay
// byte-identical even though each returns a different page. Folding the last
// read position into the signature keeps that legitimate progress from tripping
// the stuck guard, while a model that keeps re-reading the SAME exhausted file
// still produces a stable signature and is caught (task #6505).
func toolCallsSignatureWithRegistry(calls []ToolCall, tr *ToolRegistry) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, len(calls))
	for _, tc := range calls {
		sig := tc.Func.Name + "(" + strings.TrimSpace(tc.Func.Args) + ")"
		if tr != nil && tc.Func.Name == "read_file" && tr.lastReadEndLine > 0 {
			if p, ok := readFilePath(tc); ok {
				if resolved, err := tr.safePath(p); err == nil && resolved == tr.lastReadPath {
					sig += fmt.Sprintf("@%d", tr.lastReadEndLine)
				}
			}
		}
		parts = append(parts, sig)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// readFilePath extracts the "path" argument from a read_file tool call,
// reporting false when the args are unparseable or carry no usable path.
func readFilePath(tc ToolCall) (string, bool) {
	args, err := normalizeJSON(tc.Func.Args)
	if err != nil {
		return "", false
	}
	p, ok := args["path"].(string)
	return p, ok && p != ""
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
// declaration like "다시 빌드해보겠습니다", "let me now build", "I'll now run" —
// patterns where the model announces what it *will* do but doesn't actually call
// a tool. This is a key symptom of false-completion (task #6290, mitigation ①).
//
// futureIntentionKorean is ANCHORED to the end of the (punctuation-stripped)
// text and gated on an action-verb stem followed by a volitional ending
// (~겠습니다 / ~할게요 / ~하려고 합니다 / ~할 예정). It deliberately does NOT
// require a leading adverb (이제/지금부터) — that requirement is exactly why the
// original regex missed "다시 빌드해보겠습니다" (task #6294). Past-tense endings
// (했습니다/완료했습니다) are not matched: those report finished work, so they are
// genuine completions and handled by the build-verification gate instead.
var futureIntentionKorean = regexp.MustCompile(
	`(하|해|보|봐|드리|만들|적용|진행|시작|실행|수정|확인|작성|추가|빌드|컴파일|테스트|점검|살펴|이어|계속|완성|정리|구현|생성|변경|시도)(아야|어야|야)?(겠습니다|겠어요|겠네요|겠음)$` +
		`|(할게요|할께요|해볼게요|볼게요|해드릴게요|을게요|를게요)$` +
		`|(려고\s*합니다|려\s*합니다|할\s*예정입니다|할\s*것입니다|할\s*계획입니다)$`)

// futureIntentionEnglish is a contains-match (declarations often precede a
// trailing sentence). A first-person subject is required so "you can run …"
// style suggestions don't trip it.
var futureIntentionEnglish = regexp.MustCompile(`(?i)\b(let me (now |then |go ahead and |try to )?(finish|complete|implement|build|rebuild|run|re-?run|execute|check|verify|test|fix|continue|add|create|update|write)|i('ll| will|'m going to| am going to)( now| then| also| next)? (finish|complete|implement|build|rebuild|run|re-?run|execute|check|verify|test|fix|continue|add|create|update|write)|now i('ll| will)|next,? i('ll| will))\b`)

// futureIntentionTrailing is the set of trailing characters stripped before the
// end-anchored Korean check, so a trailing ". ! ~ … 。" or quote doesn't defeat $.
const futureIntentionTrailing = " \t\r\n.!?~…。\"'’”)】」』"

func isFutureIntention(content string) bool {
	s := strings.TrimSpace(content)
	if s == "" {
		return false
	}
	if futureIntentionEnglish.MatchString(s) {
		return true
	}
	// Korean: only the final clause matters — strip trailing punctuation/quotes
	// and look for a volitional ending at the very end of the message.
	if futureIntentionKorean.MatchString(strings.TrimRight(s, futureIntentionTrailing)) {
		return true
	}
	return false
}

// pauseSummaryPattern matches the high-signal cues of a "paused mid-work,
// continue later" handoff-style summary. Deliberately narrow: it only matches
// phrasing that explicitly frames the message as an interruption point, never
// the generic "remaining/optional follow-up" wording that legitimately closes a
// completed task ("남은 개선 사항으로는…", "further work could…"). Case-insensitive
// (harmless for Korean, which is caseless).
var pauseSummaryPattern = regexp.MustCompile(
	`(?i)중단\s*시점|중단된\s*시점|작업이\s*중단|여기서\s*중단|다음\s*라운드|다음\s*세션|다음\s*작업에서\s*계속|다음에\s*이어|다음에\s*계속|이어서\s*진행하겠|to be continued|next (round|session)|pick up where|continue (in|with|on) the next`)

// isPauseSummary reports whether the model's final text answer reads as a
// "paused, will continue later" handoff summary rather than a genuine
// completion. A long continuation task — primed by the previous tasks' handoff
// summaries sitting in its context — sometimes voluntarily writes a progress
// summary and stops calling tools. The loop would otherwise treat that
// no-tool-call turn as a successful completion and mark the task done with the
// work abandoned and nothing queued to resume it (task #6690). This guard lets
// the loop catch that case and either nudge the model to keep going or route the
// remainder through the real context handoff.
func isPauseSummary(content string) bool {
	s := strings.TrimSpace(content)
	if s == "" {
		return false
	}
	return pauseSummaryPattern.MatchString(s)
}

// mentionsBuildWork reports whether text references build/compile/verification
// work. Used by the build-verification gate to catch continuation tasks whose
// *user prompt* carries no build keyword but whose model output claims
// build-related work (e.g. "빌드 에러를 수정했습니다") without ever running bash
// (task #6294). Kept deliberately narrow to avoid false positives.
func mentionsBuildWork(text string) bool {
	lower := strings.ToLower(text)
	keywords := []string{
		"빌드", "컴파일", "build", "compile",
		"gradlew", "gradle", "compiledebug",
	}
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return true
		}
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
		// False-completion guard state (task #6290): bashCalled tracks whether bash
		// ran (build-verification gate); codeModified tracks whether write_file/
		// edit_file ran, so the gate can flag "edited code but never verified";
		// futureIntentionNudges counts consecutive future-intention stalls so the
		// nudge loop is bounded (task #6294).
		bashCalled            bool
		codeModified          bool
		futureIntentionNudges int
		// pauseSummaryNudges counts consecutive "paused, continue later" handoff
		// summaries so that guard's nudge loop is bounded (task #6690).
		pauseSummaryNudges int
	)

	// markToolUsed records state-changing tool activity for the false-completion
	// guards. Only bash/write_file/edit_file count: they represent real progress,
	// so they clear the future-intention stall counter. read_file (mere inspection)
	// intentionally does NOT reset it, so a "read then re-declare" ping-pong still
	// hits the nudge cap and is reported incomplete.
	markToolUsed := func(name string) {
		switch name {
		case "bash":
			bashCalled = true
			futureIntentionNudges = 0
		case "write_file", "edit_file":
			codeModified = true
			futureIntentionNudges = 0
		}
	}

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
			sig := toolCallsSignatureWithRegistry(msg.ToolCalls, toolRegistry)
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
				markToolUsed(tc.Func.Name)
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
					Content:    truncateToolResult(resultStr, tc.Func.Name),
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
					markToolUsed(tc.ToolCall.Func.Name)
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
						Content:    truncateToolResult(resultStr, tc.Func.Name),
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

		// ── Mitigation ①: Future-intention nudge (task #6290 / #6294) ──
		// If there are no tool calls AND the content ends with a future-action
		// declaration ("다시 빌드해보겠습니다", "let me now ~"), do NOT treat it as a
		// completion. Nudge the model to actually do it — but only up to
		// maxFutureIntentionNudges times, after which the task is reported
		// incomplete rather than nudged forever (prevents the #6294 token loop).
		if isFutureIntention(msg.Content) {
			futureIntentionNudges++
			if futureIntentionNudges > maxFutureIntentionNudges {
				if opts.OnOutput != nil {
					opts.OnOutput("⚠️ [미래형 선언 반복]: 선언만 반복하고 실제 실행이 없어 작업을 미완료로 종료합니다.")
				}
				return &ExecuteResult{
					Result:           msg.Content,
					Incomplete:       true,
					IncompleteReason: "model repeatedly declared future actions without executing them",
					PromptTokens:     totalPromptTokens,
					CompletionTokens: totalCompletionTokens,
				}, nil
			}
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

		// ── Mitigation ②: Build-verification gate (task #6290 / #6294) ──
		// Mark incomplete if a build was expected but bash never ran. "Expected"
		// means EITHER the user prompt names a build command, OR the model itself
		// edited code AND its final message claims build-related work — the latter
		// catches continuation tasks (e.g. "6284 이어서 작업해줘") whose prompt has no
		// build keyword but whose output claims "빌드 에러를 수정했습니다" without ever
		// verifying it (task #6294).
		buildRequired := hasBuildCommandInPrompt(opts.UserPrompt)
		buildClaimed := codeModified && mentionsBuildWork(msg.Content)
		if (buildRequired || buildClaimed) && !bashCalled {
			if opts.OnOutput != nil {
				opts.OnOutput("⚠️ [빌드 검증 게이트]: 빌드가 필요한 작업인데 bash 빌드 검증이 한 번도 실행되지 않았습니다.")
			}
			return &ExecuteResult{
				Result:           msg.Content,
				Incomplete:       true,
				IncompleteReason: "required build verification was never run (bash was not called)",
				PromptTokens:     totalPromptTokens,
				CompletionTokens: totalCompletionTokens,
			}, nil
		}

		// ── Mitigation ③: Pause-summary / self-handoff guard (task #6690) ──
		// The model returned no tool calls and its final answer reads as a
		// "paused, will continue later" handoff summary, not a real completion.
		// Treat it as unfinished. First nudge it to keep going in THIS task
		// (cheap, bounded like the future-intention guard); if it persists, route
		// the remainder through the real context handoff so the work is queued as
		// a follow-up — and only if no handoff is available do we report
		// incomplete, never silently mark it complete.
		if isPauseSummary(msg.Content) {
			pauseSummaryNudges++
			if pauseSummaryNudges <= maxPauseSummaryNudges {
				if opts.OnOutput != nil {
					opts.OnOutput("⚠️ [중단 요약 감지]: 작업이 끝나지 않았습니다. 중단 요약 대신 실제 도구 호출로 계속 진행해주세요.")
				}
				messages = append(messages, ChatMessage{Role: ChatRoleAssistant, Content: msg.Content})
				messages = append(messages, ChatMessage{
					Role: ChatRoleUser,
					Content: "아직 작업이 끝나지 않았습니다. '중단 시점' 요약을 작성하지 말고, " +
						"남은 작업을 실제 도구 호출(bash, write_file, mobile_* 등)로 계속 진행해주세요. " +
						"정말로 모든 작업이 끝났다면 무엇을 완료했는지만 명확히 보고해주세요.",
				})
				continue
			}
			// Persisted: hand the remaining work off as a follow-up task rather
			// than abandoning it with a false completion.
			if opts.EnqueueFollowUp != nil && (opts.ShouldHandoff == nil || opts.ShouldHandoff()) {
				if res, ok := attemptHandoff(ctx, client, opts, messages, totalPromptTokens, totalCompletionTokens); ok {
					return res, nil
				}
			}
			if opts.OnOutput != nil {
				opts.OnOutput("⚠️ [중단 요약 반복]: 작업을 끝내지 않고 중단 요약만 반복하여 미완료로 종료합니다.")
			}
			return &ExecuteResult{
				Result:           msg.Content,
				Incomplete:       true,
				IncompleteReason: "model produced a pause/handoff summary instead of finishing the task",
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

// appendPendingImages drains any images buffered during the turn (by read_file
// reading an image file, or by an MCP tool such as mobile_take_screenshot
// returning an image block) and appends them as a single user message with
// image_url content parts, so a vision-capable model can view them. Returns
// messages unchanged when there are no pending images.
func appendPendingImages(messages []ChatMessage, tr *ToolRegistry) []ChatMessage {
	imgs := tr.DrainPendingImages()
	if len(imgs) == 0 {
		return messages
	}
	parts := make([]ContentPart, 0, len(imgs)+1)
	parts = append(parts, ContentPart{Type: "text", Text: "Attached image(s) from the tool call(s) above:"})
	for _, img := range imgs {
		// Log the optimized image size for debugging context budget issues.
		fmt.Printf("🖼️  image injected: %s (%s, ~%d tokens)\n",
			img.name, FormatImageSize(img.dataURL), EstimateImageTokens(img.dataURL))
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

// maxToolResultChars bounds how many characters of any single tool result are
// stored in message history. Very large outputs (e.g. full file contents printed
// by bash) blow up the context window quickly; truncating here keeps the
// conversation manageable while still giving the model the most important prefix.
//
// read_file (tools.go) deliberately keeps its own output below this limit so the
// paging footer it appends — which lives at the END of the output — is never the
// casualty of this cut. If read_file's output exceeded this limit, the footer
// would be the first thing dropped, hiding the file's tail from the model and
// recreating the deadlock from task #6309.
const maxToolResultChars = 8000

// sanitizeToolResult removes binary control characters (U+0000–U+0008,
// U+000B, U+000C, U+000E–U+001F, U+007F) from tool output before it enters the
// chat history. These characters — common in raw binary file contents, ADB
// dumps, and crash logs returned by bash or MCP tools — cause local models
// (notably Qwen3 via llama.cpp) to emit an immediate EOS on the very next
// turn, producing an empty response with completion_tokens=1. The model sees a
// corrupted token sequence after the control bytes and decides generation is
// finished. Stripping them lets the model process tool results normally.
// Tab (\t), newline (\n), and carriage return (\r) are preserved.
func sanitizeToolResult(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return -1 // drop
		}
		if r == 0x7F { // DEL
			return -1
		}
		return r
	}, s)
}

// truncateToolResult limits tool output stored in message history to
// maxToolResultChars. It is the universal backstop every tool result passes
// through. When it has to cut, it appends an ACTIONABLE recovery hint tailored
// to the tool so the model can fetch the hidden remainder instead of dead-ending.
//
// The dead-end was the #6309 failure mode: a tool result silently chopped to its
// first maxToolResultChars characters, with only a "...[truncated N chars]" notice
// that named no way forward. The model re-issued the identical call, saw the
// identical prefix, and the repeated-call guard (maxRepeatedToolTurns) eventually
// killed the task. The read_file fix paged that one tool; this normalizes the same
// escape hatch for EVERY tool whose output is not self-paging (bash, grep, glob,
// MCP tools). read_file (tools.go) keeps its own output below this limit so its
// paging footer — which lives at the END of the output — survives, meaning
// read_file results pass through here unchanged.
//
// Binary control characters are stripped first (sanitizeToolResult) to prevent
// them from poisoning the model's context and causing empty-response loops.
func truncateToolResult(s, toolName string) string {
	s = sanitizeToolResult(s)
	runes := []rune(s)
	if len(runes) <= maxToolResultChars {
		return s
	}
	hidden := len(runes) - maxToolResultChars
	return string(runes[:maxToolResultChars]) + fmt.Sprintf(
		"\n...[truncated %d of %d chars. %s]", hidden, len(runes), truncationHint(toolName))
}

// truncationHint returns tool-specific guidance for retrieving output that
// truncateToolResult had to drop. Each hint names a concrete next action whose
// tool-call signature differs from the call that was just truncated — re-running
// the SAME call would only reproduce the same truncated prefix and re-arm the
// repeated-call stuck guard (task #6309).
func truncationHint(toolName string) string {
	switch toolName {
	case "bash":
		return "Only the first part of the output is shown. To see the rest, re-run the " +
			"command narrowing its output — pipe through head/tail or `sed -n 'START,ENDp'`, " +
			"or grep for what you need — or redirect it to a file (`cmd > /tmp/out.txt`) and " +
			"open that file with read_file, which pages large files."
	case "grep":
		return "Only the first matches are shown. Narrow the search to surface the relevant " +
			"ones — tighten the pattern or pass a glob filter."
	case "glob":
		return "Too many matches. Narrow the glob pattern to surface the relevant paths."
	case "read_file":
		// read_file self-pages and should not reach here; if it ever does, point at
		// its own paging mechanism rather than a generic message.
		return "Call read_file again with a higher offset to continue paging through the file."
	default:
		return "The output was truncated. Re-run the tool requesting a narrower slice to see the rest."
	}
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

		dataURL := optimizeImageForContext(data, mime)
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
