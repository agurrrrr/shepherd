package magi

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
)

// testEndpoint creates an EndpointRef for testing.
func testEndpoint(id, model string) EndpointRef {
	return EndpointRef{
		ID:            id,
		BaseURL:       "http://localhost:9999",
		APIKey:        "test-key",
		Model:         model,
		ContextTokens: 32768,
	}
}

// fakeFunc is the signature of a single fake callEndpoint function.
type fakeFunc func(ctx context.Context, ep EndpointRef, systemPrompt, userPrompt string, temperature float32, maxTokens int, onToken func(string), tools []embedded.OpenAIToolDef, dispatch embedded.MCPDispatcher, projectPath, sheepName string) (string, embedded.ChatUsage, error)

// fakeCallEndpoint replaces callEndpoint for testing. Functions are dispatched
// by endpoint ID so concurrent calls are routed correctly regardless of
// goroutine scheduling order.
type fakeCallEndpoint struct {
	mu       sync.Mutex
	calls    int
	funcs    map[string]fakeFunc
	received []string // system prompts received
}

func (f *fakeCallEndpoint) call(ctx context.Context, ep EndpointRef, systemPrompt, userPrompt string, temperature float32, maxTokens int, onToken func(string), tools []embedded.OpenAIToolDef, dispatch embedded.MCPDispatcher, projectPath, sheepName string) (string, embedded.ChatUsage, error) {
	f.mu.Lock()
	f.calls++
	f.received = append(f.received, systemPrompt)
	fn := f.funcs[ep.ID]
	f.mu.Unlock()
	if fn == nil {
		return "", embedded.ChatUsage{}, errors.New("no fake for endpoint " + ep.ID)
	}
	return fn(ctx, ep, systemPrompt, userPrompt, temperature, maxTokens, onToken, tools, dispatch, projectPath, sheepName)
}

// withFakeCallEndpoint swaps callEndpoint and returns a restore function.
// It also installs a default reaskProposer fake that returns an error, so
// existing tests that produce CONFIDENCE-less answers don't accidentally
// trigger a real reask (which would hit the fake endpoint over HTTP and
// stall). Tests that need to exercise reask should install their own fake
// via installFakeReask (step-09).
func withFakeCallEndpoint(fake *fakeCallEndpoint) func() {
	orig := callEndpoint
	origReask := reaskProposer
	callEndpoint = fake.call
	reaskProposer = func(context.Context, ProposerSpec, string, string, string, string, time.Duration, string, string, func(string)) (string, embedded.ChatUsage, error) {
		return "", embedded.ChatUsage{}, errors.New("reask not faked")
	}
	return func() {
		callEndpoint = orig
		reaskProposer = origReask
	}
}

// okFake returns a fakeFunc that always succeeds with the given answer and
// confidence. Usage: okFake("answer\nCONFIDENCE: 8")
func okFake(answer string) fakeFunc {
	return func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
		return answer, embedded.ChatUsage{}, nil
	}
}

// errFake returns a fakeFunc that always fails with the given error.
func errFake(err string) fakeFunc {
	return func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
		return "", embedded.ChatUsage{}, errors.New(err)
	}
}

// slowFake returns a fakeFunc that blocks until ctx is cancelled.
func slowFake() fakeFunc {
	return func(ctx context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
		<-ctx.Done()
		return "", embedded.ChatUsage{}, ctx.Err()
	}
}

// TestRunProposers_AllSuccess verifies that three successful proposers return
// results in slot order with parsed confidence values.
func TestRunProposers_AllSuccess(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("Answer A\nCONFIDENCE: 8"),
			"ep2": okFake("Answer B\n신뢰도: 6"),
			"ep3": okFake("Answer C without confidence"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior"},
			{Endpoint: testEndpoint("ep2", "llama-3.3-70b"), PersonaKey: "balthasar"},
			{Endpoint: testEndpoint("ep3", "mistral-small"), PersonaKey: "casper"},
		},
		BaseSystem:  "You are a code reviewer.",
		UserPrompts: []string{"Review this PR"},
		Timeout:     5 * time.Second,
		Temperature: 0.7,
	}

	results := RunProposers(context.Background(), opts)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Slot 0 — melchior, confidence 8
	if results[0].Err != nil {
		t.Fatalf("slot 0 unexpected error: %v", results[0].Err)
	}
	if results[0].Confidence != 8 {
		t.Errorf("slot 0 confidence: expected 8, got %d", results[0].Confidence)
	}
	if results[0].Answer != "Answer A" {
		t.Errorf("slot 0 answer: expected %q, got %q", "Answer A", results[0].Answer)
	}

	// Slot 1 — balthasar, confidence 6 (Korean variant)
	if results[1].Err != nil {
		t.Fatalf("slot 1 unexpected error: %v", results[1].Err)
	}
	if results[1].Confidence != 6 {
		t.Errorf("slot 1 confidence: expected 6, got %d", results[1].Confidence)
	}

	// Slot 2 — casper, no confidence reported → -1
	if results[2].Err != nil {
		t.Fatalf("slot 2 unexpected error: %v", results[2].Err)
	}
	if results[2].Confidence != -1 {
		t.Errorf("slot 2 confidence: expected -1 (not reported), got %d", results[2].Confidence)
	}
	if !strings.Contains(results[2].Answer, "Answer C") {
		t.Errorf("slot 2 answer should contain 'Answer C', got %q", results[2].Answer)
	}
}

// TestRunProposers_OneError verifies that a single proposer failure does not
// abort the round — only that slot has Err set.
func TestRunProposers_OneError(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("OK slot 0\nCONFIDENCE: 7"),
			"ep2": errFake("connection refused"),
			"ep3": okFake("OK slot 2\nCONFIDENCE: 9"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
			{Endpoint: testEndpoint("ep2", "model-b"), PersonaKey: "balthasar"},
			{Endpoint: testEndpoint("ep3", "model-c"), PersonaKey: "casper"},
		},
		BaseSystem:  "test base",
		UserPrompts: []string{"do something"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].Err != nil {
		t.Errorf("slot 0 should succeed, got err: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Error("slot 1 should have an error")
	}
	if results[2].Err != nil {
		t.Errorf("slot 2 should succeed despite slot 1 failure, got err: %v", results[2].Err)
	}
	if results[2].Confidence != 9 {
		t.Errorf("slot 2 confidence: expected 9, got %d", results[2].Confidence)
	}
}

// TestRunProposers_Timeout verifies that a proposer that blocks on ctx.Done
// does not prevent the other two from returning promptly.
func TestRunProposers_Timeout(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("fast slot 0\nCONFIDENCE: 5"),
			"ep2": slowFake(),
			"ep3": okFake("fast slot 2\nCONFIDENCE: 9"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
			{Endpoint: testEndpoint("ep2", "model-b"), PersonaKey: "balthasar"},
			{Endpoint: testEndpoint("ep3", "model-c"), PersonaKey: "casper"},
		},
		BaseSystem:  "test base",
		UserPrompts: []string{"do something"},
		Timeout:     200 * time.Millisecond,
	}

	start := time.Now()
	results := RunProposers(context.Background(), opts)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("round took too long: %v (expected ~200ms timeout)", elapsed)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].Err != nil {
		t.Errorf("slot 0 should succeed, got err: %v", results[0].Err)
	}
	if results[2].Err != nil {
		t.Errorf("slot 2 should succeed, got err: %v", results[2].Err)
	}
	if results[1].Err == nil {
		t.Error("slot 1 should have a timeout error")
	}
}

