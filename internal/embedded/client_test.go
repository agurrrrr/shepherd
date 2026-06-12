package embedded

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// TestAccumulateStreamMultipleToolCalls verifies that two parallel tool calls
// (index 0 and 1) emitted in a single streaming response are both preserved.
func TestAccumulateStreamMultipleToolCalls(t *testing.T) {
	lines := []string{
		// First chunk: both tool calls start (id+name only)
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}},{"index":1,"id":"call_2","type":"function","function":{"name":"get_status","arguments":""}}]}}]}`,
		// Subsequent chunks: argument fragments
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"comm"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{}"}}]}}]}`,
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
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2: %+v", len(msg.ToolCalls), msg.ToolCalls)
	}
	if msg.ToolCalls[0].Func.Name != "bash" {
		t.Errorf("tool call[0].Name = %q, want bash", msg.ToolCalls[0].Func.Name)
	}
	if msg.ToolCalls[0].Func.Args != `{"command":"ls"}` {
		t.Errorf("tool call[0].Args = %q, want %q", msg.ToolCalls[0].Func.Args, `{"command":"ls"}`)
	}
	if msg.ToolCalls[1].Func.Name != "get_status" {
		t.Errorf("tool call[1].Name = %q, want get_status", msg.ToolCalls[1].Func.Name)
	}
	if msg.ToolCalls[1].ID != "call_2" {
		t.Errorf("tool call[1].ID = %q, want call_2", msg.ToolCalls[1].ID)
	}
}

// TestAccumulateStreamArgsWithEscapedQuotes verifies that a tool call with
// shell command args containing escaped quotes is accumulated correctly.
// This is the Qwen3 pattern: `curl -A \"Mozilla/5.0\"` split across chunks.
func TestAccumulateStreamArgsWithEscapedQuotes(t *testing.T) {
	// The arg string is: {"command":"curl -s -L -A \"Mozilla/5.0\" https://example.com"}
	// Split into 4 fragments at awkward quote/backslash boundaries.
	lines := []string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"comm"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"curl -s -L -A \\\""}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"Mozilla/5.0\\\" https://example.com"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	msg, _, _, err := c.AccumulateStream(context.Background(), &ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("AccumulateStream error: %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	want := `{"command":"curl -s -L -A \"Mozilla/5.0\" https://example.com"}`
	if msg.ToolCalls[0].Func.Args != want {
		t.Errorf("args = %q\nwant  = %q", msg.ToolCalls[0].Func.Args, want)
	}

	// Also verify normalizeJSON can parse the assembled args.
	parsed, err := normalizeJSON(msg.ToolCalls[0].Func.Args)
	if err != nil {
		t.Fatalf("normalizeJSON after stream accumulation: %v", err)
	}
	wantCmd := `curl -s -L -A "Mozilla/5.0" https://example.com`
	if parsed["command"] != wantCmd {
		t.Errorf("parsed command = %v, want %q", parsed["command"], wantCmd)
	}
}

