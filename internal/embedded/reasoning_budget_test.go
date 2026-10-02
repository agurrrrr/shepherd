package embedded

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Issue #347: reasoning-only turns cut at max_tokens (12,288) were handed off
// as if the context had overflowed, and every follow-up stalled at the same
// point. These tests cover the opt-in reasoning budget, the thinking-off
// handoff summary request and the bounded thinking-truncation nudge.

const (
	budgetKey      = `"reasoning_budget_tokens"`
	templateKwargs = `"chat_template_kwargs"`
	thinkingOffKV  = `"chat_template_kwargs":{"enable_thinking":false}`
)

// handoffSummaryRound is a summary response that passes the handoff quality
// gate and carries a follow-up prompt.
func handoffSummaryRound() []string {
	return buildSSELines(nil, healthyHandoffSummary()+"\n"+handoffMarker+"\n남은 구현을 이어서 진행한다.", "stop")
}

// thinkingOnlyLengthRound is a turn that spent the whole output cap thinking.
func thinkingOnlyLengthRound() []string {
	return buildSSELinesWithReasoning("전체 구현을 설계해 보자. 먼저 구조를 정하고…", "", "length")
}

func writeFileRound(id string) []string {
	return buildSSELines([]toolCallSpec{
		{id: id, name: "write_file", args: `{"path":"step.txt","content":"ok"}`},
	}, "", "")
}

func TestRequestMaxTokensAndReasoningBudget(t *testing.T) {
	cases := []struct {
		ctx, configured, wantMax, wantBudget int
	}{
		{100000, 6144, 12288, 6144},
		{16384, 6144, 4096, 2048},
		{100000, 20000, 12288, 6144},
		{32768, 0, 8192, 0},
		{32768, -5, 8192, 0},
	}
	for _, c := range cases {
		maxTok := requestMaxTokens(c.ctx)
		if maxTok != c.wantMax {
			t.Errorf("requestMaxTokens(%d) = %d, want %d", c.ctx, maxTok, c.wantMax)
		}
		if got := requestReasoningBudget(c.configured, maxTok); got != c.wantBudget {
			t.Errorf("requestReasoningBudget(%d, %d) = %d, want %d", c.configured, maxTok, got, c.wantBudget)
		}
	}
}

// TestReasoningBudgetSentClampedToHalfMaxTokens checks the agent-loop request
// body carries min(configured, max_tokens/2).
func TestReasoningBudgetSentClampedToHalfMaxTokens(t *testing.T) {
	cases := []struct {
		ctx        int
		wantMax    string
		wantBudget string
	}{
		{100000, `"max_tokens":12288`, `"reasoning_budget_tokens":6144`},
		{16384, `"max_tokens":4096`, `"reasoning_budget_tokens":2048`},
	}
	for _, c := range cases {
		srv, bodies := multiRoundSSEServer(t, [][]string{buildSSELines(nil, "확인했습니다.", "stop")})
		res, err := Run(context.Background(), ExecuteOptions{
			BaseURL:               srv.URL + "/chat/completions",
			Model:                 "qwen3-test",
			SystemPrompt:          "sys",
			UserPrompt:            "상태를 알려주세요.",
			MaxIterations:         2,
			ContextTokens:         c.ctx,
			ReasoningBudgetTokens: 6144,
		})
		srv.Close()
		if err != nil || res.Incomplete {
			t.Fatalf("ctx=%d: run failed: err=%v res=%+v", c.ctx, err, res)
		}
		body := (*bodies)[0]
		if !strings.Contains(body, c.wantMax) || !strings.Contains(body, c.wantBudget) {
			t.Errorf("ctx=%d: body missing %s / %s: %s", c.ctx, c.wantMax, c.wantBudget, truncateForTest(body, 600))
		}
		if strings.Contains(body, templateKwargs) {
			t.Errorf("ctx=%d: main loop request must not carry chat_template_kwargs", c.ctx)
		}
	}
}

