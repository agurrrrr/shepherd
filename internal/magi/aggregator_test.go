package magi

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/agurrrrr/shepherd/internal/embedded"
)

// fakeAggregatorFunc is the signature of a single fake aggregatorComplete
// function.
type fakeAggregatorFunc func(ctx context.Context, spec AggregatorSpec, systemPrompt, userPrompt string) (string, embedded.ChatUsage, error)

// fakeAggregator replaces aggregatorComplete for testing. It supports
// call-sequence-based fakes so the first and second calls can return
// different outputs.
type fakeAggregator struct {
	mu       sync.Mutex
	calls    int
	funcs    []fakeAggregatorFunc // sequential: funcs[0] for 1st call, funcs[1] for 2nd
	received []string             // user prompts received
}

func (f *fakeAggregator) call(ctx context.Context, spec AggregatorSpec, systemPrompt, userPrompt string) (string, embedded.ChatUsage, error) {
	f.mu.Lock()
	f.calls++
	idx := f.calls - 1
	f.received = append(f.received, userPrompt)
	fn := f.funcs[idx]
	f.mu.Unlock()

	if fn == nil {
		return "", embedded.ChatUsage{}, errors.New("no fake for call")
	}
	return fn(ctx, spec, systemPrompt, userPrompt)
}

// withFakeAggregator swaps aggregatorComplete and returns a restore function.
func withFakeAggregator(fake *fakeAggregator) func() {
	orig := aggregatorComplete
	aggregatorComplete = fake.call
	return func() { aggregatorComplete = orig }
}

// validVerdictJSON is a well-formed verdict JSON string for tests.
const validVerdictJSON = `{
  "verdict": "majority",
  "agreement_axis": "두 심의자가 같은 방식을 제안함",
  "synthesis": "종합 답변입니다.",
  "dissent": "한 심의자가 다른 접근을 주장함",
  "confidence": 8
}`

// testResults creates three ProposerResult values for testing.
func testResults() []ProposerResult {
	return []ProposerResult{
		{
			Spec: ProposerSpec{
				Endpoint:   testEndpoint("ep1", "qwen3-27b"),
				PersonaKey: "melchior",
			},
			Answer:     "MELCHIOR의 답변입니다.",
			Confidence: 8,
		},
		{
			Spec: ProposerSpec{
				Endpoint:   testEndpoint("ep2", "llama-3.3-70b"),
				PersonaKey: "balthasar",
			},
			Answer:     "BALTHASAR의 답변입니다.",
			Confidence: 6,
		},
		{
			Spec: ProposerSpec{
				Endpoint:   testEndpoint("ep3", "mistral-small"),
				PersonaKey: "casper",
			},
			Answer:     "CASPER의 답변입니다.",
			Confidence: 9,
		},
	}
}

// TestJudge_FirstAttemptSuccess verifies that a valid JSON on the first
// call returns a Verdict with call count 1.
func TestJudge_FirstAttemptSuccess(t *testing.T) {
	fake := &fakeAggregator{
		funcs: []fakeAggregatorFunc{
			func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
				return validVerdictJSON, embedded.ChatUsage{PromptTokens: 100, CompletionTokens: 50}, nil
			},
		},
	}
	restore := withFakeAggregator(fake)
	defer restore()

	verdict, usage, calls, err := Judge(context.Background(), AggregatorSpec{Type: "endpoint"}, testResults(), "test task", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	if verdict == nil {
		t.Fatal("verdict is nil")
	}
	if verdict.Verdict != "majority" {
		t.Errorf("verdict = %q, want majority", verdict.Verdict)
	}
	if verdict.Confidence != 8 {
		t.Errorf("confidence = %d, want 8", verdict.Confidence)
	}
	if usage.PromptTokens != 100 {
		t.Errorf("usage.PromptTokens = %d, want 100", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Errorf("usage.CompletionTokens = %d, want 50", usage.CompletionTokens)
	}
}

// TestJudge_RetrySuccess verifies that a broken first output followed by a
// valid second output returns a Verdict with call count 2.
func TestJudge_RetrySuccess(t *testing.T) {
	fake := &fakeAggregator{
		funcs: []fakeAggregatorFunc{
			// First call: broken output.
			func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
				return "이것은 JSON이 아닙니다.", embedded.ChatUsage{PromptTokens: 100}, nil
			},
			// Second call (re-prompt): valid JSON.
			func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
				return validVerdictJSON, embedded.ChatUsage{PromptTokens: 200}, nil
			},
		},
	}
	restore := withFakeAggregator(fake)
	defer restore()

	var outputLines []string
	onOutput := func(s string) { outputLines = append(outputLines, s) }

	verdict, usage, calls, err := Judge(context.Background(), AggregatorSpec{Type: "endpoint"}, testResults(), "test task", onOutput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if verdict == nil {
		t.Fatal("verdict is nil")
	}
	if verdict.Verdict != "majority" {
		t.Errorf("verdict = %q, want majority", verdict.Verdict)
	}
	if usage.PromptTokens != 300 {
		t.Errorf("usage.PromptTokens = %d, want 300", usage.PromptTokens)
	}

	// Check that the re-prompt warning was emitted.
	found := false
	for _, line := range outputLines {
		if strings.Contains(line, "판정 JSON 파싱 실패") {
			found = true
			break
		}
	}
	if !found {
		t.Error("re-prompt warning not emitted in live output")
	}

	// Check that the re-prompt included the previous output.
	if len(fake.received) < 2 {
		t.Fatal("expected at least 2 received prompts")
	}
	if !strings.Contains(fake.received[1], "이것은 JSON이 아닙니다.") {
		t.Error("re-prompt should include the previous broken output")
	}
}

