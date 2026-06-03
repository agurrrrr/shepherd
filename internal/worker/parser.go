package worker

import (
	"encoding/json"
	"fmt"
)

// ExecuteResult contains the result of a Claude Code execution.
type ExecuteResult struct {
	SessionID        string   // Session ID (for next --resume)
	Result           string   // Response text
	FilesModified    []string // List of modified files
	CostUSD          float64  // Cost (USD)
	PromptTokens     int64    // Input token count
	CompletionTokens int64    // Output token count

	// Incomplete marks an OpenCode run that exited 0 but actually stalled
	// mid-task (e.g. a local reasoning model leaked its next tool call into the
	// reasoning/answer channel as plain text, or the turn was length-truncated).
	// The caller turns this into an error so the task is recorded as failed
	// instead of silently completed. See #5468.
	Incomplete       bool
	IncompleteReason string
}

// claudeOutput represents the JSON output from Claude Code CLI.
// Based on claude --output-format json output structure.
type claudeOutput struct {
	Type             string  `json:"type"`        // "result"
	SessionID        string  `json:"session_id"`  // Session ID
	Result           string  `json:"result"`      // Response text
	CostUSD          float64 `json:"cost_usd"`    // Cost
	TotalCost        float64 `json:"total_cost"`  // Total cost (alternative)
	IsError          bool    `json:"is_error"`    // Whether error occurred
	NumTurns         int     `json:"num_turns"`   // Number of turns
	DurationMs       float64 `json:"duration_ms"` // Execution time
	DurationAPI      float64 `json:"duration_api_ms"`
	InputTokens      int64   `json:"input_tokens"`  // Input tokens
	OutputTokens     int64   `json:"output_tokens"` // Output tokens
	CacheReadTokens  int64   `json:"cache_read_input_tokens"`
	CacheWriteTokens int64   `json:"cache_creation_input_tokens"`
}

// ParseClaudeOutput parses the JSON output from Claude Code CLI.
func ParseClaudeOutput(output []byte) (*ExecuteResult, error) {
	var co claudeOutput
	if err := json.Unmarshal(output, &co); err != nil {
		return nil, fmt.Errorf("JSON parsing failed: %w", err)
	}

	if co.IsError {
		return nil, fmt.Errorf("Claude Code execution error: %s", co.Result)
	}

	cost := co.CostUSD
	if cost == 0 {
		cost = co.TotalCost
	}

	promptTokens := co.InputTokens + co.CacheReadTokens + co.CacheWriteTokens
	completionTokens := co.OutputTokens

	return &ExecuteResult{
		SessionID:        co.SessionID,
		Result:           co.Result,
		FilesModified:    []string{},
		CostUSD:          cost,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}, nil
}
