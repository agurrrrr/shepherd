package embedded

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #348: est_tokens ran 14–25% (production: ~2× at iteration 0) below the
// server's prompt_tokens because tool definitions were not counted and the
// text heuristic undercounted code and machine output. These tests cover tool
// definitions in the estimate and the usage.prompt_tokens calibration.

func TestEstimateTextTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"안녕하세요", 4},  // 5 × 0.65 → 3.25
		{"12345", 7},  // 5 × 1.4
		{"abcdef", 1}, // 6 × 1/6
		{"a  b", 1},   // 2/6 + one space run (1/3 + 2×0.05) → 0.77
		{"→", 1},
	}
	for _, c := range cases {
		if got := estimateTextTokens(c.in); got != c.want {
			t.Errorf("estimateTextTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}

	// Code must not fall back to the old 4-ASCII-chars-per-token undercount.
	code := "func main() {\n\tfor i := 0; i < 10; i++ {\n\t\tfmt.Println(i)\n\t}\n}\n"
	if got, old := estimateTextTokens(code), len(code)/4; got <= old {
		t.Errorf("code estimate %d should exceed the old ascii/4 estimate %d", got, old)
	}
}

func testToolDefs(n int) []OpenAIToolDef {
	defs := make([]OpenAIToolDef, n)
	for i := range defs {
		defs[i] = OpenAIToolDef{Type: "function", Function: OpenAIFunction{
			Name:        fmt.Sprintf("tool_%d", i),
			Description: "Read a file from the project and return its contents with line numbers.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "file path"},
				},
				"required": []string{"path"},
			},
		}}
	}
	return defs
}

func TestEstimateToolDefsTokens(t *testing.T) {
	if got := estimateToolDefsTokens(nil); got != 0 {
		t.Fatalf("no tools: got %d, want 0", got)
	}
	one, ten := estimateToolDefsTokens(testToolDefs(1)), estimateToolDefsTokens(testToolDefs(10))
	if one <= toolDefsHeaderTokens || ten <= one {
		t.Fatalf("estimate should grow with tools: header=%d one=%d ten=%d", toolDefsHeaderTokens, one, ten)
	}

	// "<" must be counted as one character, not as a 6-char unicode escape.
	withDesc := func(d string) []OpenAIToolDef {
		defs := testToolDefs(1)
		defs[0].Function.Description = d
		return defs
	}
	if a, b := estimateToolDefsTokens(withDesc("<tools> & <x>")), estimateToolDefsTokens(withDesc("[tools] + [x]")); a != b {
		t.Errorf("HTML escaping inflated the estimate: %d vs %d", a, b)
	}
}

// historyOf returns system + user + n tool turns (assistant call + result).
func historyOf(n int, result string) []ChatMessage {
	msgs := []ChatMessage{
		{Role: ChatRoleSystem, Content: "You are an agent."},
		{Role: ChatRoleUser, Content: "Fix the bug."},
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs,
			ChatMessage{Role: ChatRoleAssistant, ToolCalls: []ToolCall{{ID: id, Type: "function",
				Func: ToolCallFunction{Name: "read_file", Args: `{"path":"main.go"}`}}}},
			ChatMessage{Role: ChatRoleTool, ToolCallID: id, Content: result},
		)
	}
	return msgs
}

func TestTrimMessagesCountsToolDefs(t *testing.T) {
	msgs := historyOf(6, strings.Repeat("line of code\n", 40))
	defs := testToolDefs(30)
	msgTokens, _ := newPromptEstimator(nil).estimate(msgs)
	withTools, _ := newPromptEstimator(defs).estimate(msgs)
	if withTools-msgTokens != estimateToolDefsTokens(defs) {
		t.Fatalf("tool defs not added: messages=%d with tools=%d defs=%d", msgTokens, withTools, estimateToolDefsTokens(defs))
	}

	// A window where the messages alone fit under 75% but messages + tool
	// definitions do not: only the estimator that knows the tools trims.
	ctx := (msgTokens + withTools) / 2 * 4 / 3
	if got := trimMessages(msgs, ctx, newPromptEstimator(nil)); len(got) != len(msgs) {
		t.Fatalf("without tool defs: trimmed %d → %d, want no trim", len(msgs), len(got))
	}
	got := trimMessages(msgs, ctx, newPromptEstimator(defs))
	if len(got) >= len(msgs) {
		t.Fatalf("with tool defs: got %d messages, want fewer than %d", len(got), len(msgs))
	}
	if got[0].Role != ChatRoleSystem || got[1].Role != ChatRoleUser {
		t.Fatalf("system/user must be preserved, got roles %s/%s", got[0].Role, got[1].Role)
	}
}