// TestJudge_BothFail verifies that two failed parse attempts return
// (nil, _, 2, nil) — no error.
func TestJudge_BothFail(t *testing.T) {
	fake := &fakeAggregator{
		funcs: []fakeAggregatorFunc{
			func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
				return "깨진 출력 1", embedded.ChatUsage{}, nil
			},
			func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
				return "깨진 출력 2", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeAggregator(fake)
	defer restore()

	verdict, _, calls, err := Judge(context.Background(), AggregatorSpec{Type: "endpoint"}, testResults(), "test task", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v (should be nil — parse failure is not an error)", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if verdict != nil {
		t.Errorf("verdict should be nil, got %+v", verdict)
	}
}

// TestJudge_BackendError verifies that a backend error is returned as an
// error (not swallowed).
func TestJudge_BackendError(t *testing.T) {
	fake := &fakeAggregator{
		funcs: []fakeAggregatorFunc{
			func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
				return "", embedded.ChatUsage{}, errors.New("connection refused")
			},
		},
	}
	restore := withFakeAggregator(fake)
	defer restore()

	_, _, calls, err := Judge(context.Background(), AggregatorSpec{Type: "endpoint"}, testResults(), "test task", nil)
	if err == nil {
		t.Fatal("expected error for backend failure")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should contain 'connection refused', got %v", err)
	}
}

// TestBuildJudgePrompt_IdentityMasking verifies that the prompt contains
// persona display names but NOT model names or endpoint IDs.
func TestBuildJudgePrompt_IdentityMasking(t *testing.T) {
	results := testResults()
	prompt := BuildJudgePrompt(results, "test task prompt")

	// Persona display names should be present.
	expectedNames := []string{"MELCHIOR-1", "BALTHASAR-2", "CASPER-3"}
	for _, name := range expectedNames {
		if !strings.Contains(prompt, name) {
			t.Errorf("prompt should contain persona name %q", name)
		}
	}

	// Model names should NOT be present.
	modelNames := []string{"qwen3-27b", "llama-3.3-70b", "mistral-small"}
	for _, model := range modelNames {
		if strings.Contains(prompt, model) {
			t.Errorf("prompt should NOT contain model name %q (identity masking)", model)
		}
	}

	// Endpoint IDs should NOT be present.
	endpointIDs := []string{"ep1", "ep2", "ep3"}
	for _, id := range endpointIDs {
		if strings.Contains(prompt, id) {
			t.Errorf("prompt should NOT contain endpoint ID %q (identity masking)", id)
		}
	}

	// Task prompt should be present.
	if !strings.Contains(prompt, "test task prompt") {
		t.Error("prompt should contain the task prompt")
	}

	// Bias suppression instruction should be present.
	if !strings.Contains(prompt, "길이가 아니라 근거의 질") {
		t.Error("prompt should contain bias suppression instruction")
	}

	// JSON schema instruction should be present.
	if !strings.Contains(prompt, "JSON 객체 하나만 출력하라") {
		t.Error("prompt should instruct JSON-only output")
	}
}

// TestBuildJudgePrompt_AllAnswersPresent verifies that all three answers
// are present in the prompt regardless of shuffle order.
func TestBuildJudgePrompt_AllAnswersPresent(t *testing.T) {
	results := testResults()
	prompt := BuildJudgePrompt(results, "test task")

	for _, r := range results {
		if !strings.Contains(prompt, r.Answer) {
			t.Errorf("prompt should contain answer %q", r.Answer)
		}
	}
}

