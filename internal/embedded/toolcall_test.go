package embedded

import (
	"encoding/json"
	"fmt"
	"strings"
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

// TestNormalizeJSONWithRepairFlag distinguishes intact JSON from structurally
// repaired (truncated) payloads — write_file/edit_file use the flag to refuse
// silent prefix writes (task #7412).
func TestNormalizeJSONWithRepairFlag(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantRepaired bool
		wantErr     bool
		wantKey     string
		wantVal     string
	}{
		{
			name:         "intact JSON",
			input:        `{"path":"a.go","content":"package main"}`,
			wantRepaired: false,
			wantKey:      "content",
			wantVal:      "package main",
		},
		{
			name:         "truncated mid-content (write_file #7412)",
			input:        `{"path":"a.go","content":"package main\n\nfunc main() {\n  fmt.Println("`,
			wantRepaired: true,
			wantKey:      "path",
			wantVal:      "a.go",
		},
		{
			name:         "truncated mid-key drops content pair",
			input:        `{"path":"a.go","conte`,
			wantRepaired: true,
			wantKey:      "path",
			wantVal:      "a.go",
		},
		{
			name:    "unrepairable",
			input:   `not json at all`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, repaired, err := normalizeJSONWithRepairFlag(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (result=%v repaired=%v)", got, repaired)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repaired != tt.wantRepaired {
				t.Errorf("repaired=%v, want %v", repaired, tt.wantRepaired)
			}
			if tt.wantKey != "" && got[tt.wantKey] != tt.wantVal {
				t.Errorf("got[%q]=%v, want %q", tt.wantKey, got[tt.wantKey], tt.wantVal)
			}
		})
	}
}

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
			name:      "two tool calls in text",
			text:      "Let me run this: {\"name\":\"bash\",\"arguments\":{\"command\":\"pwd\"}} and then {\"name\":\"get_status\",\"arguments\":{}}",
			wantCount: 2,
			wantName:  "bash",
		},
		{
			name:      "nested braces with string containing braces",
			text:      `{"name":"bash","arguments":{"command":"echo '{hello}'"}}`,
			wantCount: 1,
			wantName:  "bash",
			wantArgs:  `{"command":"echo '{hello}'"}`,
		},
		{
			name:      "unmatched opening brace ignored",
			text:      `some text { not closed and {"name":"bash","arguments":{"command":"ls"}}`,
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

// ─────────────────────────────────────────────
// A4: Leaked tool call ID uniqueness
// ─────────────────────────────────────────────

// TestLeakedToolCallUniqueIDs verifies that calling the same tool twice via the
// leaked path produces unique IDs (A4). Before the fix, both calls got
// "leaked-bash" as the ID, causing duplicate key errors on servers that validate
// tool_call_id matching.
func TestLeakedToolCallUniqueIDs(t *testing.T) {
	// Reset counter for deterministic test
	seqBefore := leakedCallSeq

	tests := []struct {
		name string
		text string
	}{
		{
			name: "same tool twice in one message",
			text: `<tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call> and <tool_call>{"name":"bash","arguments":{"command":"pwd"}}</tool_call>`,
		},
		{
			name: "different tools",
			text: `<tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call> then <tool_call>{"name":"get_status","arguments":{}}</tool_call>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLeakedToolCalls(tt.text)
			if len(got) == 0 {
				t.Fatal("no leaked tool calls parsed")
			}

			// Check all IDs are unique within this batch
			seen := make(map[string]int)
			for i, tc := range got {
				if prev, dup := seen[tc.ID]; dup {
					t.Errorf("duplicate ID %q at index %d (first seen at %d)", tc.ID, i, prev)
				}
				seen[tc.ID] = i
			}

			// Check that IDs follow the leaked-{name}-{seq} format
			for _, tc := range got {
				if !strings.HasPrefix(tc.ID, "leaked-") {
					t.Errorf("ID %q doesn't start with leaked-", tc.ID)
				}
				// Verify the ID contains a number (the sequence)
				parts := strings.SplitN(strings.TrimPrefix(tc.ID, "leaked-"), "-", 2)
				if len(parts) < 2 {
					t.Errorf("ID %q doesn't contain sequence number", tc.ID)
				} else {
					var seq int
					_, err := fmt.Sscanf(parts[1], "%d", &seq)
					if err != nil || seq <= 0 {
						t.Errorf("ID %q has invalid sequence: %q", tc.ID, parts[1])
					}
				}
			}

			// Verify the counter actually advanced (uniqueness across calls)
			if leakedCallSeq <= seqBefore {
				t.Errorf("leakedCallSeq didn't advance: before=%d, after=%d", seqBefore, leakedCallSeq)
			}
			seqBefore = leakedCallSeq
		})
	}
}

// TestLeakedToolCallUniqueIDsAcrossCalls verifies that even when parseLeakedToolCalls
// is called multiple times (simulating multiple iterations), IDs remain unique.
func TestLeakedToolCallUniqueIDsAcrossCalls(t *testing.T) {
	allIDs := make(map[string]bool)

	for i := 0; i < 5; i++ {
		text := `<tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call>`
		got := parseLeakedToolCalls(text)
		if len(got) != 1 {
			t.Fatalf("iteration %d: expected 1 tool call, got %d", i, len(got))
		}
		if allIDs[got[0].ID] {
			t.Errorf("iteration %d: duplicate ID %q", i, got[0].ID)
		}
		allIDs[got[0].ID] = true
	}

	if len(allIDs) != 5 {
		t.Errorf("expected 5 unique IDs, got %d", len(allIDs))
	}
}

// ─────────────────────────────────────────────
// A3: removeLeakedMarkers — strips marker blocks from content
// ─────────────────────────────────────────────

func TestRemoveLeakedMarkers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single marker block",
			input:    `thinking <tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call> done`,
			expected: "thinking done",
		},
		{
			name:     "marker block only",
			input:    `<tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call>`,
			expected: "",
		},
		{
			name:     "two marker blocks with prose between",
			input:    `start <tool_call>{"name":"bash","arguments":{"command":"ls"}}</tool_call> middle <tool_call>{"name":"get_status","arguments":{}}</tool_call> end`,
			expected: "start middle end",
		},
		{
			name:     "no markers",
			input:    `just plain text`,
			expected: "just plain text",
		},
		{
			name:     "xml-style markers",
			input:    `<|tool▁calls▁begin|>{"name":"bash"}<|tool▁calls▁end|>`,
			expected: "",
		},
		{
			name:     "prose before and after xml markers",
			input:    `before <|tool▁calls▁begin|>{"name":"bash"}<|tool▁calls▁end|> after`,
			expected: "before after",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeLeakedMarkers(tt.input)
			if got != tt.expected {
				t.Errorf("removeLeakedMarkers(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ─────────────────────────────────────────────
// A3: sanitizeLeakedToolCalls — same as native path
// ─────────────────────────────────────────────

func TestSanitizeLeakedToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		input    []ToolCall
		wantArgs []string // expected args after sanitization, "" means unchanged
	}{
		{
			name: "valid JSON unchanged",
			input: []ToolCall{
				{ID: "t1", Type: "function", Func: ToolCallFunction{Name: "bash", Args: `{"command":"ls"}`}},
			},
			wantArgs: []string{`{"command":"ls"}`},
		},
		{
			name: "empty args replaced with {}",
			input: []ToolCall{
				{ID: "t1", Type: "function", Func: ToolCallFunction{Name: "bash", Args: ""}},
			},
			wantArgs: []string{"{}"},
		},
		{
			name: "truncated args repaired",
			input: []ToolCall{
				{ID: "t1", Type: "function", Func: ToolCallFunction{Name: "bash", Args: `{"command":"ls -la`}},
			},
			wantArgs: []string{`{"command":"ls -la"}`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeLeakedToolCalls(tt.input)
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("got %d tool calls, want %d", len(got), len(tt.wantArgs))
			}
			for i, want := range tt.wantArgs {
				if got[i].Func.Args != want {
					t.Errorf("tool[%d].Args = %q, want %q", i, got[i].Func.Args, want)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────
// A3: Full leaked message reconstruction integration test
// ─────────────────────────────────────────────

// TestLeakedMessageReconstruction verifies that the reconstructed assistant message
// from a leaked tool call path: (1) has no marker text in content, (2) has ToolCalls
// properly populated, (3) tool messages' ToolCallID matches the assistant's tool_calls.
func TestLeakedMessageReconstruction(t *testing.T) {
	// Simulate what the loop does for leaked tool calls.
	content := `Let me check: <tool_call>{"name":"bash","arguments":{"command":"ls -la"}}</tool_call> and <tool_call>{"name":"get_status","arguments":{}}</tool_call> done.`

	leaked := parseLeakedToolCalls(content)
	if len(leaked) != 2 {
		t.Fatalf("expected 2 leaked tool calls, got %d", len(leaked))
	}

	// Step 1: Build reconstructed assistant message (same as loop.go)
	toolCalls := make([]ToolCall, 0, len(leaked))
	for _, tc := range leaked {
		toolCalls = append(toolCalls, tc.ToolCall)
	}
	toolCalls = sanitizeLeakedToolCalls(toolCalls)

	cleanContent := removeLeakedMarkers(content)

	assistantMsg := ChatMessage{
		Role:      ChatRoleAssistant,
		Content:   cleanContent,
		ToolCalls: toolCalls,
	}

	// Assertion 1: No marker text in content
	if strings.Contains(assistantMsg.Content, "<tool_call>") {
		t.Errorf("reconstructed content still contains marker '<tool_call>': %q", assistantMsg.Content)
	}
	if strings.Contains(assistantMsg.Content, "</tool_call>") {
		t.Errorf("reconstructed content still contains marker '</tool_call>': %q", assistantMsg.Content)
	}

	// Assertion 2: ToolCalls properly populated
	if len(assistantMsg.ToolCalls) != 2 {
		t.Errorf("expected 2 tool_calls, got %d", len(assistantMsg.ToolCalls))
	}

	// Assertion 3: ToolCall IDs are unique
	idSet := make(map[string]bool)
	for _, tc := range assistantMsg.ToolCalls {
		if idSet[tc.ID] {
			t.Errorf("duplicate tool_call_id: %q", tc.ID)
		}
		idSet[tc.ID] = true
	}

	// Assertion 4: Simulate tool result messages and verify ID matching
	for i, tc := range leaked {
		toolMsg := ChatMessage{
			Role:       ChatRoleTool,
			Content:    "result",
			ToolCallID: tc.ID,
		}
		// The tool message's ToolCallID should match one of the assistant's tool_calls
		found := false
		for _, atc := range assistantMsg.ToolCalls {
			if atc.ID == toolMsg.ToolCallID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool message[%d] ToolCallID %q doesn't match any assistant tool_call", i, toolMsg.ToolCallID)
		}
	}

	// Assertion 5: Verify the reconstructed message marshals to valid JSON
	data, err := json.Marshal(assistantMsg)
	if err != nil {
		t.Fatalf("failed to marshal reconstructed message: %v", err)
	}

	var unmarshaled ChatMessage
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal reconstructed message: %v", err)
	}
	if len(unmarshaled.ToolCalls) != 2 {
		t.Errorf("round-trip: expected 2 tool_calls, got %d", len(unmarshaled.ToolCalls))
	}
}

// ─────────────────────────────────────────────
// A5: <function=name>...</function> dedicated parser
// ─────────────────────────────────────────────

func TestParseFunctionCallTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantArgs string
	}{
		{
			name:     "basic bash call",
			input:    `<function=bash>{"command":"ls"}</function>`,
			wantName: "bash",
			wantArgs: `{"command":"ls"}`,
		},
		{
			name:     "with surrounding text",
			input:    `Let me run this: <function=bash>{"command":"ls -la /tmp"}</function> done.`,
			wantName: "bash",
			wantArgs: `{"command":"ls -la /tmp"}`,
		},
		{
			name:     "read_file tool",
			input:    `<function=read_file>{"path":"foo.txt"}</function>`,
			wantName: "read_file",
			wantArgs: `{"path":"foo.txt"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := parseLeakedToolCalls(tt.input)
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Func.Name != tt.wantName {
				t.Errorf("name = %q, want %q", results[0].Func.Name, tt.wantName)
			}
			if results[0].Func.Args != tt.wantArgs {
				t.Errorf("args = %q, want %q", results[0].Func.Args, tt.wantArgs)
			}
		})
	}
}

func TestParseFunctionCallTagMultiple(t *testing.T) {
	input := `<function=bash>{"command":"ls"}</function> and then <function=read_file>{"path":"a.txt"}</function>`
	results := parseLeakedToolCalls(input)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Func.Name != "bash" {
		t.Errorf("first name = %q, want bash", results[0].Func.Name)
	}
	if results[1].Func.Name != "read_file" {
		t.Errorf("second name = %q, want read_file", results[1].Func.Name)
	}
}