// TestReasoningFieldsOmittedByDefault: endpoints that never opted in must send
// exactly the old body — unknown fields may be rejected by other servers.
func TestReasoningFieldsOmittedByDefault(t *testing.T) {
	srv, bodies := multiRoundSSEServer(t, [][]string{buildSSELines(nil, "확인했습니다.", "stop")})
	defer srv.Close()
	if _, err := Run(context.Background(), ExecuteOptions{
		BaseURL:       srv.URL + "/chat/completions",
		Model:         "qwen3-test",
		SystemPrompt:  "sys",
		UserPrompt:    "상태를 알려주세요.",
		MaxIterations: 2,
		ContextTokens: 100000,
	}); err != nil {
		t.Fatal(err)
	}
	body := (*bodies)[0]
	if strings.Contains(body, budgetKey) || strings.Contains(body, templateKwargs) {
		t.Fatalf("default request carries opt-in fields: %s", truncateForTest(body, 600))
	}

	// Byte-level: a ChatRequest with zero new fields marshals exactly like the
	// pre-#347 struct.
	type legacyChatRequest struct {
		Model            string                 `json:"model"`
		Messages         []ChatMessage          `json:"messages"`
		Tools            []OpenAIToolDef        `json:"tools,omitempty"`
		ToolChoice       interface{}            `json:"tool_choice,omitempty"`
		Temperature      float32                `json:"temperature,omitempty"`
		FrequencyPenalty float32                `json:"frequency_penalty,omitempty"`
		PresencePenalty  float32                `json:"presence_penalty,omitempty"`
		MaxTokens        int                    `json:"max_tokens,omitempty"`
		Stream           bool                   `json:"stream"`
		StreamOptions    *StreamOptions         `json:"stream_options,omitempty"`
		Options          map[string]interface{} `json:"options,omitempty"`
	}
	msgs := []ChatMessage{{Role: ChatRoleSystem, Content: "sys"}, {Role: ChatRoleUser, Content: "hi"}}
	cur, _ := json.Marshal(&ChatRequest{Model: "m", Messages: msgs, ToolChoice: "auto", Temperature: 0.7,
		FrequencyPenalty: 0.3, PresencePenalty: 0.3, MaxTokens: 12288, Stream: true, StreamOptions: &StreamOptions{IncludeUsage: true}})
	old, _ := json.Marshal(&legacyChatRequest{Model: "m", Messages: msgs, ToolChoice: "auto", Temperature: 0.7,
		FrequencyPenalty: 0.3, PresencePenalty: 0.3, MaxTokens: 12288, Stream: true, StreamOptions: &StreamOptions{IncludeUsage: true}})
	if string(cur) != string(old) {
		t.Fatalf("default body changed:\n new=%s\n old=%s", cur, old)
	}
}

// TestHandoffSummaryRequestThinkingOff: with HandoffNoThinking only the
// summary request disables thinking (and drops the budget); the main loop
// request keeps the budget and no template kwargs.
func TestHandoffSummaryRequestThinkingOff(t *testing.T) {
	// Round 1: empty content, no reasoning, finish=length → handoff path.
	srv, bodies := multiRoundSSEServer(t, [][]string{
		buildSSELines(nil, "", "length"),
		handoffSummaryRound(),
	})
	defer srv.Close()

	var followUps []string
	res, err := Run(context.Background(), ExecuteOptions{
		BaseURL:               srv.URL + "/chat/completions",
		Model:                 "qwen3-test",
		SystemPrompt:          "sys",
		UserPrompt:            "기능을 구현해주세요.",
		MaxIterations:         4,
		ContextTokens:         100000,
		ReasoningBudgetTokens: 6144,
		HandoffNoThinking:     true,
		EnqueueFollowUp:       func(p string) error { followUps = append(followUps, p); return nil },
	})
	if err != nil || res.Incomplete {
		t.Fatalf("run failed: err=%v res=%+v", err, res)
	}
	if len(followUps) != 1 || len(*bodies) != 2 {
		t.Fatalf("want 1 follow-up over 2 requests, got followUps=%d requests=%d", len(followUps), len(*bodies))
	}
	main, summary := (*bodies)[0], (*bodies)[1]
	if strings.Contains(main, templateKwargs) || !strings.Contains(main, `"reasoning_budget_tokens":6144`) {
		t.Errorf("main request: want budget, no kwargs: %s", truncateForTest(main, 600))
	}
	if !strings.Contains(summary, thinkingOffKV) {
		t.Errorf("summary request missing %s", thinkingOffKV)
	}
	if strings.Contains(summary, budgetKey) {
		t.Errorf("summary request with thinking off must not carry a reasoning budget")
	}
}

