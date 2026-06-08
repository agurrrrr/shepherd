package embedded

import (
	"context"
	"encoding/json"
	"fmt"
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
		{Role: ChatRoleUser, Content: opts.UserPrompt},
	}

	// Ensure base_url has /v1 suffix
	baseURL := opts.BaseURL
	if !strings.HasSuffix(strings.ToLower(baseURL), "/v1") {
		baseURL = strings.TrimRight(baseURL, "/") + "/v1"
	}

	client := NewClient(baseURL, opts.APIKey, opts.Model)
	toolRegistry := NewToolRegistry(opts.ProjectPath, opts.SheepName, opts.MCPDefs, opts.MCPDispatch)

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
		loopGuard             = make(map[string]int) // detect repeated tool+args
	)

	for iteration := 0; iteration < opts.MaxIterations; iteration++ {
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
			// Check for repeated tool+args (loop detection)
			for _, tc := range msg.ToolCalls {
				key := tc.Func.Name + "::" + tc.Func.Args
				loopGuard[key]++
				if loopGuard[key] > 5 {
					return &ExecuteResult{
						Result:           msg.Content,
						Incomplete:       true,
						IncompleteReason: fmt.Sprintf("repeated tool call detected: %s (5+ times)", tc.Func.Name),
						PromptTokens:     totalPromptTokens,
						CompletionTokens: totalCompletionTokens,
					}, nil
				}
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
				continue
			}
		}

		// Pure text response — check for empty consecutive responses.
		// Do NOT add the empty message to history: it wastes context and does not
		// help the model recover. Instead, inject a nudge so the model knows it
		// should produce output.
		if strings.TrimSpace(msg.Content) == "" {
			consecutiveEmpty++
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

		// Check for length truncation
		if finishReason == "length" && strings.TrimSpace(msg.Content) == "" {
			return &ExecuteResult{
				Result:           "",
				Incomplete:       true,
				IncompleteReason: "response truncated (max tokens reached)",
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