// TestRunProposers_LiveOutputPersona verifies that the live output callback
// receives lines containing the persona display name and model name.
func TestRunProposers_LiveOutputPersona(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("answer\nCONFIDENCE: 8"),
			"ep2": errFake("boom"),
			"ep3": okFake("answer c\nCONFIDENCE: 9"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	var mu sync.Mutex
	var outputLines []string

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior"},
			{Endpoint: testEndpoint("ep2", "llama-3.3-70b"), PersonaKey: "balthasar"},
			{Endpoint: testEndpoint("ep3", "mistral-small"), PersonaKey: "casper"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
		OnOutput: func(line string) {
			mu.Lock()
			defer mu.Unlock()
			outputLines = append(outputLines, line)
		},
	}

	RunProposers(context.Background(), opts)

	mu.Lock()
	defer mu.Unlock()

	if len(outputLines) != 3 {
		t.Fatalf("expected 3 output lines, got %d", len(outputLines))
	}

	allOutput := strings.Join(outputLines, "")

	expectedPairs := []struct {
		displayName string
		model       string
	}{
		{"MELCHIOR-1", "qwen3-27b"},
		{"BALTHASAR-2", "llama-3.3-70b"},
		{"CASPER-3", "mistral-small"},
	}

	for _, pair := range expectedPairs {
		if !strings.Contains(allOutput, pair.displayName) {
			t.Errorf("output missing persona name %q in:\n%s", pair.displayName, allOutput)
		}
		if !strings.Contains(allOutput, pair.model) {
			t.Errorf("output missing model name %q in:\n%s", pair.model, allOutput)
		}
	}

	if !strings.Contains(allOutput, "응답 실패") {
		t.Errorf("output should contain a failure message:\n%s", allOutput)
	}

	successCount := strings.Count(allOutput, "응답 완료")
	if successCount != 2 {
		t.Errorf("expected 2 success lines, got %d in:\n%s", successCount, allOutput)
	}
}

// TestRunProposers_LiveOutputConfidenceNotReported verifies that when confidence
// is -1 (model did not report), the output shows "신뢰도 미보고".
func TestRunProposers_LiveOutputConfidenceNotReported(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("no confidence line here"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	var mu sync.Mutex
	var outputLines []string

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
		OnOutput: func(line string) {
			mu.Lock()
			defer mu.Unlock()
			outputLines = append(outputLines, line)
		},
	}

	RunProposers(context.Background(), opts)

	mu.Lock()
	defer mu.Unlock()

	if len(outputLines) != 1 {
		t.Fatalf("expected 1 output line, got %d", len(outputLines))
	}
	if !strings.Contains(outputLines[0], "신뢰도 미보고") {
		t.Errorf("output should contain '신뢰도 미보고', got %q", outputLines[0])
	}
}

// TestSuccessfulResults verifies that failed slots are filtered out while
// preserving order and original pipeline Slot indices.
func TestSuccessfulResults(t *testing.T) {
	results := []ProposerResult{
		{Spec: ProposerSpec{}, Slot: 0, Answer: "A", Confidence: 8, Err: nil},
		{Spec: ProposerSpec{}, Slot: 1, Answer: "", Confidence: -1, Err: errors.New("timeout")},
		{Spec: ProposerSpec{}, Slot: 2, Answer: "C", Confidence: 6, Err: nil},
	}

	successful := SuccessfulResults(results)

	if len(successful) != 2 {
		t.Fatalf("expected 2 successful results, got %d", len(successful))
	}
	if successful[0].Answer != "A" || successful[1].Answer != "C" {
		t.Errorf("order not preserved")
	}
	// Compact indices are 0,1 but pipeline slots must stay 0,2 so [MAGI:N]
	// and OnProposerToken still target the original panels (task #7234).
	if successful[0].Slot != 0 || successful[1].Slot != 2 {
		t.Errorf("Slot not preserved: got [%d, %d], want [0, 2]", successful[0].Slot, successful[1].Slot)
	}
}

// TestSuccessfulResults_AllFailed verifies empty slice when all fail.
func TestSuccessfulResults_AllFailed(t *testing.T) {
	results := []ProposerResult{
		{Err: errors.New("err1")},
		{Err: errors.New("err2")},
	}
	successful := SuccessfulResults(results)
	if len(successful) != 0 {
		t.Fatalf("expected 0 successful results when all failed, got %d", len(successful))
	}
}

// TestSuccessfulResults_Empty verifies empty input returns empty.
func TestSuccessfulResults_Empty(t *testing.T) {
	successful := SuccessfulResults(nil)
	if len(successful) != 0 {
		t.Fatalf("expected 0 results for nil input, got %d", len(successful))
	}
}

// TestRunProposers_OriginalSlotsPreservesPipelineIndex verifies that when
// OriginalSlots remaps a compact Proposers list (post-SuccessfulResults),
// OnProposerToken and result.Slot use the pipeline index, not 0..n-1.
func TestRunProposers_OriginalSlotsPreservesPipelineIndex(t *testing.T) {
	var tokenSlots []int
	var mu sync.Mutex
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"epA": func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, onToken func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				if onToken != nil {
					onToken("hello-from-slot-2")
				}
				return longKoreanProse("slot2 answer ") + "\nCONFIDENCE: 7", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		// Compact list: only the surviving proposer (original pipeline slot 2).
		Proposers:     []ProposerSpec{{Endpoint: testEndpoint("epA", "model-c"), PersonaKey: "casper"}},
		OriginalSlots: []int{2},
		BaseSystem:    "test",
		UserPrompts:   []string{"prompt"},
		Timeout:       5 * time.Second,
		OnProposerToken: func(slot int, text string) {
			mu.Lock()
			tokenSlots = append(tokenSlots, slot)
			mu.Unlock()
		},
	}

	results := RunProposers(context.Background(), opts)
	if len(results) != 1 {
		t.Fatalf("len(results)=%d, want 1", len(results))
	}
	if results[0].Slot != 2 {
		t.Errorf("result.Slot=%d, want 2 (pipeline index)", results[0].Slot)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokenSlots) == 0 {
		t.Fatal("expected OnProposerToken to fire")
	}
	for _, s := range tokenSlots {
		if s != 2 {
			t.Errorf("OnProposerToken slot=%d, want 2", s)
		}
	}
}

// TestOriginalSlotsFor_FallbackAndPreserve covers the debate helper that
// maps compacted results back to pipeline slots.
func TestOriginalSlotsFor_FallbackAndPreserve(t *testing.T) {
	// Hand-built fixtures with Slot all zero → fall back to compact indices.
	unset := []ProposerResult{{}, {}, {}}
	got := originalSlotsFor(unset)
	if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Errorf("unset fixtures: got %v, want [0 1 2]", got)
	}

	// After SuccessfulResults filter: slots 0 and 2 survive.
	filtered := []ProposerResult{{Slot: 0}, {Slot: 2}}
	got = originalSlotsFor(filtered)
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("filtered: got %v, want [0 2]", got)
	}

	// Single survivor at original slot 0 (zero is a valid slot).
	only0 := []ProposerResult{{Slot: 0}}
	got = originalSlotsFor(only0)
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("only slot 0: got %v, want [0]", got)
	}
}

// TestRunProposers_TemperatureDefault verifies that temperature 0 is replaced
// with the diversity default of 0.7.
func TestRunProposers_TemperatureDefault(t *testing.T) {
	var capturedTemp float32
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _, _ string, temp float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				capturedTemp = temp
				return "answer\nCONFIDENCE: 5", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Temperature: 0,
		Timeout:     5 * time.Second,
		OnOutput:    func(string) {},
	}

	RunProposers(context.Background(), opts)

	if capturedTemp != 0.7 {
		t.Errorf("temperature default: expected 0.7, got %f", capturedTemp)
	}
}

// TestRunProposers_SystemPromptContainsPersona verifies that the system prompt
// passed to callEndpoint includes the persona block.
func TestRunProposers_SystemPromptContainsPersona(t *testing.T) {
	var capturedSystem string
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, sys string, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				capturedSystem = sys
				return "answer\nCONFIDENCE: 5", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "balthasar"},
		},
		BaseSystem:  "BASE SYSTEM PROMPT",
		UserPrompts: []string{"user prompt"},
		Timeout:     5 * time.Second,
	}

	RunProposers(context.Background(), opts)

	if !strings.Contains(capturedSystem, "BASE SYSTEM PROMPT") {
		t.Errorf("system prompt should contain base system prompt")
	}
	if !strings.Contains(capturedSystem, "BALTHASAR-2") {
		t.Errorf("system prompt should contain persona name BALTHASAR-2")
	}
	if !strings.Contains(capturedSystem, "심의 규칙") {
		t.Errorf("system prompt should contain deliberation rules")
	}
}

// TestRunProposers_MaxTokens verifies that max tokens is computed as
// ContextTokens / 4.
func TestRunProposers_MaxTokens(t *testing.T) {
	var capturedMaxTokens int
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _ string, _ string, _ float32, mt int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				capturedMaxTokens = mt
				return "answer\nCONFIDENCE: 5", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	RunProposers(context.Background(), opts)

	if capturedMaxTokens != 8192 {
		t.Errorf("maxTokens: expected 8192 (32768/4), got %d", capturedMaxTokens)
	}
}

// TestRunProposers_MaxTokens_DefaultContext verifies that when ContextTokens
// is 0, the default is used.
func TestRunProposers_MaxTokens_DefaultContext(t *testing.T) {
	var capturedMaxTokens int
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _ string, _ string, _ float32, mt int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				capturedMaxTokens = mt
				return "answer\nCONFIDENCE: 5", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	ep := testEndpoint("ep1", "model-a")
	ep.ContextTokens = 0

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: ep, PersonaKey: "melchior"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	RunProposers(context.Background(), opts)

	if capturedMaxTokens != embedded.DefaultContextTokens/4 {
		t.Errorf("maxTokens with default context: expected %d (%d/4), got %d",
			embedded.DefaultContextTokens/4, embedded.DefaultContextTokens,
			capturedMaxTokens)
	}
}

// TestRunProposers_BlindIsolation verifies that each proposer receives only
// its own system prompt and user prompt — no cross-contamination of answers.
func TestRunProposers_BlindIsolation(t *testing.T) {
	var mu sync.Mutex
	receivedUserPrompts := make(map[string]string)

	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _ string, userPrompt string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				mu.Lock()
				receivedUserPrompts["ep1"] = userPrompt
				mu.Unlock()
				return "answer from melchior\nCONFIDENCE:7", embedded.ChatUsage{}, nil
			},
			"ep2": func(_ context.Context, _ EndpointRef, _ string, userPrompt string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				mu.Lock()
				receivedUserPrompts["ep2"] = userPrompt
				mu.Unlock()
				return "answer from balthasar\nCONFIDENCE:6", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
			{Endpoint: testEndpoint("ep2", "model-b"), PersonaKey: "balthasar"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"shared prompt", "shared prompt"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if receivedUserPrompts["ep1"] != "shared prompt" {
		t.Errorf("proposer ep1 should receive 'shared prompt', got %q", receivedUserPrompts["ep1"])
	}
	if receivedUserPrompts["ep2"] != "shared prompt" {
		t.Errorf("proposer ep2 should receive 'shared prompt', got %q", receivedUserPrompts["ep2"])
	}
	if strings.Contains(results[0].Answer, "balthasar") {
		t.Errorf("slot 0 answer should not contain balthasar's answer")
	}
	if strings.Contains(results[1].Answer, "melchior") {
		t.Errorf("slot 1 answer should not contain melchior's answer")
	}
}

// TestRunProposers_PerSlotSheepName verifies that each proposer receives a
// unique sheep name derived from the base sheep name + persona display name.
// This ensures each MAGI proposer gets its own isolated browser session
// (task #7139).
func TestRunProposers_PerSlotSheepName(t *testing.T) {
	var mu sync.Mutex
	receivedNames := make(map[string]string) // endpoint ID → sheepName

	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _ string, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, sn string) (string, embedded.ChatUsage, error) {
				mu.Lock()
				receivedNames["ep1"] = sn
				mu.Unlock()
				return "answer\nCONFIDENCE: 8", embedded.ChatUsage{}, nil
			},
			"ep2": func(_ context.Context, _ EndpointRef, _ string, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, sn string) (string, embedded.ChatUsage, error) {
				mu.Lock()
				receivedNames["ep2"] = sn
				mu.Unlock()
				return "answer\nCONFIDENCE: 7", embedded.ChatUsage{}, nil
			},
			"ep3": func(_ context.Context, _ EndpointRef, _ string, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, sn string) (string, embedded.ChatUsage, error) {
				mu.Lock()
				receivedNames["ep3"] = sn
				mu.Unlock()
				return "answer\nCONFIDENCE: 9", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
			{Endpoint: testEndpoint("ep2", "model-b"), PersonaKey: "balthasar"},
			{Endpoint: testEndpoint("ep3", "model-c"), PersonaKey: "casper"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
		SheepName:   "햄찌",
	}

	RunProposers(context.Background(), opts)

	mu.Lock()
	defer mu.Unlock()

	expected := map[string]string{
		"ep1": "햄찌-slot0-MELCHIOR-1",
		"ep2": "햄찌-slot1-BALTHASAR-2",
		"ep3": "햄찌-slot2-CASPER-3",
	}
	for ep, want := range expected {
		if got := receivedNames[ep]; got != want {
			t.Errorf("endpoint %s: sheepName = %q, want %q", ep, got, want)
		}
	}
}

// TestRunProposers_EmptySheepName verifies that when SheepName is empty (e.g.
// tests that don't use browser tools), callEndpoint receives an empty string
// and does not inject sheep_name into tool args.
func TestRunProposers_EmptySheepName(t *testing.T) {
	var receivedName string
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _ string, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, sn string) (string, embedded.ChatUsage, error) {
				receivedName = sn
				return "answer\nCONFIDENCE: 5", embedded.ChatUsage{}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
		SheepName:   "", // empty — no browser session
	}

	RunProposers(context.Background(), opts)

	if receivedName != "" {
		t.Errorf("empty SheepName should produce empty per-slot name, got %q", receivedName)
	}
}

