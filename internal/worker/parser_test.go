package worker

import (
	"testing"
)

func TestParseClaudeOutput(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     bool
		wantSession string
		wantResult  string
	}{
		{
			name: "valid output",
			input: `{
				"type": "result",
				"session_id": "abc123",
				"result": "작업 완료",
				"cost_usd": 0.01,
				"is_error": false
			}`,
			wantErr:     false,
			wantSession: "abc123",
			wantResult:  "작업 완료",
		},
		{
			name: "error output",
			input: `{
				"type": "result",
				"session_id": "abc123",
				"result": "에러 발생",
				"is_error": true
			}`,
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   ``,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseClaudeOutput([]byte(tt.input))

			if tt.wantErr {
				if err == nil {
					t.Error("ParseClaudeOutput() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("ParseClaudeOutput() unexpected error: %v", err)
				return
			}

			if result.SessionID != tt.wantSession {
				t.Errorf("SessionID = %q, want %q", result.SessionID, tt.wantSession)
			}

			if result.Result != tt.wantResult {
				t.Errorf("Result = %q, want %q", result.Result, tt.wantResult)
			}
		})
	}
}

func TestExecuteResult(t *testing.T) {
	result := &ExecuteResult{
		SessionID:     "test-session",
		Result:        "test result",
		FilesModified: []string{"file1.go", "file2.go"},
		CostUSD:       0.05,
	}

	if result.SessionID != "test-session" {
		t.Errorf("SessionID = %q, want %q", result.SessionID, "test-session")
	}

	if len(result.FilesModified) != 2 {
		t.Errorf("FilesModified length = %d, want 2", len(result.FilesModified))
	}
}
