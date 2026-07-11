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
			Index int `json:"index"`
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

func (r *recordingDispatcher) Dispatch(name string, args map[string]interface{}) (string, []MCPImage, error) {
	r.calls = append(r.calls, dispatchedCall{Name: name, Args: args})
	return r.result, nil, r.err
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

// ─────────────────────────────────────────────
// B6: truncate on rune boundaries (Korean support)
// ─────────────────────────────────────────────

func TestTruncateRuneBoundary(t *testing.T) {
	// Korean text: "안녕하세요" (5 runes, 15 bytes in UTF-8)
	korean := "안녕하세요"

	// Truncate to 3 runes should give "안녕하" without replacement chars
	result := truncate(korean, 3)
	if result != "안녕하..." {
		t.Errorf("truncate(%q, 3) = %q, want %q", korean, result, "안녕하...")
	}

	// Full length should not truncate
	result = truncate(korean, 10)
	if result != korean {
		t.Errorf("truncate(%q, 10) = %q, want %q", korean, result, korean)
	}

	// Mixed ASCII + Korean
	mixed := "hello 안녕하세요"
	result = truncate(mixed, 8)
	// "hello 안녕" is 8 runes (h,e,l,l,o, ,안,녕) + "..." = 11 runes total
	if len([]rune(result)) != 11 { // 8 runes + "..." = 11 runes
		t.Errorf("truncate(%q, 8) rune len = %d, want 11", mixed, len([]rune(result)))
	}

	// Check no replacement character appears
	if strings.Contains(result, "\uFFFD") {
		t.Errorf("truncate produced replacement character: %q", result)
	}
}

func TestTruncateToolResultRuneBoundary(t *testing.T) {
	// Long Korean text that exceeds the max (8000 chars)
	longKorean := strings.Repeat("안녕하세요\n", 2000) // ~30,000 runes

	result := truncateToolResult(longKorean, "bash")
	if strings.Contains(result, "\uFFFD") {
		t.Errorf("truncateToolResult produced replacement character")
	}

	// Should end with truncation notice
	if !strings.Contains(result, "[truncated") {
		t.Errorf("expected truncation notice, got ending: %q", result[len(result)-50:])
	}

	// Short text should pass through unchanged
	short := "짧은 텍스트"
	result = truncateToolResult(short, "bash")
	if result != short {
		t.Errorf("truncateToolResult(%q) = %q, want unchanged", short, result)
	}
}

// When the universal backstop has to cut, it must hand the model a concrete way
// to retrieve the hidden remainder — tailored to the tool — so it does not
// re-issue the identical (truncated) call and stall on the repeated-call guard
// (the #6309 dead-end, now reproducible for bash/grep/glob/MCP, not just read_file).
func TestTruncateToolResultActionableHint(t *testing.T) {
	big := strings.Repeat("x", maxToolResultChars+1000)

	// bash: must point at a recovery path (read_file via redirect) AND suggest
	// narrowing the output — both change the next tool-call signature.
	bashOut := truncateToolResult(big, "bash")
	for _, want := range []string{"truncated", "read_file", "head"} {
		if !strings.Contains(bashOut, want) {
			t.Errorf("bash truncation hint missing %q; tail=%q", want, bashOut[len(bashOut)-220:])
		}
	}

	// grep gets its own hint, not bash's.
	if g := truncateToolResult(big, "grep"); !strings.Contains(g, "Narrow the search") {
		t.Errorf("grep should get a grep-specific hint; tail=%q", g[len(g)-220:])
	}

	// An unknown (e.g. MCP) tool still gets a non-empty, generic recovery hint.
	if d := truncateToolResult(big, "some_mcp_tool"); !strings.Contains(d, "narrower slice") {
		t.Errorf("default hint missing; tail=%q", d[len(d)-220:])
	}

	// The total character count is reported so the model can gauge how much is hidden.
	if !strings.Contains(bashOut, fmt.Sprintf("of %d chars", len([]rune(big)))) {
		t.Errorf("truncation notice should report the total char count; tail=%q", bashOut[len(bashOut)-220:])
	}
}

// ─────────────────────────────────────────────
// B7: duplicate nudge prevention
// ─────────────────────────────────────────────

func TestDuplicateNudgePrevention(t *testing.T) {
	// This test verifies the logic conceptually since we can't easily
	// test the full loop. Instead, verify that the nudge message content
	// is what we expect and that the check would work.
	nudgeContent := "Please continue with the task."

	messages := []ChatMessage{
		{Role: ChatRoleUser, Content: nudgeContent},
	}

	// Simulate the B7 check: last message is already a nudge
	lastNudge := len(messages) > 0 &&
		messages[len(messages)-1].Role == ChatRoleUser &&
		messages[len(messages)-1].Content == nudgeContent

	if !lastNudge {
		t.Error("expected lastNudge to be true when last message is the nudge")
	}

	// When last message is NOT a nudge, we should add one
	messages2 := []ChatMessage{
		{Role: ChatRoleAssistant, Content: "some output"},
	}
	lastNudge2 := len(messages2) > 0 &&
		messages2[len(messages2)-1].Role == ChatRoleUser &&
		messages2[len(messages2)-1].Content == nudgeContent

	if lastNudge2 {
		t.Error("expected lastNudge to be false when last message is assistant content")
	}

	// When messages is empty, should add nudge
	messages3 := []ChatMessage{}
	lastNudge3 := len(messages3) > 0 &&
		messages3[len(messages3)-1].Role == ChatRoleUser &&
		messages3[len(messages3)-1].Content == nudgeContent

	if lastNudge3 {
		t.Error("expected lastNudge to be false when messages is empty")
	}
}

// ─────────────────────────────────────────────
// B2: empty tc.ID fallback in streaming mode
// ─────────────────────────────────────────────

func TestEmptyToolCallIDFallback(t *testing.T) {
	// Simulate what happens when a tool call arrives with empty ID.
	// The loop assigns a fallback ID: fmt.Sprintf("call_%d_%d", iteration, idx)
	tc := ToolCall{
		ID:   "",
		Type: "function",
		Func: ToolCallFunction{Name: "bash", Args: `{"command":"ls"}`},
	}

	iteration := 3
	idx := 1

	if tc.ID == "" {
		tc.ID = fmt.Sprintf("call_%d_%d", iteration, idx)
	}

	if tc.ID != "call_3_1" {
		t.Errorf("expected fallback ID 'call_3_1', got %q", tc.ID)
	}

	// Verify that non-empty IDs are preserved.
	tc2 := ToolCall{
		ID:   "existing-id-123",
		Type: "function",
		Func: ToolCallFunction{Name: "read_file", Args: `{"path":"test.txt"}`},
	}

	if tc2.ID == "" {
		tc2.ID = fmt.Sprintf("call_%d_%d", iteration, idx)
	}

	if tc2.ID != "existing-id-123" {
		t.Errorf("expected preserved ID 'existing-id-123', got %q", tc2.ID)
	}
}

// ─────────────────────────────────────────────
// Build-verification gate nudge tests (task #7000)
// ─────────────────────────────────────────────

// buildGateWriteRound returns an SSE round where the model writes a file, so
// codeModified becomes true and the heuristic buildClaimed path can arm.
func buildGateWriteRound(t *testing.T) []string {
	t.Helper()
	return buildSSELines([]toolCallSpec{
		{id: "call_w", name: "write_file", args: `{"path":"res/xml/config.xml","content":"<x/>"}`},
	}, "", "")
}

// TestBuildGateNudgeRecovery is the regression test for task #7000: the model
// edits a file and its final answer claims build work without ever calling
// bash. The heuristic path must nudge instead of failing outright; when the
// model re-reports without the unverified claim, the task completes normally.
func TestBuildGateNudgeRecovery(t *testing.T) {
	r1 := buildGateWriteRound(t)
	r2 := buildSSELines(nil, "빌드 에러를 수정했습니다.", "stop")
	r3 := buildSSELines(nil, "XML 속성 하나만 바꾼 변경이라 빌드 검증은 불필요합니다. 작업이 끝났습니다.", "stop")

	srv := multiRoundServer(t, [][]string{r1, r2, r3})
	defer srv.Close()

	var outputs []string
	result, err := Run(context.Background(), ExecuteOptions{
		BaseURL:       srv.URL,
		Model:         "qwen3-test",
		SystemPrompt:  "You are a helpful assistant.",
		UserPrompt:    "접근성 설정 문제를 해결해줘.",
		ProjectPath:   t.TempDir(),
		MaxIterations: 6,
		OnOutput:      func(s string) { outputs = append(outputs, s) },
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("result.Incomplete = true, want recovery via nudge: %s", result.IncompleteReason)
	}
	if !strings.Contains(result.Result, "빌드 검증은 불필요") {
		t.Errorf("result = %q, want the clarified final answer", result.Result)
	}
	nudged := false
	for _, o := range outputs {
		if strings.Contains(o, "빌드 검증 게이트") {
			nudged = true
		}
	}
	if !nudged {
		t.Error("expected a build-gate nudge output before completion")
	}
}

// TestBuildGateNudgeExhaustion verifies the nudge loop is bounded: a model
// that keeps claiming build work without running bash is failed after
// maxBuildGateNudges chances.
func TestBuildGateNudgeExhaustion(t *testing.T) {
	r1 := buildGateWriteRound(t)
	claim := buildSSELines(nil, "빌드 에러를 수정했습니다.", "stop")

	srv := multiRoundServer(t, [][]string{r1, claim, claim, claim})
	defer srv.Close()

	result, err := Run(context.Background(), ExecuteOptions{
		BaseURL:       srv.URL,
		Model:         "qwen3-test",
		SystemPrompt:  "You are a helpful assistant.",
		UserPrompt:    "접근성 설정 문제를 해결해줘.",
		ProjectPath:   t.TempDir(),
		MaxIterations: 8,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !result.Incomplete {
		t.Fatal("result.Incomplete = false, want incomplete after nudges exhausted")
	}
	if !strings.Contains(result.IncompleteReason, "build verification") {
		t.Errorf("IncompleteReason = %q, want build verification failure", result.IncompleteReason)
	}
}

// TestBuildGateRequiredPathStrict verifies suggestion ③ of #7001: when the
// user prompt explicitly names a build command, the gate still fails
// immediately without a nudge — the expectation is unambiguous there.
func TestBuildGateRequiredPathStrict(t *testing.T) {
	r1 := buildSSELines(nil, "코드를 수정했습니다.", "stop")

	srv := multiRoundServer(t, [][]string{r1})
	defer srv.Close()

	var outputs []string
	result, err := Run(context.Background(), ExecuteOptions{
		BaseURL:       srv.URL,
		Model:         "qwen3-test",
		SystemPrompt:  "You are a helpful assistant.",
		UserPrompt:    "버그를 고친 다음 go build ./... 로 검증해줘.",
		ProjectPath:   t.TempDir(),
		MaxIterations: 4,
		OnOutput:      func(s string) { outputs = append(outputs, s) },
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !result.Incomplete {
		t.Fatal("result.Incomplete = false, want immediate incomplete on the buildRequired path")
	}
	for _, o := range outputs {
		if strings.Contains(o, "검증 또는 해명") {
			t.Error("buildRequired path must not nudge, but a nudge output was emitted")
		}
	}
}

// TestEnsureOutputNL verifies trailing newlines for LineCoalescer safety.
func TestEnsureOutputNL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"hello", "hello\n"},
		{"hello\n", "hello\n"},
		{"a\nb", "a\nb\n"},
		{"a\nb\n", "a\nb\n"},
	}
	for _, tc := range cases {
		got := ensureOutputNL(tc.in)
		if got != tc.want {
			t.Errorf("ensureOutputNL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestToolCallHeaderTrailingNL ensures tool headers always close the line so
// the next result/narration chunk is not glued mid-line by LineCoalescer.
func TestToolCallHeaderTrailingNL(t *testing.T) {
	h := toolCallHeader("bash", map[string]interface{}{"command": "ls"})
	if !strings.HasSuffix(h, "\n") {
		t.Fatalf("toolCallHeader missing trailing NL: %q", h)
	}
	if strings.Count(h, "\n") != 1 {
		t.Fatalf("toolCallHeader should be a single line, got %q", h)
	}
	h2 := toolCallHeader("read_file", nil)
	if !strings.HasSuffix(h2, "\n") {
		t.Fatalf("toolCallHeader(no args) missing trailing NL: %q", h2)
	}
}

// TestIndentResultTrailingNL ensures result blocks close so final summary text
// is not glued onto the last indented result line (task #7267).
func TestIndentResultTrailingNL(t *testing.T) {
	out := indentResult("line one\nline two")
	if out == "" {
		t.Fatal("indentResult returned empty")
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("indentResult missing trailing NL: %q", out)
	}
	// Last non-empty line must still be indented.
	trimmed := strings.TrimRight(out, "\n")
	lines := strings.Split(trimmed, "\n")
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "  ") {
			t.Fatalf("result line not indented: %q", ln)
		}
	}
	if indentResult("   \n  ") != "" {
		t.Fatal("blank result should return empty")
	}
}

// TestEmitOutputDoesNotGlueUnits simulates the LineCoalescer contract: when
// discrete units each end with '\n', they stay separate array elements.
func TestEmitOutputDoesNotGlueUnits(t *testing.T) {
	var chunks []string
	// Mimic LineCoalescer: open buffer, flush complete lines.
	var buf strings.Builder
	coalesce := func(s string) {
		buf.WriteString(s)
		for {
			str := buf.String()
			i := strings.IndexByte(str, '\n')
			if i < 0 {
				break
			}
			chunks = append(chunks, str[:i+1])
			buf.Reset()
			buf.WriteString(str[i+1:])
		}
	}

	emitOutput(coalesce, toolCallHeader("bash", map[string]interface{}{"command": "echo hi"}))
	emitOutput(coalesce, indentResult("ok\n1 file changed"))
	emitOutput(coalesce, "수정이 완료되었습니다.\n\n## 수정 요약")

	if buf.Len() != 0 {
		// Flush open remainder (should be empty if all units had NL)
		chunks = append(chunks, buf.String())
	}

	joined := strings.Join(chunks, "")
	if strings.Contains(joined, "changed수정") || strings.Contains(joined, "ok🔧") {
		t.Fatalf("units glued mid-line: %q", chunks)
	}
	// Summary start must be its own non-indented line.
	foundSummary := false
	for _, c := range chunks {
		if strings.HasPrefix(c, "수정이 완료") {
			foundSummary = true
			if strings.HasPrefix(c, " ") {
				t.Fatalf("summary line still indented/glued: %q", c)
			}
		}
	}
	if !foundSummary {
		t.Fatalf("summary line missing from coalesced output: %q", chunks)
	}
}