// TestSideBySideFallback verifies that the fallback output contains the
// warning header and all three answers with persona names.
func TestSideBySideFallback(t *testing.T) {
	results := testResults()
	output := SideBySideFallback(results)

	// Warning header.
	if !strings.Contains(output, "⚠️ MAGI 판정 실패") {
		t.Error("fallback should contain warning header")
	}

	// All three persona names.
	expectedNames := []string{"MELCHIOR-1", "BALTHASAR-2", "CASPER-3"}
	for _, name := range expectedNames {
		if !strings.Contains(output, name) {
			t.Errorf("fallback should contain persona name %q", name)
		}
	}

	// All three answers.
	for _, r := range results {
		if !strings.Contains(output, r.Answer) {
			t.Errorf("fallback should contain answer %q", r.Answer)
		}
	}
}

// TestSideBySideFallback_SkipsFailed verifies that failed proposers are
// skipped in the fallback output.
func TestSideBySideFallback_SkipsFailed(t *testing.T) {
	results := testResults()
	results[1].Err = errors.New("timeout")

	output := SideBySideFallback(results)

	if strings.Contains(output, results[1].Answer) {
		t.Error("fallback should not contain answer from failed proposer")
	}
	if !strings.Contains(output, results[0].Answer) {
		t.Error("fallback should contain answer from successful proposer 0")
	}
	if !strings.Contains(output, results[2].Answer) {
		t.Error("fallback should contain answer from successful proposer 2")
	}
}

// TestBuildJudgePrompt_ConfidenceDisplay verifies that confidence is shown
// in the prompt header.
func TestBuildJudgePrompt_ConfidenceDisplay(t *testing.T) {
	results := testResults()
	prompt := BuildJudgePrompt(results, "test")

	if !strings.Contains(prompt, "신뢰도 8/10") {
		t.Error("prompt should contain MELCHIOR's confidence 8/10")
	}
	if !strings.Contains(prompt, "신뢰도 6/10") {
		t.Error("prompt should contain BALTHASAR's confidence 6/10")
	}
	if !strings.Contains(prompt, "신뢰도 9/10") {
		t.Error("prompt should contain CASPER's confidence 9/10")
	}
}

// TestBuildJudgePrompt_ConfidenceUnreported verifies that unreported
// confidence (-1) is displayed as "신뢰도 미보고".
func TestBuildJudgePrompt_ConfidenceUnreported(t *testing.T) {
	results := testResults()
	results[0].Confidence = -1

	prompt := BuildJudgePrompt(results, "test")

	if !strings.Contains(prompt, "신뢰도 미보고") {
		t.Error("prompt should show '신뢰도 미보고' for unreported confidence")
	}
}

// TestBuildJudgePrompt_TaskTruncation verifies that a very long task prompt
// is truncated.
func TestBuildJudgePrompt_TaskTruncation(t *testing.T) {
	longTask := strings.Repeat("가", 5000)
	prompt := BuildJudgePrompt(testResults(), longTask)

	if !strings.Contains(prompt, "[truncated]") {
		t.Error("long task prompt should be truncated")
	}
}

// TestBuildJudgePrompt_AnswerTruncation verifies that a very long answer
// is truncated.
func TestBuildJudgePrompt_AnswerTruncation(t *testing.T) {
	results := testResults()
	results[0].Answer = strings.Repeat("나", 13000)

	prompt := BuildJudgePrompt(results, "test")

	if !strings.Contains(prompt, "[truncated]") {
		t.Error("long answer should be truncated")
	}
}

// TestBuildJudgePrompt_AbstainedInstruction verifies that the judge prompt
// instructs the model to populate the "abstained" array (step-10).
func TestBuildJudgePrompt_AbstainedInstruction(t *testing.T) {
	prompt := BuildJudgePrompt(testResults(), "test task")

	// The JSON schema should mention the abstained field.
	if !strings.Contains(prompt, `"abstained"`) {
		t.Error("prompt should contain \"abstained\" in the JSON schema")
	}

	// The abstention rule section should instruct the model to record names.
	if !strings.Contains(prompt, "abstained") {
		t.Error("prompt should contain abstained instruction")
	}
	if !strings.Contains(prompt, "기권 처리한 심의자의 이름") {
		t.Error("prompt should instruct recording abstained names")
	}
}
