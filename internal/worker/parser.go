package worker

import (
	"encoding/json"
	"fmt"
)

// ExecuteResult contains the result of a Claude Code execution.
type ExecuteResult struct {
	SessionID     string   // Session ID (for next --resume)
	Result        string   // Response text
	FilesModified []string // List of modified files
	CostUSD       float64  // Cost (USD)
}

// claudeOutput represents the JSON output from Claude Code CLI.
// Based on claude --output-format json output structure.
type claudeOutput struct {
	Type        string  `json:"type"`        // "result"
	SessionID   string  `json:"session_id"`  // Session ID
	Result      string  `json:"result"`      // Response text
	CostUSD     float64 `json:"cost_usd"`    // Cost
	TotalCost   float64 `json:"total_cost"`  // Total cost (alternative)
	IsError     bool    `json:"is_error"`    // Whether error occurred
	NumTurns    int     `json:"num_turns"`   // Number of turns
	DurationMs  float64 `json:"duration_ms"` // Execution time
	DurationAPI float64 `json:"duration_api_ms"`
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

	return &ExecuteResult{
		SessionID:     co.SessionID,
		Result:        co.Result,
		FilesModified: []string{}, // Add parsing from Claude output if needed
		CostUSD:       cost,
	}, nil
}
