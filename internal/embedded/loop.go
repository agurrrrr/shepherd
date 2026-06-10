package embedded

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

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

	// Vision is enabled when the task prompt carries attached files (the web UI
	// prepends an "[Attached files]" block). In that case read_file surfaces
	// image files as viewable images instead of a "cannot read binary" notice.
	toolRegistry.SetVision(strings.Contains(opts.UserPrompt, "[Attached files]"))

	// Pre-register any image paths mentioned in the initial prompt as already
	// read. This prevents the model from calling read_file on them — they are
	// already provided in the context. Without this, the model may enter an
	// infinite loop of calling read_file → image loaded → call read_file again.
	if strings.Contains(opts.UserPrompt, "[Attached files]") {
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
	)

	for iteration := 0; iteration < opts.MaxIterations; iteration++ {
		// Poll for injected user prompts (non-blocking). Each injected prompt is
		// appended as a {role: user} message so the model sees it as a natural
		// continuation of the conversation. This is checked at the top of each
		// loop iteration so injected messages are included in the next LLM call.
		if opts.InjectCh != nil {
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
					goto doneInjectPoll
				}
			}
		}
	doneInjectPoll:

		// Trim messages to fit context window
		messages = trimMessages(messages, opts.ContextTokens)

		// Build request
		req := &ChatRequest{
			Model:         opts.Model,
			Messages:      messages,
			Tools:         toolDefs,
			ToolChoice:    "auto",
			Temperature:   0.7,
			MaxTokens:     opts.ContextTokens / 4,
			Stream:        true,
			StreamOptions: &StreamOptions{IncludeUsage: true},
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

		// Handle tool calls (native function-calling)
		if len(msg.ToolCalls) > 0 {
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
			for _, tc := range msg.ToolCalls {
				// Show the tool call (name + command/args) BEFORE running it, in the
				// "🔧 name → detail" format the web UI parses (OutputViewer.svelte).
				if opts.OnOutput != nil {
					parsedArgs, _ := normalizeJSON(tc.Func.Args)
					opts.OnOutput(toolCallHeader(tc.Func.Name, parsedArgs))
				}

				result, err := dispatchTool(ctx, toolRegistry, tc, opts)
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
				// Add assistant message
				messages = append(messages, *msg)

				for _, tc := range leaked {
					args, parseErr := normalizeJSON(tc.Func.Args)
					if parseErr != nil {
						// Feed error back to model for self-repair
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

					result, err := toolRegistry.Dispatch(tc.Func.Name, args)
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
			// If the model has reasoning_content but no visible content, it's
			// still thinking — don't count this as an empty response. The
			// reasoning was already surfaced above via OnOutput.
			if reasoningPresent {
				consecutiveEmpty = 0
			} else {
				consecutiveEmpty++
			}

			// If finish_reason is "stop" with empty content, the model decided
			// it has nothing more to say. This is a normal completion, not a
			// sign of being stuck. Only count toward empty loop if the model
			// didn't explicitly stop.
			if finishReason == "stop" {
				consecutiveEmpty = 0
			}

			if consecutiveEmpty >= 3 {
				return &ExecuteResult{
					Result:           "",
					Incomplete:       true,
					IncompleteReason: "empty response loop detected",
					PromptTokens:     totalPromptTokens,
					CompletionTokens: totalCompletionTokens,
				}, nil
			}
			// Add a nudge to prompt the model to continue.
			messages = append(messages, ChatMessage{
				Role:    ChatRoleUser,
				Content: "Please continue with the task.",
			})
			continue
		}
		consecutiveEmpty = 0

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

	// Inject sheep_name for MCP tools
	args["sheep_name"] = opts.SheepName

	return tr.Dispatch(tc.Func.Name, args)
}

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

// truncate cuts a string to maxLen and adds "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateToolResult limits tool output stored in message history to 8 000
// characters. Very large outputs (e.g. full file contents printed by bash)
// blow up the context window quickly; truncating here keeps the conversation
// manageable while still giving the model the most important prefix.
func truncateToolResult(s string) string {
	const maxToolResultChars = 8000
	if len(s) <= maxToolResultChars {
		return s
	}
	return s[:maxToolResultChars] + fmt.Sprintf("\n...[truncated %d chars]", len(s)-maxToolResultChars)
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
	// Match image file paths in the prompt (same regex as MarkPreReadImages)
	imageRe := regexp.MustCompile(`(/[^\s"\']+?\.(jpg|jpeg|png|gif|webp|bmp|JPG|JPEG|PNG|GIF|WEBP|BMP))`)
	matches := imageRe.FindAllStringSubmatch(prompt, -1)

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
