package worker

import (
	"strings"
	"testing"
)

// Claude's terminal "result" event is the CLI's own verdict on the turn. A run
// can exit 0 (or 1) with is_error:true and an api_error_status, and it puts the
// whole failure sentence in "result". Reading that sentence as the assistant's
// answer recorded a rejected --model id as a completed task (task #12073 ran
// "claude-opus-5.5", the CLI refused it, and shepherd stored the refusal as the
// summary). parseStreamOutput must carry the failure into Incomplete instead.
func TestParseStreamOutputSurfacesResultEventError(t *testing.T) {
	cases := []struct {
		name           string
		line           string
		wantIncomplete bool
		wantReasonPart string
		wantStatus     int
	}{
		{
			name:           "rejected model id",
			line:           `{"type":"result","subtype":"success","result":"There's an issue with the selected model (claude-opus-5.5). It may not exist or you may not have access to it.","is_error":true,"terminal_reason":"api_error","api_error_status":404,"api_error_type":"not_found_error"}`,
			wantIncomplete: true, wantReasonPart: "api error 404", wantStatus: 404,
		},
		{
			name:           "quota stop carries its status",
			line:           `{"type":"result","subtype":"success","result":"You've hit your 5-hour limit · resets 1:40am","is_error":true,"terminal_reason":"api_error","api_error_status":429,"api_error_type":"rate_limit_error"}`,
			wantIncomplete: true, wantReasonPart: "api error 429", wantStatus: 429,
		},
		{
			name: "normal turn is not incomplete",
			line: `{"type":"result","subtype":"success","result":"done","is_error":false,"terminal_reason":"completed"}`,
		},
		{
			name:           "error subtype without is_error",
			line:           `{"type":"result","subtype":"error_during_execution","result":"","is_error":true}`,
			wantIncomplete: true, wantReasonPart: "claude reported an error result",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := parseStreamOutput(c.line)
			if res == nil {
				t.Fatalf("parseStreamOutput returned nil")
			}
			if res.Incomplete != c.wantIncomplete {
				t.Errorf("Incomplete = %v, want %v (reason=%q)", res.Incomplete, c.wantIncomplete, res.IncompleteReason)
			}
			if c.wantReasonPart != "" && !strings.Contains(res.IncompleteReason, c.wantReasonPart) {
				t.Errorf("IncompleteReason = %q, want it to contain %q", res.IncompleteReason, c.wantReasonPart)
			}
			if res.APIErrorStatus != c.wantStatus {
				t.Errorf("APIErrorStatus = %d, want %d", res.APIErrorStatus, c.wantStatus)
			}
		})
	}
}

// The 429 reason must keep the wording queue.IsRateLimitError matches on, or a
// quota stop loses its provider handoff (#675). A rejected model id must not
// match it, so it fails as a configuration error instead of being retried.
func TestClassifyClaudeFailureRoutesRateLimitAndModelErrors(t *testing.T) {
	quota := &ExecuteResult{
		Incomplete:       true,
		IncompleteReason: "api error 429: rate_limit_error: You've hit your 5-hour limit",
		APIErrorStatus:   429,
	}
	got := classifyClaudeFailure(quota, "")
	if !strings.HasPrefix(got, "rate limit: ") {
		t.Errorf("classifyClaudeFailure(429) = %q, want the \"rate limit: \" prefix", got)
	}

	model := &ExecuteResult{
		Incomplete:       true,
		IncompleteReason: "api error 404: not_found_error: There's an issue with the selected model (claude-opus-5.5)",
		APIErrorStatus:   404,
	}
	got = classifyClaudeFailure(model, "")
	if strings.Contains(got, "rate limit") {
		t.Errorf("classifyClaudeFailure(404) = %q, must not look like a rate limit", got)
	}
	if !strings.Contains(got, "claude reported an error") {
		t.Errorf("classifyClaudeFailure(404) = %q, want the error prefix", got)
	}
}

// A rejected model id is the one failure the user can only fix from settings, so
// the reason has to name the setting. Without it the task error is a bare CLI
// sentence and the reader has no idea which of the model settings is wrong.
func TestDescribeResultEventFailureNamesTheSettingForModelErrors(t *testing.T) {
	reason := describeResultEventFailure(
		"There's an issue with the selected model (claude-opus-5.5).",
		"api_error", "not_found_error", 404)

	if !strings.Contains(reason, "model_claude") {
		t.Errorf("reason = %q, want it to name the model setting", reason)
	}
	if strings.Contains(strings.ToLower(reason), "rate limit") {
		t.Errorf("reason = %q, must not be mistaken for a rate limit", reason)
	}
}