// TestHandoffSummaryRequestKeepsBudget: without HandoffNoThinking the summary
// request still gets the budget so its reasoning cannot eat the cap (#10023).
func TestHandoffSummaryRequestKeepsBudget(t *testing.T) {
	srv, bodies := multiRoundSSEServer(t, [][]string{
		buildSSELines(nil, "", "length"),
		handoffSummaryRound(),
	})
	defer srv.Close()

	res, err := Run(context.Background(), ExecuteOptions{
		BaseURL:               srv.URL + "/chat/completions",
		Model:                 "qwen3-test",
		SystemPrompt:          "sys",
		UserPrompt:            "기능을 구현해주세요.",
		MaxIterations:         4,
		ContextTokens:         100000,
		ReasoningBudgetTokens: 6144,
		EnqueueFollowUp:       func(string) error { return nil },
	})
	if err != nil || res.Incomplete {
		t.Fatalf("run failed: err=%v res=%+v", err, res)
	}
	summary := (*bodies)[1]
	if !strings.Contains(summary, `"reasoning_budget_tokens":6144`) || strings.Contains(summary, templateKwargs) {
		t.Errorf("summary request: want budget, no kwargs: %s", truncateForTest(summary, 600))
	}
}

func countNudges(t *testing.T, body string) int {
	t.Helper()
	n := 0
	for _, c := range lastUserContents(t, body) {
		if c == systemReminder(thinkingTruncatedNudgeBody) {
			n++
		}
	}
	return n
}

// TestThinkingTruncatedTurnIsNudgedNotHandedOff: a reasoning-only length turn
// continues the same conversation with a nudge instead of a handoff.
func TestThinkingTruncatedTurnIsNudgedNotHandedOff(t *testing.T) {
	srv, bodies := multiRoundSSEServer(t, [][]string{
		thinkingOnlyLengthRound(),
		writeFileRound("call_1"),
		buildSSELines(nil, "step.txt 파일을 작성했습니다.", "stop"),
	})
	defer srv.Close()

	var followUps int
	var out strings.Builder
	res, err := Run(context.Background(), ExecuteOptions{
		BaseURL:         srv.URL + "/chat/completions",
		Model:           "qwen3-test",
		ProjectPath:     t.TempDir(),
		SystemPrompt:    "sys",
		UserPrompt:      "기능을 구현해주세요.",
		MaxIterations:   6,
		ContextTokens:   100000,
		EnqueueFollowUp: func(string) error { followUps++; return nil },
		OnOutput:        func(s string) { out.WriteString(s) },
	})
	if err != nil || res.Incomplete {
		t.Fatalf("run failed: err=%v res=%+v", err, res)
	}
	if followUps != 0 {
		t.Fatalf("thinking truncation must not hand off; follow-ups=%d", followUps)
	}
	if len(*bodies) != 3 {
		t.Fatalf("want 3 requests, got %d", len(*bodies))
	}
	if n := countNudges(t, (*bodies)[1]); n != 1 {
		t.Errorf("2nd request should carry one nudge, got %d", n)
	}
	if strings.Contains((*bodies)[1], "전체 구현을 설계해 보자") {
		t.Errorf("truncated reasoning must not enter history")
	}
	if !strings.Contains(out.String(), "⚠️ [추론 길이 초과]") {
		t.Errorf("missing nudge notice in live output")
	}
}

