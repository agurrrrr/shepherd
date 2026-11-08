package worker

import (
	"strings"
	"testing"
)

// parseStreamLine must terminate every discrete event with '\n'. The processor
// LineCoalescer treats a chunk without a newline as an open line and appends the
// next chunk to it, which glues narration onto tool headers and makes the
// interim text invisible in Live Output (#11771).
func TestParseStreamLine_DiscreteEventsEndWithNewline(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "assistant narration",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"먼저 상태를 확인하겠습니다."}]}}`,
			want: "먼저 상태를 확인하겠습니다.\n",
		},
		{
			name: "assistant tool_use",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
			want: "🔧 Bash → ls\n",
		},
		{
			name: "assistant text and tool_use in one event",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"확인하겠습니다."},{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/a.go"}}]}}`,
			want: "확인하겠습니다.\n🔧 Read → /tmp/a.go\n",
		},
		{
			name: "tool result",
			line: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"hello"}]}}`,
			want: "   hello\n",
		},
		{
			name: "result event",
			line: `{"type":"result","subtype":"success","result":"done"}`,
			want: "✅ Task complete\n",
		},
		{
			name: "system init",
			line: `{"type":"system","subtype":"init","session_id":"s1"}`,
			want: "🚀 Claude session starting...\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseStreamLine(c.line)
			if got != c.want {
				t.Errorf("parseStreamLine = %q, want %q", got, c.want)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("event must end with newline: %q", got)
			}
		})
	}
}

// Token deltas are fragments of one line and must NOT carry a newline.
func TestParseStreamLine_DeltaStaysOpen(t *testing.T) {
	got := parseStreamLine(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hel"}}`)
	if got != "hel" {
		t.Errorf("delta = %q, want %q", got, "hel")
	}
}

// Non-JSON noise is emitted as one complete line.
func TestParseStreamLine_NonJSONLine(t *testing.T) {
	if got := parseStreamLine("plain text"); got != "plain text\n" {
		t.Errorf("non-JSON = %q, want %q", got, "plain text\n")
	}
}

// Unknown event types are dropped (no stray empty line).
func TestParseStreamLine_UnknownEventDropped(t *testing.T) {
	if got := parseStreamLine(`{"type":"rate_limit_event"}`); got != "" {
		t.Errorf("unknown event = %q, want empty", got)
	}
}

// End-to-end: a Claude turn with narration between tool calls must keep the
// narration as its own line after LineCoalescer.
func TestParseStreamLine_CoalescedNarrationSeparates(t *testing.T) {
	stream := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"먼저 확인하겠습니다."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"a.go"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"이제 수정하겠습니다."}]}}`,
	}

	var lines []string
	c := NewLineCoalescer(func(line string) { lines = append(lines, line) })
	for _, raw := range stream {
		if p := parseStreamLine(raw); p != "" {
			c.Append(p)
		}
	}
	c.Flush()

	want := []string{
		"🚀 Claude session starting...\n",
		"먼저 확인하겠습니다.\n",
		"🔧 Bash → ls\n",
		"   a.go\n",
		"이제 수정하겠습니다.\n",
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}