// ─── callEndpoint mini agent loop tests ───────────────────────────────────
//
// These drive the REAL callEndpoint via the chatTurn seam so the tool
// exploration, forced-convergence, and empty-response-nudge paths are covered
// (the fakeCallEndpoint tests above replace callEndpoint wholesale and never
// exercise its loop).

// scriptedTurn is one programmed response from a scripted fake chatTurn.
type scriptedTurn struct {
	msg *embedded.ChatMessage
	err error
}

// fakeChatTurn returns a scripted sequence of assistant messages and records
// every request it saw so tests can assert on tool presence and appended
// directive/nudge turns.
type fakeChatTurn struct {
	mu     sync.Mutex
	script []scriptedTurn
	idx    int
	reqs   []*embedded.ChatRequest
}

func (f *fakeChatTurn) turn(_ context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if f.idx >= len(f.script) {
		return &embedded.ChatMessage{Role: embedded.ChatRoleAssistant}, embedded.ChatUsage{}, nil
	}
	tn := f.script[f.idx]
	f.idx++
	return tn.msg, embedded.ChatUsage{}, tn.err
}

func withFakeChatTurn(fn func(context.Context, *embedded.Client, *embedded.ChatRequest, func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error)) func() {
	orig := chatTurn
	chatTurn = fn
	return func() { chatTurn = orig }
}

// installFakeReask replaces reaskProposer for testing and returns a restore
// function. Mirrors the withFakeChatTurn pattern.
func installFakeReask(fn func(ctx context.Context, spec ProposerSpec, systemPrompt, taskPrompt, prevAnswer, directive string, budget time.Duration, projectPath, sheepName string, onToken func(string)) (string, embedded.ChatUsage, error)) func() {
	orig := reaskProposer
	reaskProposer = fn
	return func() { reaskProposer = orig }
}

func toolCallMsg(name, args string) *embedded.ChatMessage {
	return &embedded.ChatMessage{
		Role: embedded.ChatRoleAssistant,
		ToolCalls: []embedded.ToolCall{
			{ID: "tc1", Type: "function", Func: embedded.ToolCallFunction{Name: name, Args: args}},
		},
	}
}

func answerMsg(content string) *embedded.ChatMessage {
	return &embedded.ChatMessage{Role: embedded.ChatRoleAssistant, Content: content}
}

func emptyMsg() *embedded.ChatMessage {
	return &embedded.ChatMessage{Role: embedded.ChatRoleAssistant, Content: ""}
}

// TestCallEndpoint_ToolExplorationThenAnswer verifies that a proposer reads via
// a tool call and then returns a final answer, with the tool dispatched once.
func TestCallEndpoint_ToolExplorationThenAnswer(t *testing.T) {
	fake := &fakeChatTurn{script: []scriptedTurn{
		{msg: toolCallMsg("get_status", `{}`)},
		{msg: answerMsg("final answer\nCONFIDENCE: 8")},
	}}
	defer withFakeChatTurn(fake.turn)()

	var dispatched []string
	dispatch := func(name string, _ map[string]interface{}) (string, []embedded.MCPImage, error) {
		dispatched = append(dispatched, name)
		return "status: ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "final answer\nCONFIDENCE: 8" {
		t.Errorf("unexpected content: %q", content)
	}
	if len(dispatched) != 1 || dispatched[0] != "get_status" {
		t.Errorf("expected get_status dispatched once, got %v", dispatched)
	}
	if len(fake.reqs) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(fake.reqs))
	}
	if len(fake.reqs[0].Tools) == 0 {
		t.Error("exploration request should include tools")
	}
}

// TestCallEndpoint_ForcedConvergenceNearDeadline verifies that when a model
// keeps calling tools, the loop stops exploring near the deadline and forces a
// final answer via a tools-off request — instead of hard-failing (task #7066).
func TestCallEndpoint_ForcedConvergenceNearDeadline(t *testing.T) {
	var mu sync.Mutex
	var toolReqs, finalReqs int
	restore := withFakeChatTurn(func(_ context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(req.Tools) > 0 {
			toolReqs++
			// Slow exploration turn so the deadline is reached in a bounded
			// number of iterations.
			time.Sleep(20 * time.Millisecond)
			return toolCallMsg("get_status", `{}`), embedded.ChatUsage{}, nil
		}
		finalReqs++ // convergence request (tools dropped) → final answer
		return answerMsg("converged answer\nCONFIDENCE: 5"), embedded.ChatUsage{}, nil
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "converged answer\nCONFIDENCE: 5" {
		t.Errorf("expected converged answer, got %q", content)
	}
	mu.Lock()
	defer mu.Unlock()
	if toolReqs == 0 {
		t.Error("expected at least one tool-exploration request before convergence")
	}
	if finalReqs == 0 {
		t.Error("expected a tools-off convergence request")
	}
}

// TestCallEndpoint_EmptyAnswerNudgeRecovers verifies that an empty answer is
// rescued by a single nudge instead of an immediate hard failure.
func TestCallEndpoint_EmptyAnswerNudgeRecovers(t *testing.T) {
	fake := &fakeChatTurn{script: []scriptedTurn{
		{msg: emptyMsg()},
		{msg: answerMsg("recovered\nCONFIDENCE: 6")},
	}}
	defer withFakeChatTurn(fake.turn)()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// No tools → forceFinalAnswer path with appendDirective=false.
	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, nil, nil, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "recovered\nCONFIDENCE: 6" {
		t.Errorf("expected recovered answer, got %q", content)
	}
	if len(fake.reqs) != 2 {
		t.Fatalf("expected 2 requests (initial + nudge), got %d", len(fake.reqs))
	}
	last := fake.reqs[1].Messages[len(fake.reqs[1].Messages)-1]
	if last.Role != embedded.ChatRoleUser || !strings.Contains(last.Content, "빈 응답") {
		t.Errorf("nudge request should append the empty-answer nudge, got %+v", last)
	}
}

// TestCallEndpoint_EmptyAnswerTwiceFails verifies that two consecutive empty
// answers (initial + nudge) produce a failure, and no third attempt is made.
func TestCallEndpoint_EmptyAnswerTwiceFails(t *testing.T) {
	fake := &fakeChatTurn{script: []scriptedTurn{
		{msg: emptyMsg()},
		{msg: emptyMsg()},
	}}
	defer withFakeChatTurn(fake.turn)()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, nil, nil, "", "sheep")
	if err == nil {
		t.Fatal("expected error when both attempts are empty")
	}
	if !strings.Contains(err.Error(), "no substantive answer") {
		t.Errorf("expected no-substantive-answer error, got %v", err)
	}
	if len(fake.reqs) != 2 {
		t.Errorf("expected exactly 2 attempts, got %d", len(fake.reqs))
	}
}

// TestCallEndpoint_WriteToolRejected verifies that a write tool requested by a
// proposer is never dispatched; the rejection is fed back as a tool result.
func TestCallEndpoint_WriteToolRejected(t *testing.T) {
	fake := &fakeChatTurn{script: []scriptedTurn{
		{msg: toolCallMsg("write_file", `{"path":"x"}`)},
		{msg: answerMsg("done\nCONFIDENCE: 7")},
	}}
	defer withFakeChatTurn(fake.turn)()

	var dispatched []string
	dispatch := func(name string, _ map[string]interface{}) (string, []embedded.MCPImage, error) {
		dispatched = append(dispatched, name)
		return "should not run", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "write_file"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "done\nCONFIDENCE: 7" {
		t.Errorf("unexpected content: %q", content)
	}
	if len(dispatched) != 0 {
		t.Errorf("write tool must not be dispatched, got %v", dispatched)
	}
	found := false
	for _, m := range fake.reqs[1].Messages {
		if m.Role == embedded.ChatRoleTool && strings.Contains(m.Content, "not allowed") {
			found = true
		}
	}
	if !found {
		t.Error("expected a tool-role rejection message in the follow-up request")
	}
}

// TestConvergenceCutoff verifies the reserve computation: default 1/4 with a
// floor, capped at half the remaining budget, and false when no deadline.
func TestConvergenceCutoff(t *testing.T) {
	// No deadline → no cutoff.
	if _, _, ok := convergenceCutoff(context.Background()); ok {
		t.Error("expected no cutoff for a deadline-less context")
	}

	// Generous budget → reserve is 1/4 (below the half cap, above the floor).
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	cutoff, reserve, ok := convergenceCutoff(ctx)
	if !ok {
		t.Fatal("expected a cutoff for a deadline context")
	}
	dl, _ := ctx.Deadline()
	// reserve should be ~50s (200/4) and cutoff = deadline - reserve. Allow
	// small slack for scheduling drift between context creation and the
	// computation.
	if reserve < 49*time.Second || reserve > 51*time.Second {
		t.Errorf("expected ~50s reserve, got %v", reserve)
	}
	if gap := dl.Sub(cutoff); gap < 49*time.Second || gap > 51*time.Second {
		t.Errorf("expected cutoff ~50s before deadline, got %v", gap)
	}
}

// ─── task #7077 regression tests (the three real-world failure modes) ──────────
//
// These drive callEndpoint through the seams that the earlier convergence tests
// left uncovered: an exploration turn whose deadline actually fires (BALTHASAR),
// tool-call markup returned as text that the gate rejects (CASPER), and a
// transient send error mid-exploration (MELCHIOR).

// TestCallEndpoint_ConvergenceSurvivesExpiredExplorationCtx reproduces BALTHASAR:
// an exploration turn runs the per-proposer deadline out, yet forced convergence
// must still produce an answer because it runs on a detached, freshly-budgeted
// context. Sharing the exploration ctx (the old behavior) would hand the forced
// request an already-expired context → "context deadline exceeded".
func TestCallEndpoint_ConvergenceSurvivesExpiredExplorationCtx(t *testing.T) {
	restore := withFakeChatTurn(func(ctx context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		if len(req.Tools) > 0 {
			// Slow exploration turn: block until the ORIGINAL deadline fires.
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
			}
			return toolCallMsg("get_status", `{}`), embedded.ChatUsage{}, nil
		}
		// Forced-convergence request. A real client would fail on an expired
		// ctx; if the fix works this ctx is detached and still live.
		if ctx.Err() != nil {
			return nil, embedded.ChatUsage{}, ctx.Err()
		}
		return answerMsg("탐색 데드라인이 지났어도 누적 맥락으로 최종 답변을 완성했다.\nCONFIDENCE: 5"), embedded.ChatUsage{}, nil
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("forced convergence should survive an expired exploration ctx, got err: %v", err)
	}
	if !strings.Contains(content, "최종 답변을 완성") {
		t.Errorf("expected the converged answer, got %q", content)
	}
}

// TestCallEndpoint_ToolCallTextGetsNudged reproduces CASPER: a proposer returns
// tool-call markup as its answer text (non-empty Content, no structured tool
// calls). The old loop returned it verbatim and the RunProposers gate then
// rejected it with no recovery. Now it must be nudged into a real answer.
func TestCallEndpoint_ToolCallTextGetsNudged(t *testing.T) {
	fake := &fakeChatTurn{script: []scriptedTurn{
		{msg: answerMsg(`<tool_call>{"name": "read_file", "arguments": {"path": "x"}}</tool_call>`)},
		{msg: answerMsg("파일을 확인한 결과 문제는 데드라인 공유였고, 독립 컨텍스트 분리로 해결된다.\nCONFIDENCE: 7")},
	}}
	defer withFakeChatTurn(fake.turn)()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "read_file"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("tool-call-text should be nudged, not fail: %v", err)
	}
	if CheckAnswerContent(content) != nil {
		t.Errorf("recovered answer must pass the content gate, got %q", content)
	}
	if len(fake.reqs) != 2 {
		t.Fatalf("expected 2 requests (exploration + tools-off convergence), got %d", len(fake.reqs))
	}
	if len(fake.reqs[0].Tools) == 0 {
		t.Error("first request should be the tool-exploration turn")
	}
	if len(fake.reqs[1].Tools) != 0 {
		t.Error("convergence request must drop tools")
	}
}

// TestCallEndpoint_TransientErrorSalvagedByConvergence reproduces MELCHIOR's
// salvage case: a transient send error AFTER useful tool exploration must not
// discard the accumulated context — forced convergence recovers a final answer.
func TestCallEndpoint_TransientErrorSalvagedByConvergence(t *testing.T) {
	fake := &fakeChatTurn{script: []scriptedTurn{
		{msg: toolCallMsg("get_status", `{}`)},                              // explores successfully
		{err: errors.New("transient error after 4 retries: API error 500")}, // send error mid-loop
		{msg: answerMsg("서버 오류 직전까지 모은 맥락으로 최종 답변을 정리했다.\nCONFIDENCE: 4")},  // salvaged
	}}
	defer withFakeChatTurn(fake.turn)()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("a transient error after exploration should be salvaged, got err: %v", err)
	}
	if !strings.Contains(content, "최종 답변을 정리") {
		t.Errorf("expected the salvaged answer, got %q", content)
	}
}

