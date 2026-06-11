package embedded

import (
	"testing"
)

// ─────────────────────────────────────────────
// normalizeJSON: basic valid input
// ─────────────────────────────────────────────

func TestNormalizeJSONValid(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal string
	}{
		{
			name:    "simple string argument",
			input:   `{"command":"ls -la"}`,
			wantKey: "command",
			wantVal: "ls -la",
		},
		{
			name:    "trailing comma removed",
			input:   `{"command":"ls -la",}`,
			wantKey: "command",
			wantVal: "ls -la",
		},
		{
			name:    "json wrapped in code fence",
			input:   "```json\n{\"command\":\"ls -la\"}\n```",
			wantKey: "command",
			wantVal: "ls -la",
		},
		{
			name:    "json wrapped in plain code fence",
			input:   "```\n{\"command\":\"ls -la\"}\n```",
			wantKey: "command",
			wantVal: "ls -la",
		},
		{
			name:    "multiple fields",
			input:   `{"project_name":"shepherd","limit":10}`,
			wantKey: "project_name",
			wantVal: "shepherd",
		},
		{
			name:    "single-quoted object",
			input:   `{'command': 'ls -la'}`,
			wantKey: "command",
			wantVal: "ls -la",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeJSON(tt.input)
			if err != nil {
				t.Fatalf("normalizeJSON(%q) unexpected error: %v", tt.input, err)
			}
			if got[tt.wantKey] != tt.wantVal {
				t.Errorf("got[%q] = %v, want %q", tt.wantKey, got[tt.wantKey], tt.wantVal)
			}
		})
	}
}

// ─────────────────────────────────────────────
// normalizeJSON: escaped quotes in strings (Qwen3 shell commands)
// ─────────────────────────────────────────────

func TestNormalizeJSONEscapedQuotes(t *testing.T) {
	// Qwen3 frequently generates shell commands with quoted arguments.
	// The tool call args arrive as: {"command":"curl -A \"Mozilla/5.0\" http://example.com"}
	tests := []struct {
		name    string
		input   string
		wantCmd string
	}{
		{
			name:    "curl with user-agent double-quoted",
			input:   `{"command":"curl -s -L -A \"Mozilla/5.0\" https://example.com"}`,
			wantCmd: `curl -s -L -A "Mozilla/5.0" https://example.com`,
		},
		{
			name:    "grep with quoted pattern",
			input:   `{"command":"grep -r \"func main\" ."}`,
			wantCmd: `grep -r "func main" .`,
		},
		{
			name:    "echo redirect with quotes",
			input:   `{"command":"echo \"hello world\" > /tmp/test.txt"}`,
			wantCmd: `echo "hello world" > /tmp/test.txt`,
		},
		{
			name:    "awk with quoted program",
			input:   `{"command":"awk \"{print $1}\" /etc/hosts"}`,
			wantCmd: `awk "{print $1}" /etc/hosts`,
		},
		{
			name:    "python -c with single-quoted script",
			input:   `{"command":"python3 -c \"import json; print(json.dumps({'key': 'value'}))\""}`,
			wantCmd: `python3 -c "import json; print(json.dumps({'key': 'value'}))"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeJSON(tt.input)
			if err != nil {
				t.Fatalf("normalizeJSON(%q) error: %v", tt.input, err)
			}
			if got["command"] != tt.wantCmd {
				t.Errorf("command = %v\n  want  = %q", got["command"], tt.wantCmd)
			}
		})
	}
}

// ─────────────────────────────────────────────
// normalizeJSON: truncated JSON (the #5826 bug)
// ─────────────────────────────────────────────

// TestNormalizeJSONTruncated verifies that common truncation patterns emitted by
// Qwen3 are repaired, not rejected with a hard parse error.
//
// Root cause of #5826: Qwen3-27B on llama.cpp generated
//
//	{"command":"cd ~/code && curl -s -L -A \"
//
// (the string was never closed and the object was never closed).
// normalizeJSON returned an error, which was fed back as a tool result.
// The NEXT API call included the assistant message with those malformed args
// and llama.cpp returned HTTP 500 ("Failed to parse tool call arguments").
func TestNormalizeJSONTruncated(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantKey string
		wantVal string
	}{
		{
			// THE bug from #5826: string ends with \" (escaped quote) — both the
			// string value and the object body are unclosed.
			name:    "truncated at escaped-quote (the #5826 bug)",
			input:   `{"command":"cd ~/code && curl -s -L -A \"`,
			wantErr: false,
			wantKey: "command",
			// After repair the value should be: cd ~/code && curl -s -L -A "
			wantVal: `cd ~/code && curl -s -L -A "`,
		},
		{
			name:    "missing closing brace only",
			input:   `{"command":"ls -la"`,
			wantErr: false,
			wantKey: "command",
			wantVal: "ls -la",
		},
		{
			name:    "truncated mid-second-key",
			input:   `{"command":"ls -la", "desc`,
			wantErr: false,
			wantKey: "command",
			wantVal: "ls -la",
		},
		{
			name:    "truncated mid-value",
			input:   `{"command":"cd ~/code && curl -s -L -A "`,
			wantErr: false,
			wantKey: "command",
			wantVal: "cd ~/code && curl -s -L -A ",
		},
		{
			name:    "complex truncation with nested quotes",
			input:   `{"command":"python3 -c \"import sys; sys.`,
			wantErr: false,
			wantKey: "command",
			// value ends at the truncation point, closing quote added
			wantVal: `python3 -c "import sys; sys.`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeJSON(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeJSON(%q): want error, got nil (result: %v)", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeJSON(%q) unexpected error: %v", tt.input, err)
			}
			if got[tt.wantKey] != tt.wantVal {
				t.Errorf("got[%q] = %v\n  want = %q", tt.wantKey, got[tt.wantKey], tt.wantVal)
			}
		})
	}
}

