package embedded

import (
	"context"
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
	toolRegistry := NewToolRegistry(opts.ProjectPath, opts.SheepName, nil, nil) // MCP tools injected externally

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

		// Handle tool calls (native function-calling)
		if len(msg.ToolCalls) > 0 {
			// Check for repeated tool+args (loop detection)
			for _, tc := range msg.ToolCalls {
				key := tc.Func.Name + "::" + tc.Func.Args
				loopGuard[key]++
				if loopGuard[key] > 3 {
					return &ExecuteResult{
						Result:           msg.Content,
						Incomplete:       true,
						IncompleteReason: fmt.Sprintf("repeated tool call detected: %s (3+ times)", tc.Func.Name),
						PromptTokens:     totalPromptTokens,
						CompletionTokens: totalCompletionTokens,
					}, nil
				}
			}

			// Add assistant message with tool calls to history
			messages = append(messages, *msg)

			// Execute each tool call
			for _, tc := range msg.ToolCalls {
				result, err := dispatchTool(ctx, toolRegistry, tc, opts)
				var resultStr string
				if err != nil {
					resultStr = fmt.Sprintf("Error: %v", err)
				} else {
					resultStr = result
				}

				// Stream the tool result preview
				if opts.OnOutput != nil {
					preview := resultStr
					if len(preview) > 500 {
						preview = preview[:500] + "..."
					}
					opts.OnOutput(fmt.Sprintf("\n🔧 %s: %s\n", tc.Func.Name, preview))
				}

				messages = append(messages, ChatMessage{
					Role:       ChatRoleTool,
					Content:    resultStr,
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

					result, err := toolRegistry.Dispatch(tc.Func.Name, args)
					var resultStr string
					if err != nil {
						resultStr = fmt.Sprintf("Error: %v", err)
					} else {
						resultStr = result
					}

					if opts.OnOutput != nil {
						opts.OnOutput(fmt.Sprintf("\n🔧 [%s recovered] %s: %s\n", tc.Func.Name, tc.Func.Name, truncate(resultStr, 500)))
					}

					messages = append(messages, ChatMessage{
						Role:       ChatRoleTool,
						Content:    resultStr,
						ToolCallID: tc.ID,
					})
				}
				continue
			}
		}

		// Pure text response — check for empty consecutive responses
		if strings.TrimSpace(msg.Content) == "" {
			consecutiveEmpty++
			if consecutiveEmpty >= 3 {
				return &ExecuteResult{
					Result:           msg.Content,
					Incomplete:       true,
					IncompleteReason: "empty response loop detected",
					PromptTokens:     totalPromptTokens,
					CompletionTokens: totalCompletionTokens,
				}, nil
			}
			// Add empty message and retry
			messages = append(messages, *msg)
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

// truncate cuts a string to maxLen and adds "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
