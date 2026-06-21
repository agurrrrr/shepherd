package worker

import (
	"encoding/json"
	"strings"
	"testing"
)

// streamJSONUserMessage must emit a single line of stream-json input that
// claude accepts as a user turn: a JSON object terminated by "\n", with the
// prompt carried in message.content. Special characters in the prompt must be
// escaped so the line stays valid JSON (a stray newline would corrupt the
// stream and make claude reject every following message).
func TestStreamJSONUserMessage_ShapeAndNewlineTerminator(t *testing.T) {
	out := streamJSONUserMessage("hello")

	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("output must end with \\n, got %q", out)
	}
	line := strings.TrimSuffix(out, "\n")

	var got struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\nline=%q", err, line)
	}
	if got.Type != "user" || got.Message.Role != "user" || got.Message.Content != "hello" {
		t.Errorf("unexpected envelope: %+v", got)
	}
}

// A prompt containing a newline must be escaped — the emitted line itself may
// not contain a raw "\n", otherwise the line-based stdin reader would split it
// into two half-JSON lines and claude would fail to parse both.
func TestStreamJSONUserMessage_EscapesSpecialChars(t *testing.T) {
	out := streamJSONUserMessage("line one\nline two\twith tab")
	if strings.Contains(out[:len(out)-1], "\n") {
		t.Errorf("emitted line contains a raw newline before the terminator: %q", out)
	}
	if strings.Contains(out[:len(out)-1], "\t") {
		t.Errorf("emitted line contains a raw tab: %q", out)
	}

	line := strings.TrimSuffix(out, "\n")
	var got struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("escaped output is not valid JSON: %v", err)
	}
	if got.Message.Content != "line one\nline two\twith tab" {
		t.Errorf("content round-trip mismatch: %q", got.Message.Content)
	}
}

// isStreamResultEvent is what drives the "close stdin after the turn" path, so
// it must accept the real result event shape and reject look-alikes (assistant
// messages, non-JSON noise, malformed JSON).
func TestIsStreamResultEvent(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"result success", `{"type":"result","subtype":"success","result":"done"}`, true},
		{"result error", `{"type":"result","subtype":"error","is_error":true}`, true},
		{"assistant message", `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`, false},
		{"system init", `{"type":"system","subtype":"init","session_id":"s"}`, false},
		{"non-json line", `some plain text`, false},
		{"empty", ``, false},
		{"malformed json", `{"type":"result",broken`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isStreamResultEvent(c.line); got != c.want {
				t.Errorf("isStreamResultEvent(%q) = %v, want %v", c.line, got, c.want)
			}
		})
	}
}
