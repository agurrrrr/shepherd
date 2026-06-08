package embedded

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sseServer returns an httptest server that streams the given raw SSE lines.
func sseServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, l := range lines {
			_, _ = w.Write([]byte(l + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
	}))
}

// TestAccumulateStreamToolCallByIndex verifies that a tool call split across
// multiple SSE chunks (only the first carrying id/name, the rest carrying
// argument fragments with empty id/name) is reassembled into a single tool
// call keyed by index — not fragmented into several broken calls.
func TestAccumulateStreamToolCallByIndex(t *testing.T) {
	lines := []string{
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":null}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_history","arguments":"{\"project"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"_name\":\"shepherd\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	msg, finish, _, err := c.AccumulateStream(context.Background(), &ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("AccumulateStream error: %v", err)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", finish)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1 (fragments must merge by index): %+v", len(msg.ToolCalls), msg.ToolCalls)
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("tool call ID = %q, want call_1", tc.ID)
	}
	if tc.Func.Name != "get_history" {
		t.Errorf("tool call name = %q, want get_history", tc.Func.Name)
	}
	if want := `{"project_name":"shepherd"}`; tc.Func.Args != want {
		t.Errorf("tool call args = %q, want %q", tc.Func.Args, want)
	}
}

// TestAccumulateStreamContent verifies plain content accumulation across chunks.
func TestAccumulateStreamContent(t *testing.T) {
	lines := []string{
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	msg, finish, _, err := c.AccumulateStream(context.Background(), &ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("AccumulateStream error: %v", err)
	}
	if finish != "stop" {
		t.Fatalf("finish reason = %q, want stop", finish)
	}
	if msg.Content != "Hello" {
		t.Errorf("content = %q, want Hello", msg.Content)
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("got %d tool calls, want 0", len(msg.ToolCalls))
	}
}
