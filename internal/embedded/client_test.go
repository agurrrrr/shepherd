package embedded

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// ─── Tests for transient error retry (task #6955, §4.1) ─────────────────────

// TestIsTransientLLMError verifies the error classification function.
func TestIsTransientLLMError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unexpected EOF", fmt.Errorf("scan SSE: unexpected EOF"), true},
		{"connection refused", fmt.Errorf("HTTP request: connection refused"), true},
		{"broken pipe", fmt.Errorf("write: broken pipe"), true},
		{"connection reset", fmt.Errorf("read: connection reset by peer"), true},
		{"idle timeout", fmt.Errorf("stream idle timeout after 5m"), true},
		{"HTTP 502", fmt.Errorf("API error 502: overloaded"), true},
		{"HTTP 503", fmt.Errorf("API error 503: service unavailable"), true},
		{"HTTP 529 overloaded", fmt.Errorf("API error 529: overloaded_error"), true},
		{"context canceled", context.Canceled, false},
		{"HTTP 400", fmt.Errorf("API error 400: bad request"), false},
		{"HTTP 401", fmt.Errorf("API error 401: unauthorized"), false},
		{"HTTP 404", fmt.Errorf("API error 404: not found"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientLLMError(tc.err); got != tc.want {
				t.Errorf("isTransientLLMError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestAccumulateStreamWithRetry verifies that a transient error (connection
// drop) is retried and succeeds on the second attempt.
func TestAccumulateStreamWithRetry(t *testing.T) {
	var attempt int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health check endpoint
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		n := atomic.AddInt32(&attempt, 1)
		if n == 1 {
			// First attempt: send a chunk then abruptly close the connection.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}` + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
			// Simulate connection drop — hijack and close.
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				conn.Close()
			}
			return
		}
		// Second attempt: normal response.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		lines := []string{
			`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hello, world!"}}]}`,
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			_, _ = w.Write([]byte(l + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	msg, finish, _, err := c.AccumulateStreamWithRetry(context.Background(), &ChatRequest{Model: "test-model"}, nil, nil)
	if err != nil {
		t.Fatalf("AccumulateStreamWithRetry error: %v", err)
	}
	if finish != "stop" {
		t.Fatalf("finish reason = %q, want stop", finish)
	}
	if msg.Content != "Hello, world!" {
		t.Errorf("content = %q, want 'Hello, world!'", msg.Content)
	}
}

// TestAccumulateStreamWithRetryContextCancel verifies that context cancellation
// during retry wait is respected immediately (no infinite retry loop).
func TestAccumulateStreamWithRetryContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Always drop the connection.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}` + "\n\n"))
		if fl != nil {
			fl.Flush()
		}
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, _, _, err := c.AccumulateStreamWithRetry(ctx, &ChatRequest{Model: "test-model"}, nil, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestAccumulateStreamWithRetryFatalError verifies that non-transient errors
// (e.g. HTTP 400) are not retried.
func TestAccumulateStreamWithRetryFatalError(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	_, _, _, err := c.AccumulateStreamWithRetry(context.Background(), &ChatRequest{Model: "test-model"}, nil, nil)
	if err == nil {
		t.Fatal("expected error from HTTP 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention HTTP 400: %v", err)
	}
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected 1 call (no retry for fatal error), got %d", count)
	}
}

// ─── Tests for repetition guard enhancement (task #6955, §4.2) ──────────────

// TestTailLinesCyclingAlternatingKorean reproduces the #6944 pattern: two
// Korean sentences alternating A/B/A/B... The old tailLinesRepeating could
// not catch this because no two adjacent lines are identical.
func TestTailLinesCyclingAlternatingKorean(t *testing.T) {
	lineA := "IME 창이 포커스를 잃지 않도록 하고 키보드 외부 터치 이벤트를 제어하기 위해 이 플래그들을 제거하거나 수정해야 할 것 같습니다."
	lineB := "`FLAG_NOT_TOUCH_MODAL`과 `FLAG_WATCH_OUTSIDE_TOUCH`를 제거하면 IME 창이 모든 터치를 독점하게 되어 키보드가 즉시 사라지는 문제가 해결됩니다."

	var sb strings.Builder
	for i := 0; i < 8; i++ {
		sb.WriteString(lineA + "\n\n")
		sb.WriteString(lineB + "\n\n")
	}

	if !isDegenerateRepetition(sb.String()) {
		t.Error("isDegenerateRepetition should detect alternating A/B Korean pattern")
	}
	if !tailLinesCycling(sb.String()) {
		t.Error("tailLinesCycling should detect alternating A/B pattern")
	}
}

// TestTailPhraseRepeatingLongCycleKorean verifies that a phrase cycle > 300 bytes
// (the old limit) but < 1200 bytes (the new limit) is caught.
func TestTailPhraseRepeatingLongCycleKorean(t *testing.T) {
	// Two Korean sentences with "\n\n" separator = ~355 bytes per cycle
	// (matching the #6944 pattern exactly).
	lineA := "IME 창이 포커스를 잃지 않도록 하고 키보드 외부 터치 이벤트를 제어하기 위해 이 플래그들을 제거하거나 수정해야 할 것 같습니다."
	lineB := "`FLAG_NOT_TOUCH_MODAL`과 `FLAG_WATCH_OUTSIDE_TOUCH`를 제거하면 IME 창이 모든 터치를 독점하게 되어 키보드가 즉시 사라지는 문제가 해결됩니다."
	unit := lineA + "\n\n" + lineB + "\n\n"

	if len(unit) <= 300 {
		t.Fatalf("test unit must be > 300 bytes for meaningful test, got %d", len(unit))
	}

	result := strings.Repeat(unit, 10)
	if !isDegenerateRepetition(result) {
		t.Error("isDegenerateRepetition should detect long-cycle Korean repetition (> 300 bytes)")
	}
}

// TestTailLinesCyclingNormalCode verifies that normal code output (repeated
// short lines like "})" or "end") does NOT trigger the cycling detector.
func TestTailLinesCyclingNormalCode(t *testing.T) {
	code := `func main() {
	if true {
		for i := 0; i < 10; i++ {
			fmt.Println(i)
		}
	}
}
`
	if tailLinesCycling(code) {
		t.Error("tailLinesCycling should not trigger on normal code output")
	}
}

// TestTailLinesCyclingTableOutput verifies that a table with repeated rows
// (which could look like cycling) doesn't trigger when rows are distinct.
func TestTailLinesCyclingTableOutput(t *testing.T) {
	table := `| Name | Value |
|------|-------|
| foo  | 1     |
| bar  | 2     |
| baz  | 3     |
| qux  | 4     |
| quux | 5     |
`
	if tailLinesCycling(table) {
		t.Error("tailLinesCycling should not trigger on table with distinct rows")
	}
}

// TestHealthCheck verifies the health check endpoint works correctly.
func TestHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	if err := c.HealthCheck(context.Background(), 5*time.Second); err != nil {
		t.Errorf("HealthCheck failed: %v", err)
	}
}

// TestHealthCheckUnreachable verifies that health check fails when server is down.
func TestHealthCheckUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	err := c.HealthCheck(context.Background(), 5*time.Second)
	if err == nil {
		t.Error("HealthCheck should fail when server returns 503")
	}
}
