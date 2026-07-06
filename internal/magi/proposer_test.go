package magi

import (
	"context"
	"errors"
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
func withFakeCallEndpoint(fake *fakeCallEndpoint) func() {
	orig := callEndpoint
	callEndpoint = fake.call
	return func() { callEndpoint = orig }
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
// preserving order.
func TestSuccessfulResults(t *testing.T) {
	results := []ProposerResult{
		{Spec: ProposerSpec{}, Answer: "A", Confidence: 8, Err: nil},
		{Spec: ProposerSpec{}, Answer: "", Confidence: -1, Err: errors.New("timeout")},
		{Spec: ProposerSpec{}, Answer: "C", Confidence: 6, Err: nil},
	}

	successful := SuccessfulResults(results)

	if len(successful) != 2 {
		t.Fatalf("expected 2 successful results, got %d", len(successful))
	}
	if successful[0].Answer != "A" || successful[1].Answer != "C" {
		t.Errorf("order not preserved")
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
