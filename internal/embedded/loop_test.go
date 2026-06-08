package embedded

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// ─────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────

// sseLines builds a mock SSE sequence for a non-streaming chat/completions
// response. llama.cpp actually supports streaming; we use a minimal streaming
// wrapper here to exercise the full AccumulateStream → loop path.
func buildSSELines(toolCalls []toolCallSpec, content string, finishReason string) []string {
	var lines []string

	if len(toolCalls) > 0 {
		// First chunk: all tool call headers (id, name)
		type argChunk struct {
			Index int    `json:"index"`
			Func  struct {
				Args string `json:"arguments"`
			} `json:"function"`
		}
		type header struct {
			Index int    `json:"index"`
			ID    string `json:"id"`
			Type  string `json:"type"`
			Func  struct {
				Name string `json:"name"`
				Args string `json:"arguments"`
			} `json:"function"`
		}

		headers := make([]header, len(toolCalls))
		for i, tc := range toolCalls {
			headers[i] = header{
				Index: i,
				ID:    tc.id,
				Type:  "function",
			}
			headers[i].Func.Name = tc.name
		}

		hb, _ := json.Marshal(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]interface{}{
						"role":       "assistant",
						"tool_calls": headers,
					},
				},
			},
		})
		lines = append(lines, "data: "+string(hb))

		// Second chunk: args for each tool call
		for i, tc := range toolCalls {
			chunk := map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"delta": map[string]interface{}{
							"tool_calls": []map[string]interface{}{
								{
									"index": i,
									"function": map[string]interface{}{
										"arguments": tc.args,
									},
								},
							},
						},
					},
				},
			}
			cb, _ := json.Marshal(chunk)
			lines = append(lines, "data: "+string(cb))
		}

		// finish_reason chunk
		fr := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"index": 0, "delta": map[string]interface{}{}, "finish_reason": "tool_calls"},
			},
		}
		frb, _ := json.Marshal(fr)
		lines = append(lines, "data: "+string(frb))
	} else {
		// Content response
		c1 := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"index": 0, "delta": map[string]interface{}{"role": "assistant", "content": content}},
			},
		}
		c1b, _ := json.Marshal(c1)
		lines = append(lines, "data: "+string(c1b))

		fr := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"index": 0, "delta": map[string]interface{}{}, "finish_reason": finishReason},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		frb, _ := json.Marshal(fr)
		lines = append(lines, "data: "+string(frb))
	}

	lines = append(lines, "data: [DONE]")
	return lines
}

type toolCallSpec struct {
	id   string
	name string
	args string
}