// TestCallEndpoint_TransientErrorSurfacedWhenConvergenceFails verifies that when
// the endpoint is truly down — exploration errors AND convergence cannot rescue
// an answer — the original transport error is surfaced (more diagnostic than
// "no substantive answer") rather than swallowed.
func TestCallEndpoint_TransientErrorSurfacedWhenConvergenceFails(t *testing.T) {
	fake := &fakeChatTurn{script: []scriptedTurn{
		{err: errors.New("transient error after 4 retries: API error 500: Internal Server Error")},
		{msg: emptyMsg()}, // convergence attempt 1 → empty
		{msg: emptyMsg()}, // convergence attempt 2 (nudge) → empty
	}}
	defer withFakeChatTurn(fake.turn)()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err == nil {
		t.Fatal("expected an error when the endpoint is down")
	}
	if !strings.Contains(err.Error(), "API error 500") {
		t.Errorf("expected the original transport error surfaced, got %v", err)
	}
}

// TestCallEndpoint_UserCancelAbortsWithoutSalvage verifies that a user
// cancellation aborts immediately — it must NOT fall through to the salvage /
// forced-convergence path (that path is only for transient/deadline errors).
func TestCallEndpoint_UserCancelAbortsWithoutSalvage(t *testing.T) {
	fake := &fakeChatTurn{script: []scriptedTurn{
		{err: context.Canceled},
	}}
	defer withFakeChatTurn(fake.turn)()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // user stop

	_, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(fake.reqs) != 1 {
		t.Errorf("user cancel must not attempt forced convergence, got %d requests", len(fake.reqs))
	}
}

