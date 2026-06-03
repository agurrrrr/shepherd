package worker

import (
	"strings"
	"testing"
)

func TestLooksLikeLeakedToolCall(t *testing.T) {
	leaked := []string{
		"<tool_call>\n<function=bash>\n<parameter=command>kubectl get pods</parameter>\n</function>\n</tool_call>",
		"<function=bash><parameter=command>ls</parameter></function>",
		"thinking... <|tool_call|> bash",
		"[TOOL_CALL] bash(command='ls')",
	}
	for _, s := range leaked {
		if !looksLikeLeakedToolCall(s) {
			t.Errorf("expected leaked tool call to be detected: %q", s)
		}
	}

	clean := []string{
		"작업을 완료했습니다.",
		"Here is the result of the analysis.",
		"</think> The deployment is healthy.",
		"",
	}
	for _, s := range clean {
		if looksLikeLeakedToolCall(s) {
			t.Errorf("did not expect clean text to be flagged: %q", s)
		}
	}
}

// TestParseOpenCodeOutput_Incomplete reproduces #5468: a local model leaks its
// next tool call into the reasoning channel as the trailing event, so OpenCode
// exits 0 but the agent actually stalled.
func TestParseOpenCodeOutput_Incomplete(t *testing.T) {
	tests := []struct {
		name           string
		lines          []string
		wantIncomplete bool
		wantResult     string
	}{
		{
			name: "tool call leaked into reasoning channel at end of turn",
			lines: []string{
				`{"type":"text","sessionID":"s1","part":{"type":"text","text":"Docker 이미지를 빌드하고 푸시해야 합니다."}}`,
				`{"type":"reasoning","sessionID":"s1","part":{"type":"reasoning","text":"<tool_call>\n<function=bash>\n<parameter=command>kubectl get deployment</parameter>\n</function>\n</tool_call>"}}`,
			},
			wantIncomplete: true,
		},
		{
			name: "leak recovered by a real tool_use afterwards",
			lines: []string{
				`{"type":"reasoning","sessionID":"s1","part":{"type":"reasoning","text":"<tool_call><function=bash></function></tool_call>"}}`,
				`{"type":"tool_use","sessionID":"s1","part":{"type":"tool","tool":"bash","state":{"status":"completed"}}}`,
				`{"type":"text","sessionID":"s1","part":{"type":"text","text":"완료했습니다."}}`,
			},
			wantIncomplete: false,
			wantResult:     "완료했습니다.",
		},
		{
			name: "clean final answer after a leak clears the flag",
			lines: []string{
				`{"type":"reasoning","sessionID":"s1","part":{"type":"reasoning","text":"<tool_call> bash"}}`,
				`{"type":"text","sessionID":"s1","part":{"type":"text","text":"분석을 마쳤습니다."}}`,
			},
			wantIncomplete: false,
			wantResult:     "분석을 마쳤습니다.",
		},
		{
			name: "length truncation with no final response",
			lines: []string{
				`{"type":"reasoning","sessionID":"s1","part":{"type":"reasoning","text":"계속 생각 중"}}`,
				`{"type":"step_finish","sessionID":"s1","finishReason":"length"}`,
			},
			wantIncomplete: true,
		},
		{
			name: "normal completion",
			lines: []string{
				`{"type":"tool_use","sessionID":"s1","part":{"type":"tool","tool":"bash","state":{"status":"completed"}}}`,
				`{"type":"text","sessionID":"s1","part":{"type":"text","text":"작업 완료"}}`,
				`{"type":"step_finish","sessionID":"s1","finishReason":"stop"}`,
			},
			wantIncomplete: false,
			wantResult:     "작업 완료",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := strings.Join(tt.lines, "\n")
			result := parseOpenCodeOutput(out)
			if result.Incomplete != tt.wantIncomplete {
				t.Errorf("Incomplete = %v, want %v (reason: %q)", result.Incomplete, tt.wantIncomplete, result.IncompleteReason)
			}
			if tt.wantResult != "" && result.Result != tt.wantResult {
				t.Errorf("Result = %q, want %q", result.Result, tt.wantResult)
			}
			if tt.wantIncomplete && result.IncompleteReason == "" {
				t.Error("expected a non-empty IncompleteReason when Incomplete is true")
			}
		})
	}
}