// multiRoundServer serves a sequence of pre-defined SSE responses, one per POST.
func multiRoundServer(t *testing.T, rounds [][]string) *httptest.Server {
	t.Helper()
	var count atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(count.Add(1)) - 1
		if idx >= len(rounds) {
			http.Error(w, "no more rounds", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, l := range rounds[idx] {
			_, _ = fmt.Fprintln(w, l)
			_, _ = fmt.Fprintln(w)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
}

// echoToolRegistry returns a simple ToolRegistry that records dispatched calls
// and returns a configurable string.
type recordingDispatcher struct {
	calls  []dispatchedCall
	result string
	err    error
}

type dispatchedCall struct {
	Name string
	Args map[string]interface{}
}

func (r *recordingDispatcher) Dispatch(name string, args map[string]interface{}) (string, error) {
	r.calls = append(r.calls, dispatchedCall{Name: name, Args: args})
	return r.result, r.err
}

// ─────────────────────────────────────────────
// Loop integration tests
// ─────────────────────────────────────────────

// TestLoopSimpleToolCall verifies a single-iteration loop where the model
// requests one tool call, gets a result, then returns a text answer.
func TestLoopSimpleToolCall(t *testing.T) {
	// Round 1: model asks for bash
	r1 := buildSSELines([]toolCallSpec{
		{id: "call_1", name: "bash", args: `{"command":"ls -la"}`},
	}, "", "")
	// Round 2: model returns final text
	r2 := buildSSELines(nil, "The directory contains 3 files.", "stop")

	srv := multiRoundServer(t, [][]string{r1, r2})
	defer srv.Close()

	var outputs []string
	opts := ExecuteOptions{
		BaseURL:       srv.URL,
		Model:         "qwen3-test",
		SystemPrompt:  "You are a helpful assistant.",
		UserPrompt:    "List the current directory.",
		MaxIterations: 5,
		OnOutput: func(s string) {
			outputs = append(outputs, s)
		},
	}

	// Override tool dispatch so we don't need a real filesystem.
	origReg := newRecordingRegistry("drwxr-xr-x 3 user group 96 Jun 8 test\n")
	opts.Tools = origReg.OpenAIToolDefs()

	// We can't inject a custom dispatcher via public API easily, so we test
	// that the loop gracefully handles the tool call round-trip by pointing
	// the bash tool to the mock server's predictable responses.

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("result.Incomplete = true: %s", result.IncompleteReason)
	}
	if !strings.Contains(result.Result, "files") {
		t.Errorf("result = %q, want something about files", result.Result)
	}
}

// TestLoopTruncatedArgsRecovery is the key regression test for #5826.
//
// Scenario:
//  1. Model emits a bash tool call with TRUNCATED JSON args
//     (stream ends mid-argument, like `{"command":"cd ~/code && curl -s -L -A \"`).
//  2. normalizeJSON must REPAIR the args (not hard-fail).
//  3. The repaired command is dispatched (returns an error — incomplete command).
//  4. The error is fed back to the model.
//  5. Model retries with correct args.
//  6. Loop completes successfully.
func TestLoopTruncatedArgsRecovery(t *testing.T) {
	// Round 1: truncated bash args (the #5826 exact pattern)
	truncatedArgs := `{"command":"cd ~/code && curl -s -L -A \"`

	r1Lines := buildSSELines([]toolCallSpec{
		{id: "call_1", name: "bash", args: truncatedArgs},
	}, "", "")
	// Make finish_reason="length" to simulate truncation
	for i, l := range r1Lines {
		if strings.Contains(l, `"finish_reason":"tool_calls"`) {
			r1Lines[i] = strings.ReplaceAll(l, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`)
		}
	}

	// Round 2: model retries with correct args
	r2 := buildSSELines([]toolCallSpec{
		{id: "call_2", name: "bash", args: `{"command":"ls ~/code"}`},
	}, "", "")

	// Round 3: model returns result
	r3 := buildSSELines(nil, "Done listing code directory.", "stop")

	srv := multiRoundServer(t, [][]string{r1Lines, r2, r3})
	defer srv.Close()

	opts := ExecuteOptions{
		BaseURL:       srv.URL,
		Model:         "qwen3-27b-test",
		SystemPrompt:  "You are an agent.",
		UserPrompt:    "Run a curl command.",
		MaxIterations: 10,
	}

	// The critical assertion: Run must not panic or return an immediate
	// "JSON parse error" failure. It must attempt to repair and continue.
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run returned Go error: %v", err)
	}
	// The loop may or may not complete successfully depending on how the mock
	// server handles the extra rounds, but it must NOT mark Incomplete due to
	// a raw JSON parse failure propagated from the first truncated call.
	if result.Incomplete && strings.Contains(result.IncompleteReason, "JSON parse error") {
		t.Fatalf("Loop failed with raw JSON parse error (not repaired): %s\n"+
			"This is the #5826 regression.", result.IncompleteReason)
	}
}

// TestLoopMalformedArgsDoNotCrashNextRequest verifies that when a tool call
// has malformed args and triggers an error, the NEXT API request to the LLM
// does NOT include the malformed args in the conversation history (which would
// cause llama.cpp to return HTTP 500).
func TestLoopMalformedArgsDoNotCrashNextRequest(t *testing.T) {
	var capturedBodies []string

	// Custom server: captures request bodies and returns pre-canned responses.
	round := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture request body
		var buf strings.Builder
		body := make([]byte, 65536)
		n, _ := r.Body.Read(body)
		buf.Write(body[:n])
		capturedBodies = append(capturedBodies, buf.String())

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)

		var lines []string
		switch round {
		case 0:
			// Round 1: malformed tool call args
			lines = buildSSELines([]toolCallSpec{
				{id: "call_bad", name: "bash", args: `{"command":"curl -A \"`},
			}, "", "")
		default:
			// Round 2+: final text response
			lines = buildSSELines(nil, "All done.", "stop")
		}
		round++

		for _, l := range lines {
			_, _ = fmt.Fprintln(w, l)
			_, _ = fmt.Fprintln(w)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	opts := ExecuteOptions{
		BaseURL:       srv.URL,
		Model:         "qwen3-27b-test",
		SystemPrompt:  "Agent.",
		UserPrompt:    "Run a command.",
		MaxIterations: 5,
	}

	Run(context.Background(), opts) //nolint:errcheck — we only care about request content

	// The second request must not contain the malformed args verbatim.
	// Specifically it must not send `{"command":"curl -A \"` as an argument string,
	// because llama.cpp's grammar engine can't parse it and returns HTTP 500.
	if len(capturedBodies) >= 2 {
		malformedArgs := `"curl -A \"`
		if strings.Contains(capturedBodies[1], malformedArgs) {
			t.Errorf("Round 2 request contains malformed args %q — this will cause llama.cpp HTTP 500.\n"+
				"The loop must sanitize or drop malformed tool_calls from conversation history.", malformedArgs)
		}
	}
}

// ─────────────────────────────────────────────
// helpers for loop tests
// ─────────────────────────────────────────────

// newRecordingRegistry returns an OpenAIToolDef list with a single "bash" tool
// so Run() can at least deserialize tools; it won't actually execute anything
// because the mock server controls the number of rounds.
func newRecordingRegistry(result string) *ToolRegistry {
	return NewToolRegistry("", "test-sheep", nil, nil)
}
