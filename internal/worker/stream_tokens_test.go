package worker

import "testing"

// The Claude Code stream-json "result" event carries token totals in a
// TOP-LEVEL "usage" object (sibling of total_cost_usd), not under "message".
// Regression guard for the bug where prompt/completion_tokens stayed 0 even
// though cost was captured.
func TestParseStreamOutput_ResultUsageTokens(t *testing.T) {
	// Mirrors the real shape: prompt = input + cache_read + cache_creation.
	line := `{"type":"result","subtype":"success","session_id":"s1","result":"done","total_cost_usd":0.05,` +
		`"usage":{"input_tokens":4260,"cache_creation_input_tokens":3152,"cache_read_input_tokens":17308,"output_tokens":5}}`

	res := parseStreamOutput(line)

	wantPrompt := int64(4260 + 3152 + 17308)
	if res.PromptTokens != wantPrompt {
		t.Errorf("PromptTokens = %d, want %d", res.PromptTokens, wantPrompt)
	}
	if res.CompletionTokens != 5 {
		t.Errorf("CompletionTokens = %d, want 5", res.CompletionTokens)
	}
	if res.CostUSD != 0.05 {
		t.Errorf("CostUSD = %v, want 0.05", res.CostUSD)
	}
	if res.Result != "done" {
		t.Errorf("Result = %q, want \"done\"", res.Result)
	}
}