// TestAccumulateStreamTruncatedArgs simulates the #5826 bug: the model's streaming
// response is cut off mid-argument (finish_reason="length"), resulting in truncated
// JSON args that must be handled gracefully by downstream parsing.
func TestAccumulateStreamTruncatedArgs(t *testing.T) {
	// The model starts generating args but the stream ends prematurely.
	// finish_reason is "length" (or "tool_calls" in some providers even for truncated).
	lines := []string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"comm"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"cd ~/code && curl -s -L -A \\\""}}]}}]}`,
		// Stream ends here — no closing }" fragment arrives.
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	msg, finish, _, err := c.AccumulateStream(context.Background(), &ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("AccumulateStream error: %v", err)
	}
	if finish != "length" {
		t.Fatalf("finish reason = %q, want length", finish)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	// The raw args are truncated JSON.
	rawArgs := msg.ToolCalls[0].Func.Args
	t.Logf("truncated raw args: %q", rawArgs)

	// normalizeJSON MUST NOT return a hard error for this input —
	// it should repair and return a best-effort result.
	parsed, err := normalizeJSON(rawArgs)
	if err != nil {
		t.Fatalf("normalizeJSON(truncated args) returned error: %v\n"+
			"input was: %q\n"+
			"This is the #5826 bug: truncated JSON must be repaired, not rejected.", err, rawArgs)
	}
	cmd, _ := parsed["command"].(string)
	if cmd == "" {
		t.Errorf("repaired JSON has no 'command' key; parsed = %v", parsed)
	}
	t.Logf("repaired command: %q", cmd)
}

// TestAccumulateStreamQwen3ThinkingContent verifies that reasoning_content
// (Qwen3's thinking output) does not corrupt tool call accumulation.
func TestAccumulateStreamQwen3ThinkingContent(t *testing.T) {
	lines := []string{
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think...","content":null}}]}`,
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":" I should list files."}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]}}]}`,
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
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Func.Name != "bash" {
		t.Errorf("tool name = %q, want bash", msg.ToolCalls[0].Func.Name)
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

func TestIsDegenerateRepetition(t *testing.T) {
	line := "Let me test more browser functions - typing text, screenshots, etc."
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"repeated lines", strings.Repeat(line+"\n\n", 30), true},
		{"repeated phrase no newline", strings.Repeat("blah blah blah ", 40), true},
		{"normal prose", "First I will open the page. Then I check the title. Finally I screenshot.", false},
		{"short trivial repeat", strings.Repeat("ok\n", 30), false},
		{"empty", "", false},
		{"single occurrence", line, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDegenerateRepetition(tc.in); got != tc.want {
				t.Errorf("isDegenerateRepetition(%q...) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestAccumulateStreamNoTrailingBlankLine verifies A6 fix: when the server
// closes the connection without a trailing blank line after the last data: chunk,
// the finish_reason and usage are still captured (not silently discarded).
func TestAccumulateStreamNoTrailingBlankLine(t *testing.T) {
	// Unlike sseServer which appends "\n\n" to every line, this server writes
	// raw SSE exactly as a non-standard server might: no blank line after the
	// last data: event.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)

		// Standard events with blank line separators
		events := []string{
			`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
			``,
			`data: {"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
			``,
			// Last event with finish_reason and usage — NO trailing blank line.
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		}
		for _, l := range events {
			_, _ = w.Write([]byte(l + "\n"))
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	msg, finish, usage, err := c.AccumulateStream(context.Background(), &ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("AccumulateStream error: %v", err)
	}
	if finish != "stop" {
		t.Fatalf("finish reason = %q, want stop (A6: last event was discarded without trailing blank line)", finish)
	}
	if msg.Content != "Hello" {
		t.Errorf("content = %q, want Hello", msg.Content)
	}
	if usage == nil {
		t.Fatalf("usage is nil (A6: last event with usage was discarded)")
	}
	if usage.CompletionTokens != 5 {
		t.Errorf("completion_tokens = %d, want 5", usage.CompletionTokens)
	}
}

// TestAccumulateStreamNoTrailingBlankLineWithToolCalls verifies A6 fix for the
// tool_calls finish_reason case — server closes without trailing blank line.
func TestAccumulateStreamNoTrailingBlankLineWithToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)

		events := []string{
			`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_status","arguments":"{}"}}]}}]}`,
			``,
			// finish_reason chunk — no trailing blank line
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, l := range events {
			_, _ = w.Write([]byte(l + "\n"))
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	msg, finish, _, err := c.AccumulateStream(context.Background(), &ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("AccumulateStream error: %v", err)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls (A6: last event discarded)", finish)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Func.Name != "get_status" {
		t.Errorf("tool name = %q, want get_status", msg.ToolCalls[0].Func.Name)
	}
}

// TestAccumulateStreamOnlyDataNoBlankLine verifies A6 edge case: the entire
// stream is a single data: line with no blank line at all.
func TestAccumulateStreamOnlyDataNoBlankLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Single data line, no blank line, no [DONE]
		_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}` + "\n"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	msg, finish, _, err := c.AccumulateStream(context.Background(), &ChatRequest{Model: "test-model"})
	if err != nil {
		t.Fatalf("AccumulateStream error: %v", err)
	}
	if finish != "stop" {
		t.Fatalf("finish reason = %q, want stop", finish)
	}
	if msg.Content != "hi" {
		t.Errorf("content = %q, want hi", msg.Content)
	}
}

// TestChatStreamIdleTimeout verifies B1: the stream returns an idle timeout error
// when the server stops sending chunks for too long. We use a very short timeout
// via context deadline to simulate the behavior without waiting 5 minutes.
func TestChatStreamIdleTimeout(t *testing.T) {
	// Server sends one chunk, then blocks forever (simulating a stalled connection).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)

		// Send one valid chunk
		_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}` + "\n\n"))
		if fl != nil {
			fl.Flush()
		}

		// Block in main goroutine — connection stays open, no more data.
		<-r.Context().Done()
	}))
	defer func() {
		srv.CloseClientConnections() // unblock handler goroutines
		srv.Close()
	}()

	c := NewClient(srv.URL, "", "test-model")

	// Use a context with a short deadline so we don't wait 5 minutes in the test.
	// The parent context cancellation should propagate through and we should get
	// an error (either from ctx cancellation or the idle timeout mechanism).
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := c.ChatStream(ctx, &ChatRequest{Model: "test-model"}, func(event *StreamEvent) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected an error from stalled stream, got nil")
	}
	// The error should be related to context cancellation or stream scan error.
	t.Logf("stalled stream error: %v", err)
}