func TestPromptEstimatorAnchorsToReportedUsage(t *testing.T) {
	e := newPromptEstimator(testToolDefs(3))
	msgs := historyOf(2, "package main")
	est0, heur0 := e.estimate(msgs)
	if est0 != heur0 {
		t.Fatalf("before any usage the estimate is the heuristic: est=%d heur=%d", est0, heur0)
	}

	actual := heur0 * 3 / 2 // the server counts 50% more than the heuristic
	e.observe(len(msgs), heur0, int64(actual))
	if est, _ := e.estimate(msgs); est != actual {
		t.Fatalf("same request: est=%d, want reported %d", est, actual)
	}

	grown := historyOf(4, "package main")
	est, heur := e.estimate(grown)
	want := actual + scaleTokens(heur-heur0, float64(actual)/float64(heur0))
	if est != want {
		t.Fatalf("appended turns: est=%d, want %d (actual %d + scaled delta of %d)", est, want, actual, heur-heur0)
	}
}

func TestPromptEstimatorDeltaNotBelowHeuristic(t *testing.T) {
	e := newPromptEstimator(nil)
	msgs := historyOf(2, "package main")
	_, heur0 := e.estimate(msgs)
	actual := heur0 * 4 / 5 // the heuristic overcounts this tokenizer
	e.observe(len(msgs), heur0, int64(actual))

	est, heur := e.estimate(historyOf(3, "package main"))
	if want := actual + (heur - heur0); est != want {
		t.Fatalf("est=%d, want %d: appended messages must count at full heuristic size", est, want)
	}
}

func TestPromptEstimatorIgnoresImplausibleUsage(t *testing.T) {
	e := newPromptEstimator(nil)
	msgs := historyOf(3, strings.Repeat("x := 1\n", 50))
	_, heur := e.estimate(msgs)
	// Ollama-style "uncached prompt only" report and a missing usage block.
	e.observe(len(msgs), heur, int64(heur/3))
	e.observe(len(msgs), heur, 0)
	if est, h := e.estimate(msgs); est != h {
		t.Fatalf("implausible usage must be ignored: est=%d heur=%d", est, h)
	}
}

func TestPromptEstimatorAfterTrimUsesRatio(t *testing.T) {
	e := newPromptEstimator(testToolDefs(2))
	msgs := historyOf(5, "package main\n\nfunc f() {}\n")
	_, heur := e.estimate(msgs)
	e.observe(len(msgs), heur, int64(heur*2))

	// Drop the two oldest turns the way trimMessages does.
	trimmed := append(append([]ChatMessage{}, msgs[:2]...), msgs[6:]...)
	est, h := e.estimate(trimmed)
	if want := scaleTokens(h, 2); est != want {
		t.Fatalf("after trim: est=%d, want heuristic %d × ratio 2 = %d", est, h, want)
	}
}

func TestTrimMessagesUsesReportedUsage(t *testing.T) {
	msgs := historyOf(6, strings.Repeat("line of code\n", 40))
	e := newPromptEstimator(nil)
	_, heur := e.estimate(msgs[:len(msgs)-2])
	ctx := heur * 2 // heuristic alone stays far under 75%

	if got := trimMessages(msgs, ctx, e); len(got) != len(msgs) {
		t.Fatalf("heuristic only: trimmed %d → %d, want no trim", len(msgs), len(got))
	}
	// The previous request (all but the last turn) was reported at 90% of the window.
	e.observe(len(msgs)-2, heur, int64(ctx*9/10))
	got := trimMessages(msgs, ctx, e)
	if len(got) >= len(msgs) {
		t.Fatalf("reported usage over the limit: got %d messages, want fewer than %d", len(got), len(msgs))
	}
	if est, _ := e.estimate(got); est > ctx*3/4 {
		t.Fatalf("trimmed history still estimated at %d > limit %d", est, ctx*3/4)
	}
}

// withPromptUsage appends an OpenAI usage chunk (choices: []) before [DONE].
func withPromptUsage(lines []string, promptTokens int) []string {
	usage, _ := json.Marshal(map[string]interface{}{
		"choices": []interface{}{},
		"usage":   map[string]int{"prompt_tokens": promptTokens, "completion_tokens": 1, "total_tokens": promptTokens + 1},
	})
	out := append([]string{}, lines[:len(lines)-1]...)
	return append(out, "data: "+string(usage), lines[len(lines)-1])
}

func requestMessageCount(t *testing.T, body string) int {
	t.Helper()
	var req struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return len(req.Messages)
}

