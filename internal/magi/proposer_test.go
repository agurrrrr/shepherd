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
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("expected empty-response error, got %v", err)
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
	if _, ok := convergenceCutoff(context.Background()); ok {
		t.Error("expected no cutoff for a deadline-less context")
	}

	// Generous budget → reserve is 1/4 (below the half cap, above the floor).
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	cutoff, ok := convergenceCutoff(ctx)
	if !ok {
		t.Fatal("expected a cutoff for a deadline context")
	}
	dl, _ := ctx.Deadline()
	reserve := dl.Sub(cutoff)
	// reserve should be ~50s (200/4). Allow small slack for scheduling drift
	// between context creation and the cutoff computation.
	if reserve < 49*time.Second || reserve > 51*time.Second {
		t.Errorf("expected ~50s reserve, got %v", reserve)
	}
}