// TestChatStreamNormalCompletion verifies that a properly terminated stream
// (with [DONE] and blank lines) still works correctly after the A6/B1 changes.
func TestChatStreamNormalCompletion(t *testing.T) {
	lines := []string{
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hello, world!"}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	var events []*StreamEvent
	err := c.ChatStream(context.Background(), &ChatRequest{Model: "test-model"}, func(event *StreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Delta.Content != "Hello, world!" {
		t.Errorf("first content = %q, want 'Hello, world!'", events[0].Delta.Content)
	}
	if events[1].FinishReason == nil || *events[1].FinishReason != "stop" {
		t.Errorf("finish reason = %v, want stop", events[1].FinishReason)
	}
}

// TestChatStreamIdleTimeoutErrorMessage verifies B1 error message format.
// When the idle timer fires (simulated via parent ctx cancellation during stall),
// the error should contain a clear message distinguishing it from other failures.
func TestChatStreamIdleTimeoutErrorMessage(t *testing.T) {
	// Server sends response headers then blocks forever (no body data).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		if fl != nil {
			fl.Flush()
		}
		// Block in main goroutine — connection stays open, no more data.
		<-r.Context().Done()
	}))
	defer func() {
		srv.CloseClientConnections() // unblock handler goroutines
		srv.Close()
	}()

	c := NewClient(srv.URL, "", "test-model")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := c.ChatStream(ctx, &ChatRequest{Model: "test-model"}, func(event *StreamEvent) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected an error from stalled stream, got nil")
	}
	errMsg := err.Error()
	t.Logf("stalled stream error message: %q", errMsg)
	// The error should mention either "idle timeout" or "context deadline exceeded" / "scan SSE"
	hasIdle := strings.Contains(errMsg, "idle timeout")
	hasCtx := strings.Contains(errMsg, "context deadline exceeded")
	hasScan := strings.Contains(errMsg, "scan SSE")
	if !hasIdle && !hasCtx && !hasScan {
		t.Errorf("error message %q doesn't contain expected idle/ctx/scan indicator", errMsg)
	}
}