// TestRunTrimsOnReportedPromptTokens drives the real loop: the first response
// reports a prompt size near the context window. The next request must be
// trimmed even though the heuristic alone is far below the limit; without a
// usage report the same history goes out untrimmed.
func TestRunTrimsOnReportedPromptTokens(t *testing.T) {
	const ctxTokens = 40000
	for _, tc := range []struct {
		name     string
		usage    int // prompt_tokens reported for the first request; 0 = none
		wantMsgs int // messages in the second request
	}{
		{"usage near window trims", 32000, 2},
		{"no usage keeps history", 0, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			r1 := buildSSELines([]toolCallSpec{{id: "call_1", name: "read_file", args: `{"path":"main.go"}`}}, "", "")
			if tc.usage > 0 {
				r1 = withPromptUsage(r1, tc.usage)
			}
			r2 := buildSSELines(nil, "main.go를 확인했습니다.", "stop")
			srv, bodies := multiRoundSSEServer(t, [][]string{r1, r2})
			defer srv.Close()

			result, err := Run(context.Background(), ExecuteOptions{
				BaseURL:       srv.URL + "/chat/completions",
				Model:         "qwen3-test",
				SystemPrompt:  "You are an agent.",
				UserPrompt:    "main.go 내용을 확인해 주세요.",
				ProjectPath:   dir,
				ContextTokens: ctxTokens,
				MaxIterations: 4,
			})
			if err != nil {
				t.Fatalf("Run error: %v", err)
			}
			if result.Incomplete {
				t.Fatalf("Incomplete: %s", result.IncompleteReason)
			}
			if len(*bodies) < 2 {
				t.Fatalf("expected 2 requests, got %d", len(*bodies))
			}
			if got := requestMessageCount(t, (*bodies)[1]); got != tc.wantMsgs {
				t.Fatalf("second request carried %d messages, want %d", got, tc.wantMsgs)
			}
		})
	}
}

// Task #10136: a vision task read four screenshots; est_tokens climbed to
// 147,214 while the server reported 15,007, because each picture was counted
// at len(base64)/4 and the first image report fell under half the heuristic,
// so usage calibration switched off for the rest of the run.
func TestPromptEstimatorKeepsAnchorWithImages(t *testing.T) {
	const realImageTokens = 882 // Strata, 1280×688 screenshot
	img := optimizeImageForContext(makeTestImage(1280, 688), "image/png")
	e := newPromptEstimator(testToolDefs(20))
	msgs := historyOf(1, "ok")
	_, heur := e.estimate(msgs)
	actual := heur
	e.observe(len(msgs), heur, int64(actual))

	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("img_%d", i)
		msgs = append(msgs,
			ChatMessage{Role: ChatRoleAssistant, ToolCalls: []ToolCall{{ID: id, Type: "function",
				Func: ToolCallFunction{Name: "read_file", Args: `{"path":"shot.png"}`}}}},
			ChatMessage{Role: ChatRoleTool, ToolCallID: id, Content: "Image loaded."},
			ChatMessage{Role: ChatRoleUser, ContentParts: []ContentPart{
				{Type: "text", Text: "Attached image(s) from the tool call(s) above:"},
				{Type: "image_url", ImageURL: &ImageURL{URL: img}},
			}},
		)
		est, h := e.estimate(msgs)
		appended := h - heur
		actual += appended - EstimateImageTokens(img) + realImageTokens
		if est > actual*3/2 {
			t.Fatalf("after image %d: est=%d is more than 1.5× the real %d", i+1, est, actual)
		}
		e.observe(len(msgs), h, int64(actual))
		if e.anchorActual != actual {
			t.Fatalf("after image %d: usage %d was not anchored (heur %d)", i+1, actual, h)
		}
		heur = h
	}
}

func TestEstimateMessageTokensContentPartsReplaceContent(t *testing.T) {
	text := strings.Repeat("화면에 무엇이 보이는지 설명해 주세요. ", 20)
	img := optimizeImageForContext(makeTestImage(64, 64), "image/png")
	// extractAttachedImages keeps the prompt in Content and in the first text
	// part; only the parts are sent, so the text must count once.
	withBoth := ChatMessage{Role: ChatRoleUser, Content: text, ContentParts: []ContentPart{
		{Type: "text", Text: text},
		{Type: "image_url", ImageURL: &ImageURL{URL: img}},
	}}
	want := estimateTextTokens(text) + EstimateImageTokens(img) + msgOverheadTokens
	if got := estimateMessageTokens(withBoth); got != want {
		t.Fatalf("got %d, want %d: Content must not be counted next to ContentParts", got, want)
	}
}