// ─────────────────────────────────────────────
// normalizeJSON: backslash edge cases
// ─────────────────────────────────────────────

func TestNormalizeJSONBackslash(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal string
	}{
		{
			name:    "windows path double-backslash",
			input:   `{"path":"C:\\Users\\foo\\bar"}`,
			wantKey: "path",
			wantVal: `C:\Users\foo\bar`,
		},
		{
			name:    "newline in string",
			input:   `{"text":"line1\nline2"}`,
			wantKey: "text",
			wantVal: "line1\nline2",
		},
		{
			name:    "tab in string",
			input:   `{"text":"col1\tcol2"}`,
			wantKey: "text",
			wantVal: "col1\tcol2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeJSON(tt.input)
			if err != nil {
				t.Fatalf("normalizeJSON(%q) error: %v", tt.input, err)
			}
			if got[tt.wantKey] != tt.wantVal {
				t.Errorf("got[%q] = %q, want %q", tt.wantKey, got[tt.wantKey], tt.wantVal)
			}
		})
	}
}

// ─────────────────────────────────────────────
// looksLikeLeakedToolCall
// ─────────────────────────────────────────────

func TestLooksLikeLeakedToolCall(t *testing.T) {
	positive := []string{
		`<tool_call>{"name":"bash","arguments":{}}</tool_call>`,
		`some text <tool_call>{"name":"get_status"}</tool_call> more text`,
		`<function=bash>{"command":"ls"}</function>`,
		`thinking... <|tool▁calls▁begin|>[{"name":"bash"}]<|tool▁calls▁end|>`,
	}
	for _, s := range positive {
		if !looksLikeLeakedToolCall(s) {
			t.Errorf("looksLikeLeakedToolCall(%q) = false, want true", s)
		}
	}

	negative := []string{
		`Let me run the command now.`,
		`{"name":"bash","arguments":{"command":"ls"}}`,
		`<b>bold</b> text`,
	}
	for _, s := range negative {
		if looksLikeLeakedToolCall(s) {
			t.Errorf("looksLikeLeakedToolCall(%q) = true, want false", s)
		}
	}
}

// ─────────────────────────────────────────────
// parseLeakedToolCalls: Qwen3 <tool_call> pattern
// ─────────────────────────────────────────────

func TestParseLeakedToolCallsQwen3Style(t *testing.T) {
	// Qwen3 sometimes outputs tool calls as <tool_call>JSON</tool_call>
	// instead of the native function-calling API format.
	tests := []struct {
		name      string
		text      string
		wantCount int
		wantName  string
		wantArgs  string
	}{
		{
			name:      "single tool_call tag",
			text:      `<tool_call>{"name":"bash","arguments":{"command":"ls -la"}}</tool_call>`,
			wantCount: 1,
			wantName:  "bash",
			wantArgs:  `{"command":"ls -la"}`,
		},
		{
			name:      "tool_call in prose",
			text:      "I'll run the command now.\n<tool_call>{\"name\":\"get_status\",\"arguments\":{}}</tool_call>\nDone.",
			wantCount: 1,
			wantName:  "get_status",
		},
		{
			name:      "multiple tool_call tags",
			text:      `<tool_call>{"name":"bash","arguments":{"command":"pwd"}}</tool_call><tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call>`,
			wantCount: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLeakedToolCalls(tt.text)
			if len(got) != tt.wantCount {
				t.Fatalf("got %d tool calls, want %d; calls=%+v", len(got), tt.wantCount, got)
			}
			if tt.wantName != "" && len(got) > 0 && got[0].Func.Name != tt.wantName {
				t.Errorf("tool call[0].Name = %q, want %q", got[0].Func.Name, tt.wantName)
			}
		})
	}
}