// TestCallEndpoint_ExplorationDeadlineNotSurfacedAsError verifies the task #7081
// review point: when the exploration deadline fires (the *expected* convergence
// trigger) and forced convergence then also fails to produce an answer, the
// surfaced error is the diagnostic "no substantive answer" — NOT a bare
// "context deadline exceeded". The deadline is by design, so it must not be
// recorded as exploreErr and win over the convergence failure.
func TestCallEndpoint_ExplorationDeadlineNotSurfacedAsError(t *testing.T) {
	restore := withFakeChatTurn(func(ctx context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		if len(req.Tools) > 0 {
			// Exploration turn: run the per-proposer deadline out, then report the
			// ctx error like a real client would.
			<-ctx.Done()
			return nil, embedded.ChatUsage{}, ctx.Err()
		}
		// Forced convergence runs on a detached, freshly-budgeted ctx — keep it
		// failing empty so convergence cannot rescue an answer.
		return emptyMsg(), embedded.ChatUsage{}, nil
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err == nil {
		t.Fatal("expected an error when convergence cannot produce an answer")
	}
	if !strings.Contains(err.Error(), "no substantive answer") {
		t.Errorf("convergence failure should be surfaced, got %v", err)
	}
	if strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("the expected exploration deadline must not be the surfaced error, got %v", err)
	}
}

// TestCallEndpoint_PerTurnTimeoutStopsSlowTurn verifies that a slow LLM turn
// started near convergeAt is cut off by the per-turn timeout — not by the
// parent ctx deadline. This is the core fix for task #7150: without per-turn
// timeout, a slow turn would bleed into the convergence reserve and produce
// "scan SSE: context deadline exceeded" when the parent ctx expires mid-stream.
//
// Setup: parent ctx = 600ms, reserve ≈ 150ms, convergeAt ≈ 450ms.
// The exploration turn sleeps 500ms (would exceed convergeAt).
// Per-turn timeout fires at convergeAt (~450ms), breaking the turn cleanly.
// Forced convergence then runs on a detached ctx with ~150ms budget and
// succeeds.
func TestCallEndpoint_PerTurnTimeoutStopsSlowTurn(t *testing.T) {
	var mu sync.Mutex
	var explorationTurns, convergenceTurns int
	var explorationCtxErr error

	restore := withFakeChatTurn(func(ctx context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(req.Tools) > 0 {
			explorationTurns++
			// Simulate a slow LLM call that would run past convergeAt.
			// The per-turn ctx should fire before the 500ms sleep completes.
			select {
			case <-time.After(500 * time.Millisecond):
				// If we get here, per-turn timeout didn't fire — bug.
				return toolCallMsg("get_status", `{}`), embedded.ChatUsage{}, nil
			case <-ctx.Done():
				explorationCtxErr = ctx.Err()
				return nil, embedded.ChatUsage{}, ctx.Err()
			}
		}
		convergenceTurns++
		return answerMsg("per-turn timeout이 정상적으로 동작했다\nCONFIDENCE: 7"), embedded.ChatUsage{}, nil
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	// Parent ctx: 600ms. Reserve ≈ 150ms (1/4 of 600ms). ConvergeAt ≈ 450ms.
	// Per-turn timeout = time.Until(convergeAt) ≈ 450ms.
	// The 500ms sleep in the fake turn will be cut off at ~450ms.
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("expected convergence to succeed after per-turn timeout, got err: %v", err)
	}
	if !strings.Contains(content, "per-turn timeout") {
		t.Errorf("expected the converged answer, got %q", content)
	}

	mu.Lock()
	defer mu.Unlock()
	if explorationTurns != 1 {
		t.Errorf("expected exactly 1 exploration turn, got %d", explorationTurns)
	}
	if convergenceTurns == 0 {
		t.Error("expected at least one convergence turn")
	}
	if explorationCtxErr == nil {
		t.Error("expected the exploration turn to report a ctx error (per-turn timeout)")
	}
	if !errors.Is(explorationCtxErr, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded from per-turn ctx, got %v", explorationCtxErr)
	}
}

// TestCallEndpoint_PerTurnTimeoutNotRecordedAsExploreErr verifies that when
// the per-turn ctx expires (parent ctx still alive), the DeadlineExceeded
// error is NOT recorded as exploreErr — so if forced convergence also fails,
// the surfaced error is the diagnostic "no substantive answer" rather than a
// bare "context deadline exceeded". This is the task #7081 review point
// extended to per-turn timeouts.
func TestCallEndpoint_PerTurnTimeoutNotRecordedAsExploreErr(t *testing.T) {
	restore := withFakeChatTurn(func(ctx context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		if len(req.Tools) > 0 {
			// Slow turn — per-turn ctx will expire.
			<-ctx.Done()
			return nil, embedded.ChatUsage{}, ctx.Err()
		}
		// Convergence fails — both attempts return empty.
		return emptyMsg(), embedded.ChatUsage{}, nil
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	_, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err == nil {
		t.Fatal("expected an error when convergence cannot produce an answer")
	}
	if strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("per-turn timeout must not be surfaced as 'deadline exceeded', got %v", err)
	}
	if !strings.Contains(err.Error(), "no substantive answer") {
		t.Errorf("expected 'no substantive answer' diagnostic, got %v", err)
	}
}

// TestCallEndpoint_PerTurnTimeoutMinTurnThreshold verifies that when the
// remaining time before convergeAt is less than the minimum turn threshold
// (reserve / minPerTurnFraction), the loop breaks without starting a new turn
// — even if convergeAt hasn't been reached yet. This prevents starting a turn
// that would almost immediately be cut off.
func TestCallEndpoint_PerTurnTimeoutMinTurnThreshold(t *testing.T) {
	var mu sync.Mutex
	var turnCount int

	restore := withFakeChatTurn(func(ctx context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		mu.Lock()
		turnCount++
		mu.Unlock()
		// Each turn sleeps just a bit so time advances.
		time.Sleep(10 * time.Millisecond)
		return toolCallMsg("get_status", `{}`), embedded.ChatUsage{}, nil
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	// Parent ctx: 100ms. Reserve ≈ 25ms. ConvergeAt ≈ 75ms.
	// minTurn = 25ms / 3 ≈ 8.3ms.
	// After a couple of 10ms turns, remaining time < minTurn → break.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")

	mu.Lock()
	defer mu.Unlock()
	// Should have done a few exploration turns but not an excessive number.
	if turnCount == 0 {
		t.Error("expected at least one exploration turn")
	}
	// The loop should have stopped well before the parent ctx expired.
	// With 100ms budget and 10ms turns, we expect at most ~7 turns before
	// the minTurn threshold kicks in.
	if turnCount > 15 {
		t.Errorf("expected the minTurn threshold to stop exploration early, got %d turns", turnCount)
	}
}

// TestCallEndpoint_InPlaceContextHandoff verifies that when the accumulated
// message history exceeds the soft token threshold, an in-place context refresh
// is triggered: the model is asked to summarize, and the message list is
// replaced with [system, user, summary]. The refresh should happen at most
// once (task #7164).
//
// Token thresholds are validated against real Q4 API usage response
// instead of mathematical estimation (task #7165 review).
func TestCallEndpoint_InPlaceContextHandoff(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	var mu sync.Mutex
	var callTypes []string // "explore", "summary", "final"
	var actualUsages []int // prompt tokens from real API responses

	// Q4 API endpoint — real usage response replaces mathematical token
	// estimation (task #7165 review). Credentials come from environment
	// variables to avoid committing secrets (code review #7171).
	q4URL := os.Getenv("SHEPHERD_TEST_Q4_URL")
	q4Key := os.Getenv("SHEPHERD_TEST_Q4_KEY")
	if q4URL == "" || q4Key == "" {
		t.Skip("skip: SHEPHERD_TEST_Q4_URL or SHEPHERD_TEST_Q4_KEY not set")
	}
	q4Ep := testEndpoint("qwen3.6-27b-q4", "qwen3.6-27b-q4")
	q4Ep.BaseURL = q4URL
	q4Ep.APIKey = q4Key
	// Reduced from 64000 to 8000 so the soft threshold (65% = 5200 tokens)
	// is reached within the 60s timeout. Each Korean dispatch result adds
	// ~1600 estimated tokens after truncation (2000 rune floor), so the
	// handoff triggers after 3-4 exploration turns.
	q4Ep.ContextTokens = 8000

	restore := withFakeChatTurn(func(ctx context.Context, client *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		mu.Lock()
		defer mu.Unlock()

		// Detect request type by tools presence and message content.
		if len(req.Tools) > 0 {
			// Exploration turn: call real Q4 API to get actual token usage.
			callTypes = append(callTypes, "explore")

			// Build a real request with the same messages to measure actual tokens.
			realReq := &embedded.ChatRequest{
				Model:       req.Model,
				Messages:    req.Messages,
				Temperature: req.Temperature,
				MaxTokens:   50, // minimal response for token measurement
				Stream:      false,
			}
			resp, _ := client.Chat(ctx, realReq)
			if resp != nil {
				actualUsages = append(actualUsages, int(resp.Usage.PromptTokens))
			}

			return toolCallMsg("get_status", `{}`), embedded.ChatUsage{}, nil
		}

		// No tools → either summary request or forced convergence.
		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == embedded.ChatRoleUser && strings.Contains(lastMsg.Content, "간결하지만 완전하게 요약") {
			callTypes = append(callTypes, "summary")
			return answerMsg("요약: 파일 구조와 주요 발견을 확인했습니다."), embedded.ChatUsage{}, nil
		}

		// Forced convergence.
		callTypes = append(callTypes, "final")
		return answerMsg("최종 답변입니다.\nCONFIDENCE: 7"), embedded.ChatUsage{}, nil
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		// Korean text: each character counts as ~1 token (vs ASCII's 4:1),
		// so the soft threshold is reached quickly.
		return strings.Repeat("데이터 ", 5000), nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	content, _, err := callEndpoint(ctx, q4Ep, "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content == "" {
		t.Error("expected non-empty final answer")
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify that a summary request was made.
	hasSummary := false
	summaryCount := 0
	for _, ct := range callTypes {
		if ct == "summary" {
			hasSummary = true
			summaryCount++
		}
	}
	if !hasSummary {
		t.Errorf("expected at least one summary request (context handoff), got calls: %v", callTypes)
	}
	if summaryCount > 1 {
		t.Errorf("expected at most one in-place handoff, got %d", summaryCount)
	}

	// Log actual Q4 API prompt token usage for debugging.
	t.Logf("Q4 API prompt tokens per exploration turn: %v", actualUsages)
	t.Logf("call sequence: %v", callTypes)

	// Verify the handoff triggered at a reasonable point using real API data.
	exploreCount := 0
	for _, ct := range callTypes {
		if ct == "explore" {
			exploreCount++
		}
	}
	if exploreCount < 2 {
		t.Errorf("expected at least 2 exploration turns before handoff, got %d", exploreCount)
	}
}

// TestCallEndpoint_HardThresholdStopsExploration verifies that when the token
// count exceeds the hard threshold (85%), exploration stops immediately even
// if a handoff was already used, falling through to forced convergence.
//
// Token thresholds are validated against real Q4 API usage response
// instead of mathematical estimation (task #7165 review).
func TestCallEndpoint_HardThresholdStopsExploration(t *testing.T) {
	if testing.Short() {
		t.Skip("skip integration test in short mode")
	}

	var mu sync.Mutex
	var exploreTurns int
	var actualUsages []int

	// Q4 API endpoint — real usage response replaces mathematical token
	// estimation (task #7165 review). Credentials come from environment
	// variables to avoid committing secrets (code review #7171).
	q4URL := os.Getenv("SHEPHERD_TEST_Q4_URL")
	q4Key := os.Getenv("SHEPHERD_TEST_Q4_KEY")
	if q4URL == "" || q4Key == "" {
		t.Skip("skip: SHEPHERD_TEST_Q4_URL or SHEPHERD_TEST_Q4_KEY not set")
	}
	q4Ep := testEndpoint("qwen3.6-27b-q4", "qwen3.6-27b-q4")
	q4Ep.BaseURL = q4URL
	q4Ep.APIKey = q4Key
	// Reduced from 64000 to 8000 so the hard threshold (85% = 6800 tokens)
	// is reached within the 60s timeout. Consistent with the soft threshold
	// test's ContextTokens for comparable token budget.
	q4Ep.ContextTokens = 8000

	restore := withFakeChatTurn(func(ctx context.Context, client *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		mu.Lock()
		defer mu.Unlock()

		if len(req.Tools) > 0 {
			exploreTurns++

			// Measure actual prompt tokens via real Q4 API.
			realReq := &embedded.ChatRequest{
				Model:       req.Model,
				Messages:    req.Messages,
				Temperature: req.Temperature,
				MaxTokens:   50,
				Stream:      false,
			}
			resp, _ := client.Chat(ctx, realReq)
			if resp != nil {
				actualUsages = append(actualUsages, int(resp.Usage.PromptTokens))
			}

			return toolCallMsg("get_status", `{}`), embedded.ChatUsage{}, nil
		}

		// Summary or forced convergence — return summary first time, final answer second.
		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == embedded.ChatRoleUser && strings.Contains(lastMsg.Content, "간결하지만 완전하게 요약") {
			return answerMsg("요약 내용"), embedded.ChatUsage{}, nil
		}
		return answerMsg("최종 답변\nCONFIDENCE: 6"), embedded.ChatUsage{}, nil
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return strings.Repeat("데이터 ", 5000), nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, _, err := callEndpoint(ctx, q4Ep, "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Log actual Q4 API prompt token usage for debugging.
	t.Logf("Q4 API prompt tokens per exploration turn: %v", actualUsages)
	t.Logf("explore turns before hard threshold: %d", exploreTurns)

	// With real Q4 API, each tool result adds ~5000 tokens. The hard
	// threshold (85% of 64000 = 54400) should be hit after several turns.
	if exploreTurns < 3 {
		t.Errorf("expected at least 3 exploration turns before hard threshold, got %d", exploreTurns)
	}
}

// TestRunProposers_FailurePreservesUsage verifies that when a proposer fails,
// the tokens it consumed before failing are still reported in ProposerResult.Usage
// (task #7205 — previously failed proposers reported zero usage).
func TestRunProposers_FailurePreservesUsage(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				return "", embedded.ChatUsage{
					PromptTokens:     1500,
					CompletionTokens: 300,
					TotalTokens:      1800,
				}, errors.New("endpoint exploded after consuming tokens")
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected an error for the failed proposer")
	}
	if results[0].Usage.TotalTokens != 1800 {
		t.Errorf("expected failed proposer to preserve usage (TotalTokens=1800), got %d", results[0].Usage.TotalTokens)
	}
	if results[0].Usage.PromptTokens != 1500 {
		t.Errorf("expected PromptTokens=1500, got %d", results[0].Usage.PromptTokens)
	}
	if results[0].Usage.CompletionTokens != 300 {
		t.Errorf("expected CompletionTokens=300, got %d", results[0].Usage.CompletionTokens)
	}
}

// TestRunProposers_GateFailurePreservesUsage verifies that a proposer whose
// answer fails the content gate still reports its token usage (task #7205).
func TestRunProposers_GateFailurePreservesUsage(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				// Return tool-call markup as text — passes non-empty check
				// but fails the content gate.
				return `{"name": "read_file", "arguments": {"path": "x"}}`, embedded.ChatUsage{
					PromptTokens:     2000,
					CompletionTokens: 500,
					TotalTokens:      2500,
				}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
		},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected a gate error for tool-call markup answer")
	}
	if results[0].Usage.TotalTokens != 2500 {
		t.Errorf("expected gate-failed proposer to preserve usage (TotalTokens=2500), got %d", results[0].Usage.TotalTokens)
	}
}

// TestCallEndpoint_ConvergenceStageTimeoutTagged verifies that when forced
// convergence times out (context.DeadlineExceeded), the error is tagged with
// the stage name and prompt size so the failure line is diagnostic without
// log archaeology (task #7205, step-02).
func TestCallEndpoint_ConvergenceStageTimeoutTagged(t *testing.T) {
	// Fake chatTurn: exploration returns a tool call (so the loop proceeds),
	// then forced convergence always returns context.DeadlineExceeded.
	restore := withFakeChatTurn(func(ctx context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		if len(req.Tools) > 0 {
			// Quick exploration turn — returns a tool call.
			return toolCallMsg("get_status", `{}`), embedded.ChatUsage{}, nil
		}
		// Convergence request — simulate a timeout.
		<-ctx.Done()
		return nil, embedded.ChatUsage{}, ctx.Err()
	})
	defer restore()

	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "ok", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	// Short parent timeout so the convergence reserve is small and the test
	// completes quickly. The convergence ctx will expire with
	// DeadlineExceeded.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err == nil {
		t.Fatal("expected an error when convergence times out")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, context.DeadlineExceeded) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "convergence stage timed out") {
		t.Errorf("expected error to contain 'convergence stage timed out', got %v", err)
	}
}

// TestCallEndpoint_IdenticalToolCallRefused verifies that when a proposer
// repeatedly requests the exact same (tool, args) call, only the first
// maxIdenticalToolCalls (2) are dispatched — subsequent identical repeats are
// refused with an error message injected as the tool result, and the model
// eventually returns a final answer (task #7178).
func TestCallEndpoint_IdenticalToolCallRefused(t *testing.T) {
	// Script: 4 turns of the same tool call, then a final answer.
	fake := &fakeChatTurn{script: []scriptedTurn{
		{msg: toolCallMsg("read_file", `{"path":"x"}`)},
		{msg: toolCallMsg("read_file", `{"path":"x"}`)},
		{msg: toolCallMsg("read_file", `{"path":"x"}`)},
		{msg: toolCallMsg("read_file", `{"path":"x"}`)},
		{msg: answerMsg("final answer after repeated calls\nCONFIDENCE: 7")},
	}}
	defer withFakeChatTurn(fake.turn)()

	var dispatchCount int
	var mu sync.Mutex
	dispatch := func(name string, _ map[string]interface{}) (string, []embedded.MCPImage, error) {
		mu.Lock()
		dispatchCount++
		mu.Unlock()
		return "file content", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "read_file"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "final answer after repeated calls\nCONFIDENCE: 7" {
		t.Errorf("unexpected content: %q", content)
	}

	// 1. Only the first 2 identical calls should have been dispatched.
	mu.Lock()
	if dispatchCount != maxIdenticalToolCalls {
		t.Errorf("expected %d dispatches, got %d", maxIdenticalToolCalls, dispatchCount)
	}
	mu.Unlock()

	// 2. The 3rd and 4th turns should have "identical call" in the tool result
	//    messages that were fed back to the model. Since messages accumulate
	//    across requests, check only the last request — it contains all
	//    prior refusal messages.
	var identicalRefusals int
	if len(fake.reqs) > 0 {
		lastReq := fake.reqs[len(fake.reqs)-1]
		for _, m := range lastReq.Messages {
			if m.Role == embedded.ChatRoleTool && strings.Contains(m.Content, "identical call") {
				identicalRefusals++
			}
		}
	}
	if identicalRefusals != 2 {
		t.Errorf("expected 2 'identical call' refusals (turns 3 and 4), got %d", identicalRefusals)
	}

	// 3. The final answer should have been returned successfully (checked above
	//    by the content assertion).
}

// ─── step-05: convergence timeout salvage tests ──────────────────────────────

// TestForceFinalAnswer_SalvageOnDeadlineExceeded verifies that when chatTurn
// returns context.DeadlineExceeded but has already streamed 60+ runes of
// substantive prose via onToken, forceFinalAnswer adopts the partial answer
// with a salvage marker instead of returning an error (task #7205 step-05).
func TestForceFinalAnswer_SalvageOnDeadlineExceeded(t *testing.T) {
	// Korean prose well above the 60-rune gate threshold.
	streamedProse := "수렴 타임아웃이 발생했지만 지금까지 스트리밍된 부분 응답은 충분히 의미 있는 내용을 담고 있으므로 이를 살려서 최종 답변으로 채택해야 합니다."

	restore := withFakeChatTurn(func(_ context.Context, _ *embedded.Client, _ *embedded.ChatRequest, onToken func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		// Simulate streaming content deltas before the timeout.
		if onToken != nil {
			onToken(streamedProse)
		}
		return nil, embedded.ChatUsage{}, context.DeadlineExceeded
	})
	defer restore()

	var captured []string
	onToken := func(s string) { captured = append(captured, s) }

	content, _, err := forceFinalAnswer(context.Background(), nil, testEndpoint("ep1", "m"), nil, 0.7, 100, onToken, true)
	if err != nil {
		t.Fatalf("expected salvage to succeed (err=nil), got: %v", err)
	}
	if !strings.Contains(content, streamedProse) {
		t.Errorf("expected salvaged content to contain streamed prose, got %q", content)
	}
	if !strings.Contains(content, salvageMarker) {
		t.Errorf("expected content to contain salvageMarker %q, got %q", salvageMarker, content)
	}
	// Verify the live notification was emitted.
	foundNotify := false
	for _, s := range captured {
		if strings.Contains(s, "수렴 타임아웃") {
			foundNotify = true
			break
		}
	}
	if !foundNotify {
		t.Error("expected a '[수렴 타임아웃' notification via onToken")
	}
}

// TestForceFinalAnswer_SalvageRejectedShortProse verifies that when the
// streamed partial fails the content gate (e.g. bare tool-call markup),
// forceFinalAnswer returns the DeadlineExceeded error instead of salvaging
// (task #7205 step-05).
func TestForceFinalAnswer_SalvageRejectedShortProse(t *testing.T) {
	restore := withFakeChatTurn(func(_ context.Context, _ *embedded.Client, _ *embedded.ChatRequest, onToken func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		if onToken != nil {
			// Bare tool-call JSON — fails CheckAnswerContent.
			onToken(`{"name": "read_file", "arguments": {"path": "x"}}`)
		}
		return nil, embedded.ChatUsage{}, context.DeadlineExceeded
	})
	defer restore()

	_, _, err := forceFinalAnswer(context.Background(), nil, testEndpoint("ep1", "m"), nil, 0.7, 100, nil, true)
	if err == nil {
		t.Fatal("expected an error when partial prose fails the content gate")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

// TestForceFinalAnswer_NonDeadlineErrorNotSalvaged verifies that a non-deadline
// error (e.g. a transient API error) does NOT trigger the salvage path, even if
// substantial prose was streamed before the error (task #7205 step-05).
func TestForceFinalAnswer_NonDeadlineErrorNotSalvaged(t *testing.T) {
	longProse := "이것은 충분히 긴 프로즈이지만 에러가 deadline이 아니라면 살려지지 않아야 합니다. 일반 API 오류는 salvage 경로를 타지 않습니다."

	restore := withFakeChatTurn(func(_ context.Context, _ *embedded.Client, _ *embedded.ChatRequest, onToken func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		if onToken != nil {
			onToken(longProse)
		}
		return nil, embedded.ChatUsage{}, errors.New("boom: API error 500")
	})
	defer restore()

	_, _, err := forceFinalAnswer(context.Background(), nil, testEndpoint("ep1", "m"), nil, 0.7, 100, nil, true)
	if err == nil {
		t.Fatal("expected a non-nil error for a non-deadline failure")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected the original error 'boom', got %v", err)
	}
}

// ─── step-06: convergence diet tests ─────────────────────────────────────────

// TestCallEndpoint_ConvergenceDietCompactMessages verifies the diet path
// end-to-end: when accumulated prompt exceeds 20K tokens, a summary handoff
// fires before forced convergence, and the convergence request operates on
// the compact history [system, user, summary, directive] (task #7205 step-06).
func TestCallEndpoint_ConvergenceDietCompactMessages(t *testing.T) {
	var mu sync.Mutex
	var allReqs []*embedded.ChatRequest

	restore := withFakeChatTurn(func(_ context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		mu.Lock()
		allReqs = append(allReqs, req)
		mu.Unlock()

		if len(req.Tools) > 0 {
			// Exploration turn: return a tool call.
			return toolCallMsg("get_status", `{}`), embedded.ChatUsage{}, nil
		}

		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == embedded.ChatRoleUser && strings.Contains(lastMsg.Content, "간결하지만 완전하게 요약") {
			// Summary request — return a compact summary.
			return answerMsg("요약: 조사 내용을 압축했습니다."), embedded.ChatUsage{}, nil
		}

		// Convergence request — return the final answer.
		return answerMsg("다이어트 후 최종 답변입니다.\nCONFIDENCE: 8"), embedded.ChatUsage{}, nil
	})
	defer restore()

	// Long dispatch result to push prompt past 20K tokens.
	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return strings.Repeat("x", 100000), nil, nil // ~25K estimated tokens
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(content, "다이어트 후 최종 답변") {
		t.Errorf("expected the converged answer after diet, got %q", content)
	}

	mu.Lock()
	defer mu.Unlock()

	// Find the convergence (final) request — the last request with no tools.
	var finalReq *embedded.ChatRequest
	for i := len(allReqs) - 1; i >= 0; i-- {
		if len(allReqs[i].Tools) == 0 {
			finalReq = allReqs[i]
			break
		}
	}
	if finalReq == nil {
		t.Fatal("expected a tools-off convergence request")
	}

	// After the diet, messages should be [system, user, summary] + directive = 4.
	msgs := finalReq.Messages
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages in convergence request [system, user, summary, directive], got %d", len(msgs))
	}
	if msgs[0].Role != embedded.ChatRoleSystem {
		t.Errorf("msg[0] should be system, got %v", msgs[0].Role)
	}
	if msgs[1].Role != embedded.ChatRoleUser {
		t.Errorf("msg[1] should be user (original prompt), got %v", msgs[1].Role)
	}
	if msgs[2].Role != embedded.ChatRoleAssistant || !strings.Contains(msgs[2].Content, "요약") {
		t.Errorf("msg[2] should be assistant summary, got role=%v content=%q", msgs[2].Role, msgs[2].Content)
	}
	if msgs[3].Role != embedded.ChatRoleUser || !strings.Contains(msgs[3].Content, "추가 조사") {
		t.Errorf("msg[3] should be convergence directive, got role=%v content=%q", msgs[3].Role, msgs[3].Content)
	}

	// Verify that the convergence request is NOT carrying the bloated tool
	// result — the estimated token count should be well under 20K.
	finalEst := estimateProposerTokens(msgs, nil)
	if finalEst >= convergenceDietTokens {
		t.Errorf("convergence request should be compact (< %d tokens), got est %d", convergenceDietTokens, finalEst)
	}
}

// TestCallEndpoint_ConvergenceDietNotTriggeredBelowThreshold verifies that
// when the accumulated prompt is below convergenceDietTokens (20K), no diet
// summary request is made — the proposer converges directly on the original
// message history (task #7205 step-06).
func TestCallEndpoint_ConvergenceDietNotTriggeredBelowThreshold(t *testing.T) {
	var mu sync.Mutex
	var allReqs []*embedded.ChatRequest

	restore := withFakeChatTurn(func(ctx context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
		mu.Lock()
		allReqs = append(allReqs, req)
		mu.Unlock()

		if len(req.Tools) > 0 {
			// Block until per-turn timeout fires — clean break to forced
			// convergence after a single exploration turn. This keeps the
			// accumulated prompt well below convergenceDietTokens.
			<-ctx.Done()
			return nil, embedded.ChatUsage{}, ctx.Err()
		}

		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == embedded.ChatRoleUser && strings.Contains(lastMsg.Content, "간결하지만 완전하게 요약") {
			t.Error("diet summary request should not be made when prompt is below threshold")
			return answerMsg("should not happen"), embedded.ChatUsage{}, nil
		}

		return answerMsg("직접 수렴한 답변입니다.\nCONFIDENCE: 6"), embedded.ChatUsage{}, nil
	})
	defer restore()

	// Short dispatch result — keeps total prompt well under 20K tokens.
	dispatch := func(string, map[string]interface{}) (string, []embedded.MCPImage, error) {
		return "short result", nil, nil
	}
	tools := []embedded.OpenAIToolDef{{Type: "function", Function: embedded.OpenAIFunction{Name: "get_status"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	content, _, err := callEndpoint(ctx, testEndpoint("ep1", "m"), "sys", "user", 0.7, 100, nil, tools, dispatch, "", "sheep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(content, "직접 수렴한 답변") {
		t.Errorf("expected the converged answer without diet, got %q", content)
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify no summary request was made. Every non-tool request should be
	// the convergence request (containing the convergence directive).
	for _, req := range allReqs {
		if len(req.Tools) == 0 {
			lastMsg := req.Messages[len(req.Messages)-1]
			if strings.Contains(lastMsg.Content, "간결하지만 완전하게 요약") {
				t.Error("found an unexpected diet summary request")
			}
		}
	}
}

// TestRunProposers_PerSlotTimeoutOverride verifies that a proposer with a
// non-zero Spec.Timeout gets a tighter deadline than the global opts.Timeout,
// while a proposer with zero Spec.Timeout inherits the global value.
func TestRunProposers_PerSlotTimeoutOverride(t *testing.T) {
	var mu sync.Mutex
	deadlines := make(map[string]time.Time)

	// deadlineFake records the deadline from the context it receives.
	deadlineFake := func(ctx context.Context, ep EndpointRef, _, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
		mu.Lock()
		defer mu.Unlock()
		dl, ok := ctx.Deadline()
		if ok {
			deadlines[ep.ID] = dl
		}
		return "", embedded.ChatUsage{}, ctx.Err() // let it timeout
	}

	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": deadlineFake,
			"ep2": deadlineFake,
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior", Timeout: 1 * time.Minute},
			{Endpoint: testEndpoint("ep2", "llama-3.3-70b"), PersonaKey: "balthasar"}, // no per-slot timeout
		},
		BaseSystem:  "You are a code reviewer.",
		UserPrompts: []string{"review this", "review this"},
		Timeout:     10 * time.Minute,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled so RunProposers returns immediately

	RunProposers(ctx, opts)

	mu.Lock()
	defer mu.Unlock()

	if len(deadlines) < 2 {
		t.Fatalf("expected at least 2 deadlines recorded, got %d: %v", len(deadlines), deadlines)
	}

	now := time.Now()

	// Slot 0 should have ~1 minute deadline.
	dl0, ok := deadlines["ep1"]
	if !ok {
		t.Fatal("ep1 deadline not recorded")
	}
	diff0 := dl0.Sub(now)
	if diff0 < 30*time.Second || diff0 > 90*time.Second {
		t.Errorf("slot 0 deadline should be ~1min from now, got %v", diff0)
	}

	// Slot 1 should have ~10 minute deadline.
	dl1, ok := deadlines["ep2"]
	if !ok {
		t.Fatal("ep2 deadline not recorded")
	}
	diff1 := dl1.Sub(now)
	if diff1 < 9*time.Minute || diff1 > 11*time.Minute {
		t.Errorf("slot 1 deadline should be ~10min from now, got %v", diff1)
	}
}

// ─── step-09: reask helper + confidence nudge tests ────────────────────

// longKoreanProse returns a string of Korean prose longer than 60 runes,
// without a CONFIDENCE line — suitable for testing the confidence nudge path.
func longKoreanProse(prefix string) string {
	var b strings.Builder
	b.WriteString(prefix)
	for i := 0; i < 20; i++ {
		b.WriteString("이것은 한국어 산문 답변의 일부입니다. ")
	}
	return b.String()
}

// TestConfidenceReask_ReplacesAnswerOnSuccess verifies that when a gate-passing
// answer lacks CONFIDENCE, reaskProposer is called once and a successful
// restated answer with confidence replaces the original.
func TestConfidenceReask_ReplacesAnswerOnSuccess(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake(longKoreanProse("원래 답변입니다. ")),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	reaskCalls := 0
	reaskRestore := installFakeReask(func(_ context.Context, _ ProposerSpec, _, _, _, _ string, _ time.Duration, _, _ string, _ func(string)) (string, embedded.ChatUsage, error) {
		reaskCalls++
		return "수정된 최종 답변입니다. " + longKoreanProse("") + "\nCONFIDENCE: 8", embedded.ChatUsage{PromptTokens: 50, CompletionTokens: 20}, nil
	})
	defer reaskRestore()

	opts := RunProposersOptions{
		Proposers:   []ProposerSpec{{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	if reaskCalls != 1 {
		t.Errorf("reask calls = %d, want 1", reaskCalls)
	}
	if results[0].Err != nil {
		t.Fatalf("unexpected error: %v", results[0].Err)
	}
	if results[0].Confidence != 8 {
		t.Errorf("confidence = %d, want 8", results[0].Confidence)
	}
	if !strings.Contains(results[0].Answer, "수정된") {
		t.Errorf("answer should contain '수정된', got %q", results[0].Answer)
	}
	// Confidence reask must count as ExtraCalls so orchestrator totalCalls
	// matches the abstain-reask policy (task #7234).
	if results[0].ExtraCalls != 1 {
		t.Errorf("ExtraCalls = %d, want 1 (confidence reask)", results[0].ExtraCalls)
	}
	if results[0].Slot != 0 {
		t.Errorf("Slot = %d, want 0", results[0].Slot)
	}
}

// TestConfidenceReask_KeepsAnswerOnError verifies that a reask failure leaves
// the original answer intact with confidence -1 and no error.
func TestConfidenceReask_KeepsAnswerOnError(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake(longKoreanProse("원래 답변입니다. ")),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	reaskRestore := installFakeReask(func(_ context.Context, _ ProposerSpec, _, _, _, _ string, _ time.Duration, _, _ string, _ func(string)) (string, embedded.ChatUsage, error) {
		return "", embedded.ChatUsage{PromptTokens: 10}, errors.New("reask transport error")
	})
	defer reaskRestore()

	opts := RunProposersOptions{
		Proposers:   []ProposerSpec{{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	if results[0].Err != nil {
		t.Fatalf("unexpected error: %v", results[0].Err)
	}
	if results[0].Confidence != -1 {
		t.Errorf("confidence = %d, want -1 (original preserved)", results[0].Confidence)
	}
	if !strings.Contains(results[0].Answer, "원래 답변") {
		t.Errorf("answer should contain original text, got %q", results[0].Answer)
	}
}

// TestConfidenceReask_KeepsAnswerOnGateFail verifies that when the reask result
// passes the content gate but lacks confidence, the original answer is kept.
// Also covers the case where the reask result fails the gate entirely.
func TestConfidenceReask_KeepsAnswerOnGateFail(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake(longKoreanProse("원래 답변입니다. ")),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	reaskRestore := installFakeReask(func(_ context.Context, _ ProposerSpec, _, _, _, _ string, _ time.Duration, _, _ string, _ func(string)) (string, embedded.ChatUsage, error) {
		// Return a gate-failing response (tool-call markup as text).
		return `{"tool_calls":[{"function":{"name":"read_file"}}]}`, embedded.ChatUsage{}, nil
	})
	defer reaskRestore()

	opts := RunProposersOptions{
		Proposers:   []ProposerSpec{{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	if results[0].Err != nil {
		t.Fatalf("unexpected error: %v", results[0].Err)
	}
	if results[0].Confidence != -1 {
		t.Errorf("confidence = %d, want -1 (gate-failed reask should not replace)", results[0].Confidence)
	}
	if !strings.Contains(results[0].Answer, "원래 답변") {
		t.Errorf("answer should contain original text, got %q", results[0].Answer)
	}
}

// TestConfidenceReask_SkipsSalvagedAnswer verifies that a salvaged partial
// answer (containing salvageMarker) does not trigger a reask.
func TestConfidenceReask_SkipsSalvagedAnswer(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake(longKoreanProse("부분 답변. ") + "\n\n" + salvageMarker),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	reaskCalls := 0
	reaskRestore := installFakeReask(func(_ context.Context, _ ProposerSpec, _, _, _, _ string, _ time.Duration, _, _ string, _ func(string)) (string, embedded.ChatUsage, error) {
		reaskCalls++
		return "should not be called", embedded.ChatUsage{}, nil
	})
	defer reaskRestore()

	opts := RunProposersOptions{
		Proposers:   []ProposerSpec{{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	if reaskCalls != 0 {
		t.Errorf("reask calls = %d, want 0 (salvaged answers are exempt)", reaskCalls)
	}
	if results[0].Err != nil {
		t.Fatalf("unexpected error: %v", results[0].Err)
	}
	if results[0].Confidence != -1 {
		t.Errorf("confidence = %d, want -1", results[0].Confidence)
	}
}

// TestConfidenceReask_NotCalledWhenReported verifies that an answer with a
// CONFIDENCE line does not trigger a reask.
func TestConfidenceReask_NotCalledWhenReported(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("proper answer\nCONFIDENCE: 7"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	reaskCalls := 0
	reaskRestore := installFakeReask(func(_ context.Context, _ ProposerSpec, _, _, _, _ string, _ time.Duration, _, _ string, _ func(string)) (string, embedded.ChatUsage, error) {
		reaskCalls++
		return "should not be called", embedded.ChatUsage{}, nil
	})
	defer reaskRestore()

	opts := RunProposersOptions{
		Proposers:   []ProposerSpec{{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	RunProposers(context.Background(), opts)

	if reaskCalls != 0 {
		t.Errorf("reask calls = %d, want 0 (confidence was reported)", reaskCalls)
	}
}

// TestConfidenceReask_UsageAccumulated verifies that tokens consumed by a
// failed reask are still accumulated into result.Usage (step-01 principle).
func TestConfidenceReask_UsageAccumulated(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
				return longKoreanProse("답변. "), embedded.ChatUsage{PromptTokens: 100, CompletionTokens: 50}, nil
			},
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	reaskUsage := embedded.ChatUsage{PromptTokens: 30, CompletionTokens: 10}
	reaskRestore := installFakeReask(func(_ context.Context, _ ProposerSpec, _, _, _, _ string, _ time.Duration, _, _ string, _ func(string)) (string, embedded.ChatUsage, error) {
		return "", reaskUsage, errors.New("reask failed")
	})
	defer reaskRestore()

	opts := RunProposersOptions{
		Proposers:   []ProposerSpec{{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}},
		BaseSystem:  "test",
		UserPrompts: []string{"prompt"},
		Timeout:     5 * time.Second,
	}

	results := RunProposers(context.Background(), opts)

	expectedPrompt := int64(100 + 30)
	expectedCompletion := int64(50 + 10)
	if results[0].Usage.PromptTokens != expectedPrompt {
		t.Errorf("PromptTokens = %d, want %d (original + reask)", results[0].Usage.PromptTokens, expectedPrompt)
	}
	if results[0].Usage.CompletionTokens != expectedCompletion {
		t.Errorf("CompletionTokens = %d, want %d (original + reask)", results[0].Usage.CompletionTokens, expectedCompletion)
	}
}

// TestReaskBudget verifies the reask budget calculation table.
func TestReaskBudget(t *testing.T) {
	tests := []struct {
		name      string
		input     time.Duration
		want      time.Duration
	}{
		{"60s → 30s (fraction floor clamp)", 60 * time.Second, 30 * time.Second},
		{"300s → 100s", 300 * time.Second, 100 * time.Second},
		{"600s → 120s (max clamp)", 600 * time.Second, 120 * time.Second},
		{"0 → 30s (min clamp)", 0, 30 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := reaskBudget(tc.input)
			if got != tc.want {
				t.Errorf("reaskBudget(%v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestReaskProposer_EmbeddedPromptAndBudget exercises the real reaskProposer
// with a fake chatTurn to verify prompt construction, tools absence, budget
// deadline, and parent-ctx cancellation.
func TestReaskProposer_EmbeddedPromptAndBudget(t *testing.T) {
	t.Run("prompt_contains_task_prevanswer_directive", func(t *testing.T) {
		var capturedReq *embedded.ChatRequest
		restore := withFakeChatTurn(func(_ context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
			capturedReq = req
			return &embedded.ChatMessage{Role: embedded.ChatRoleAssistant, Content: "replied\nCONFIDENCE: 7"}, embedded.ChatUsage{}, nil
		})
		defer restore()

		spec := ProposerSpec{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}
		budget := 60 * time.Second
		content, _, err := reaskProposer(context.Background(), spec, "sys-prompt", "task-prompt", "prev-answer", confidenceReaskDirective, budget, "/path", "sheep", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content != "replied\nCONFIDENCE: 7" {
			t.Errorf("content = %q, want replied", content)
		}
		if capturedReq == nil {
			t.Fatal("no request captured")
		}
		userMsg := capturedReq.Messages[1].Content
		if !strings.Contains(userMsg, "[원 태스크]") {
			t.Error("user prompt should contain [원 태스크]")
		}
		if !strings.Contains(userMsg, "task-prompt") {
			t.Error("user prompt should contain the task prompt")
		}
		if !strings.Contains(userMsg, "너의 이전 답변:") {
			t.Error("user prompt should contain previous answer header")
		}
		if !strings.Contains(userMsg, "prev-answer") {
			t.Error("user prompt should contain the previous answer")
		}
		if !strings.Contains(userMsg, confidenceReaskDirective) {
			t.Error("user prompt should contain the directive")
		}
	})

	t.Run("tools_nil", func(t *testing.T) {
		var capturedReq *embedded.ChatRequest
		restore := withFakeChatTurn(func(_ context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
			capturedReq = req
			return &embedded.ChatMessage{Role: embedded.ChatRoleAssistant, Content: "ok\nCONFIDENCE: 5"}, embedded.ChatUsage{}, nil
		})
		defer restore()

		spec := ProposerSpec{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}
		reaskProposer(context.Background(), spec, "sys", "task", "prev", confidenceReaskDirective, 60*time.Second, "", "", nil)

		if capturedReq == nil {
			t.Fatal("no request captured")
		}
		if capturedReq.Tools != nil {
			t.Errorf("Tools should be nil in reask request, got %d tools", len(capturedReq.Tools))
		}
	})

	t.Run("ctx_deadline_matches_budget", func(t *testing.T) {
		var capturedCtx context.Context
		restore := withFakeChatTurn(func(ctx context.Context, _ *embedded.Client, req *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
			capturedCtx = ctx
			return &embedded.ChatMessage{Role: embedded.ChatRoleAssistant, Content: "ok\nCONFIDENCE: 5"}, embedded.ChatUsage{}, nil
		})
		defer restore()

		spec := ProposerSpec{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}
		budget := 90 * time.Second
		reaskProposer(context.Background(), spec, "sys", "task", "prev", confidenceReaskDirective, budget, "", "", nil)

		dl, has := capturedCtx.Deadline()
		if !has {
			t.Fatal("chatTurn ctx should have a deadline")
		}
		diff := time.Until(dl)
		if diff < budget-time.Second || diff > budget+time.Second {
			t.Errorf("ctx deadline ~%v from now, want ~%v (1s tolerance)", diff, budget)
		}
	})

	t.Run("cancelled_parent_ctx_aborts_immediately", func(t *testing.T) {
		called := false
		restore := withFakeChatTurn(func(_ context.Context, _ *embedded.Client, _ *embedded.ChatRequest, _ func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
			called = true
			return &embedded.ChatMessage{Role: embedded.ChatRoleAssistant}, embedded.ChatUsage{}, nil
		})
		defer restore()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		spec := ProposerSpec{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"}
		_, _, err := reaskProposer(ctx, spec, "sys", "task", "prev", confidenceReaskDirective, 60*time.Second, "", "", nil)
		if err == nil {
			t.Error("expected error on cancelled parent ctx")
		}
		if called {
			t.Error("chatTurn should not be called when parent ctx is already cancelled")
		}
	})
}

// TestRunProposers_SkipSlot verifies that a slot marked Skip does not get a
// backend call and carries the errSlotSkipped sentinel.
func TestRunProposers_SkipSlot(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("Answer A\nCONFIDENCE: 8"),
			"ep2": okFake("Answer B\nCONFIDENCE: 7"),
			"ep3": okFake("Answer C\nCONFIDENCE: 9"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	opts := RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior"},
			{Endpoint: testEndpoint("ep2", "llama-3.3-70b"), PersonaKey: "balthasar"},
			{Endpoint: testEndpoint("ep3", "mistral-small"), PersonaKey: "casper"},
		},
		BaseSystem:  "base system",
		UserPrompts: []string{"task"},
		Timeout:     5 * time.Second,
		Skip:        []bool{false, true, false},
	}

	results := RunProposers(context.Background(), opts)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Slot 1 should carry the sentinel error.
	if !errors.Is(results[1].Err, errSlotSkipped) {
		t.Errorf("slot 1 err: expected errSlotSkipped, got %v", results[1].Err)
	}

	// Slots 0 and 2 should succeed normally.
	if results[0].Err != nil {
		t.Errorf("slot 0 unexpected error: %v", results[0].Err)
	}
	if results[0].Answer != "Answer A" {
		t.Errorf("slot 0 answer: expected 'Answer A', got %q", results[0].Answer)
	}
	if results[2].Err != nil {
		t.Errorf("slot 2 unexpected error: %v", results[2].Err)
	}
	if results[2].Answer != "Answer C" {
		t.Errorf("slot 2 answer: expected 'Answer C', got %q", results[2].Answer)
	}

	// Only 2 backend calls (slot 1 skipped).
	if fake.calls != 2 {
		t.Errorf("backend calls = %d, want 2 (slot 1 skipped)", fake.calls)
	}
}