// TestThinkingTruncatedNudgeCapFallsBackToHandoff: after maxThinkingTruncNudges
// nudges the existing handoff path runs.
func TestThinkingTruncatedNudgeCapFallsBackToHandoff(t *testing.T) {
	srv, bodies := multiRoundSSEServer(t, [][]string{
		thinkingOnlyLengthRound(),
		thinkingOnlyLengthRound(),
		thinkingOnlyLengthRound(),
		handoffSummaryRound(),
	})
	defer srv.Close()

	var followUps int
	res, err := Run(context.Background(), ExecuteOptions{
		BaseURL:           srv.URL + "/chat/completions",
		Model:             "qwen3-test",
		SystemPrompt:      "sys",
		UserPrompt:        "기능을 구현해주세요.",
		MaxIterations:     8,
		ContextTokens:     100000,
		HandoffNoThinking: true,
		EnqueueFollowUp:   func(string) error { followUps++; return nil },
	})
	if err != nil || res.Incomplete {
		t.Fatalf("run failed: err=%v res=%+v", err, res)
	}
	if followUps != 1 || len(*bodies) != 4 {
		t.Fatalf("want handoff after %d nudges: follow-ups=%d requests=%d", maxThinkingTruncNudges, followUps, len(*bodies))
	}
	// Repeated truncations don't stack identical nudges.
	if n := countNudges(t, (*bodies)[2]); n != 1 {
		t.Errorf("3rd request should carry exactly one nudge, got %d", n)
	}
	if !strings.Contains((*bodies)[3], thinkingOffKV) {
		t.Errorf("4th request should be the thinking-off handoff summary")
	}
}

// TestThinkingTruncatedNudgeCapIncompleteWithoutHandoff: no handoff available
// → the old "response truncated" incomplete result after the nudges.
func TestThinkingTruncatedNudgeCapIncompleteWithoutHandoff(t *testing.T) {
	srv, bodies := multiRoundSSEServer(t, [][]string{
		thinkingOnlyLengthRound(),
		thinkingOnlyLengthRound(),
		thinkingOnlyLengthRound(),
	})
	defer srv.Close()

	res, err := Run(context.Background(), ExecuteOptions{
		BaseURL:       srv.URL + "/chat/completions",
		Model:         "qwen3-test",
		SystemPrompt:  "sys",
		UserPrompt:    "기능을 구현해주세요.",
		MaxIterations: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Incomplete || res.IncompleteReason != "response truncated (max tokens reached)" || len(*bodies) != 3 {
		t.Fatalf("want truncated incomplete after 3 requests, got %+v (requests=%d)", res, len(*bodies))
	}
}

// TestThinkingTruncatedNudgeResetsAfterToolCall: a turn that runs tools
// restores the nudge budget for later truncations.
func TestThinkingTruncatedNudgeResetsAfterToolCall(t *testing.T) {
	srv, bodies := multiRoundSSEServer(t, [][]string{
		thinkingOnlyLengthRound(),
		thinkingOnlyLengthRound(),
		writeFileRound("call_1"),
		thinkingOnlyLengthRound(),
		thinkingOnlyLengthRound(),
		writeFileRound("call_2"),
		buildSSELines(nil, "두 단계 모두 작성했습니다.", "stop"),
	})
	defer srv.Close()

	var followUps int
	res, err := Run(context.Background(), ExecuteOptions{
		BaseURL:         srv.URL + "/chat/completions",
		Model:           "qwen3-test",
		ProjectPath:     t.TempDir(),
		SystemPrompt:    "sys",
		UserPrompt:      "기능을 구현해주세요.",
		MaxIterations:   10,
		ContextTokens:   100000,
		EnqueueFollowUp: func(string) error { followUps++; return nil },
	})
	if err != nil || res.Incomplete {
		t.Fatalf("run failed: err=%v res=%+v", err, res)
	}
	if followUps != 0 || len(*bodies) != 7 {
		t.Fatalf("want no handoff over 7 requests, got follow-ups=%d requests=%d", followUps, len(*bodies))
	}
}