// ─────────────────────────────────────────────
// repairTruncatedJSON (white-box unit test)
// ─────────────────────────────────────────────

func TestRepairTruncatedJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already closed",
			input: `{"command":"ls"}`,
			want:  `{"command":"ls"}`,
		},
		{
			name:  "missing closing brace",
			input: `{"command":"ls"`,
			want:  `{"command":"ls"}`,
		},
		{
			name:  "string and brace both missing",
			input: `{"command":"ls -la`,
			want:  `{"command":"ls -la"}`,
		},
		{
			name: "escaped quote ends string (the #5826 exact pattern)",
			// `{"command":"cd ~/code && curl -s -L -A \"`
			// After \" the string is still open (\" is an escaped " inside the string),
			// so repair must close the string and the object.
			input: `{"command":"cd ~/code && curl -s -L -A \"`,
			want:  `{"command":"cd ~/code && curl -s -L -A \""}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repairTruncatedJSON(tt.input)
			if got != tt.want {
				t.Errorf("repairTruncatedJSON(%q)\n  got  = %q\n  want = %q", tt.input, got, tt.want)
			}
		})
	}
}

// ─────────────────────────────────────────────
// findJSONObjects: nested brace extraction
// ─────────────────────────────────────────────

// TestFindJSONObjects verifies that findJSONObjects correctly extracts balanced
// JSON objects at any nesting depth. The previous regex implementation only
// supported up to 2 levels of nesting, causing failures on deeply nested tool calls.
func TestFindJSONObjects(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantCount int
		wantName  string
		wantArgs  string
	}{
		{
			name:      "flat object (0 nesting)",
			text:      `{"name":"bash","arguments":{"command":"ls"}}`,
			wantCount: 1,
			wantName:  "bash",
			wantArgs:  `{"command":"ls"}`,
		},
		{
			name:      "one level nesting",
			text:      `{"name":"tool","arguments":{"key":{"nested":"value"}}}`,
			wantCount: 1,
			wantName:  "tool",
			wantArgs:  `{"key":{"nested":"value"}}`,
		},
		{
			name:      "two levels nesting",
			text:      `{"name":"tool","arguments":{"a":{"b":{"c":"deep"}}}}`,
			wantCount: 1,
			wantName:  "tool",
			wantArgs:  `{"a":{"b":{"c":"deep"}}}`,
		},
		{
			name:      "three levels nesting",
			text:      `{"name":"tool","arguments":{"a":{"b":{"c":{"d":"very_deep"}}}}}`,
			wantCount: 1,
			wantName:  "tool",
			wantArgs:  `{"a":{"b":{"c":{"d":"very_deep"}}}}`,
		},
		{
			name:      "five levels nesting",
			text:      `{"name":"tool","arguments":{"a":{"b":{"c":{"d":{"e":"extreme"}}}}}}`,
			wantCount: 1,
			wantName:  "tool",
			wantArgs:  `{"a":{"b":{"c":{"d":{"e":"extreme"}}}}}`,
		},
		{
			name: "two tool calls in text",
			text: "Let me run this: {\"name\":\"bash\",\"arguments\":{\"command\":\"pwd\"}} and then {\"name\":\"get_status\",\"arguments\":{}}",
			wantCount: 2,
			wantName:  "bash",
		},
		{
			name: "nested braces with string containing braces",
			text: `{"name":"bash","arguments":{"command":"echo '{hello}'"}}`,
			wantCount: 1,
			wantName:  "bash",
			wantArgs:  `{"command":"echo '{hello}'"}`,
		},
		{
			name: "unmatched opening brace ignored",
			text: `some text { not closed and {"name":"bash","arguments":{"command":"ls"}}`,
			wantCount: 1,
			wantName:  "bash",
			wantArgs:  `{"command":"ls"}`,
		},
		{
			name:      "no tool call in text",
			text:      `just some regular text without any JSON`,
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findJSONObjects(tt.text, tt.text)
			if len(got) != tt.wantCount {
				t.Fatalf("got %d tool calls, want %d; calls=%+v", len(got), tt.wantCount, got)
			}
			if tt.wantName != "" && len(got) > 0 && got[0].Func.Name != tt.wantName {
				t.Errorf("tool call[0].Name = %q, want %q", got[0].Func.Name, tt.wantName)
			}
			if tt.wantArgs != "" && len(got) > 0 && got[0].Func.Args != tt.wantArgs {
				t.Errorf("tool call[0].Args = %q, want %q", got[0].Func.Args, tt.wantArgs)
			}
		})
	}
}
