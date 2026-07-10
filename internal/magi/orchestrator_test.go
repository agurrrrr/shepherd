package magi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
)

// ─── Test infrastructure ──────────────────────────────────────────────

// fakeReaskFunc is the signature of a single fake reaskProposer function.
type fakeReaskFunc func(ctx context.Context, spec ProposerSpec, systemPrompt, taskPrompt, prevAnswer, directive string, budget time.Duration, projectPath, sheepName string, onToken func(string)) (string, embedded.ChatUsage, error)

// dualFake combines endpoint and aggregator fakes with full tracking.
// Endpoint results are stored per-ID as sequential slices: index 0 = round 1,
// index 1 = debate round. Aggregator results are purely sequential.
type dualFake struct {
	mu        sync.Mutex
	epCount   map[string]int
	epTotal   int
	aggTotal  int
	epResults map[string][]fakeFunc
	aggFuncs  []fakeAggregatorFunc

	// reaskFuncs: sequential fake reaskProposer functions (step-09/10).
	// When empty, reaskProposer returns an error (default — protects existing
	// tests from accidental real reask calls).
	reaskFuncs []fakeReaskFunc
	reaskTotal int
}

func newDualFake() *dualFake {
	return &dualFake{
		epCount:   make(map[string]int),
		epResults: make(map[string][]fakeFunc),
	}
}

func (d *dualFake) setEndpoint(id string, fns ...fakeFunc) {
	d.epResults[id] = fns
}

func (d *dualFake) setAggregator(fns ...fakeAggregatorFunc) {
	d.aggFuncs = fns
}

// setReask installs sequential fake reaskProposer functions (step-10).
func (d *dualFake) setReask(fns ...fakeReaskFunc) {
	d.reaskFuncs = fns
}

func (d *dualFake) install() func() {
	origEp := callEndpoint
	origAgg := aggregatorComplete
	origReask := reaskProposer
	callEndpoint = d.epCall
	aggregatorComplete = d.aggCall
	reaskProposer = d.reaskCall
	return func() {
		callEndpoint = origEp
		aggregatorComplete = origAgg
		reaskProposer = origReask
	}
}

func (d *dualFake) epCall(ctx context.Context, ep EndpointRef, systemPrompt, userPrompt string, temperature float32, maxTokens int, onToken func(string), tools []embedded.OpenAIToolDef, dispatch embedded.MCPDispatcher, projectPath, sheepName string) (string, embedded.ChatUsage, error) {
	d.mu.Lock()
	d.epTotal++
	idx := d.epCount[ep.ID]
	d.epCount[ep.ID]++
	funcs := d.epResults[ep.ID]
	d.mu.Unlock()

	if idx >= len(funcs) || funcs[idx] == nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("no fake for %s round %d", ep.ID, idx)
	}
	return funcs[idx](ctx, ep, systemPrompt, userPrompt, temperature, maxTokens, onToken, tools, dispatch, projectPath, sheepName)
}

func (d *dualFake) aggCall(ctx context.Context, spec AggregatorSpec, systemPrompt, userPrompt string) (string, embedded.ChatUsage, error) {
	d.mu.Lock()
	d.aggTotal++
	idx := d.aggTotal - 1
	d.mu.Unlock()

	if idx >= len(d.aggFuncs) || d.aggFuncs[idx] == nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("no aggregator fake for call %d", idx+1)
	}
	return d.aggFuncs[idx](ctx, spec, systemPrompt, userPrompt)
}

func (d *dualFake) reaskCall(ctx context.Context, spec ProposerSpec, systemPrompt, taskPrompt, prevAnswer, directive string, budget time.Duration, projectPath, sheepName string, onToken func(string)) (string, embedded.ChatUsage, error) {
	d.mu.Lock()
	d.reaskTotal++
	idx := d.reaskTotal - 1
	d.mu.Unlock()

	if idx >= len(d.reaskFuncs) || d.reaskFuncs[idx] == nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("no reask fake for call %d", idx+1)
	}
	return d.reaskFuncs[idx](ctx, spec, systemPrompt, taskPrompt, prevAnswer, directive, budget, projectPath, sheepName, onToken)
}

// ─── Fake helpers ─────────────────────────────────────────────────────

func okUsage(answer string, u embedded.ChatUsage) fakeFunc {
	return func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
		return answer, u, nil
	}
}

func aggJSON(jsonStr string, u embedded.ChatUsage) fakeAggregatorFunc {
	return func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
		return jsonStr, u, nil
	}
}

func aggBroken(u embedded.ChatUsage) fakeAggregatorFunc {
	return func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
		return "이것은 JSON이 아닙니다.", u, nil
	}
}

// ─── Verdict JSON constants ───────────────────────────────────────────

const (
	jsonUnanimousHigh       = `{"verdict":"unanimous","agreement_axis":"모두 동일한 접근","synthesis":"종합 답변입니다.","dissent":"","confidence":9}`
	jsonMajorityDissent     = `{"verdict":"majority","agreement_axis":"두 명 동의","synthesis":"다수안 종합 답변입니다.","dissent":"소수 의견입니다.","confidence":8}`
	jsonSplitLow            = `{"verdict":"split","agreement_axis":"세 답 모두 상이함","synthesis":"임시 종합 답변입니다.","dissent":"모두 다른 접근을 주장함","confidence":5}`
	jsonMajorityLowConf     = `{"verdict":"majority","agreement_axis":"두 명 동의","synthesis":"저신뢰 다수안입니다.","dissent":"소수 의견","confidence":4}`
	jsonUnanimousPostDebate = `{"verdict":"unanimous","agreement_axis":"토론 후 합의","synthesis":"토론 후 종합 답변입니다.","dissent":"","confidence":9}`
	jsonSplitPostDebate     = `{"verdict":"split","agreement_axis":"여전히 분열","synthesis":"토론 후 임시 종합입니다.","dissent":"여전히 의견 상이","confidence":5}`
	jsonSplitAbstained      = `{"verdict":"split","agreement_axis":"유효표 부족","synthesis":"임시 종합.","dissent":"CASPER-3 기권 처리","confidence":5,"abstained":["CASPER-3"]}`
)

// ─── Standard options builder ─────────────────────────────────────────

func stdOptions() Options {
	return Options{
		TaskPrompt:          "test task prompt",
		BaseSystem:          "base system prompt",
		Proposers:           stdProposers(),
		Aggregator:          AggregatorSpec{Type: "endpoint"},
		ConfidenceThreshold: 7,
		MaxDebateRounds:     1,
		ProposerTimeout:     5 * time.Second,
	}
}

func stdProposers() []ProposerSpec {
	return []ProposerSpec{
		{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior"},
		{Endpoint: testEndpoint("ep2", "llama-3.3-70b"), PersonaKey: "balthasar"},
		{Endpoint: testEndpoint("ep3", "mistral-small"), PersonaKey: "casper"},
	}
}

// ─── Tests ────────────────────────────────────────────────────────────

// Test 1: 평상 경로 — 3 성공 + unanimous/신뢰도9 → 토론 없이 synthesis 채택,
// 호출 수 = 4 (proposer 3 + judge 1).
func TestRun_UnanimousNoDebate(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage("Answer A\nCONFIDENCE: 8", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", okUsage("Answer B\nCONFIDENCE: 6", embedded.ChatUsage{}))
	fake.setEndpoint("ep3", okUsage("Answer C\nCONFIDENCE: 9", embedded.ChatUsage{}))
	fake.setAggregator(aggJSON(jsonUnanimousHigh, embedded.ChatUsage{}))
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
	if result.Result != "종합 답변입니다." {
		t.Errorf("result = %q, want synthesis text", result.Result)
	}

	if fake.epTotal != 3 {
		t.Errorf("endpoint calls = %d, want 3", fake.epTotal)
	}
	if fake.aggTotal != 1 {
		t.Errorf("aggregator calls = %d, want 1", fake.aggTotal)
	}

	found := false
	for _, line := range out {
		if strings.Contains(line, "✅ 합의 도달") {
			found = true
			break
		}
	}
	if !found {
		t.Error("output should contain '✅ 합의 도달'")
	}
}

// Test 2: majority + dissent → synthesis 뒤 소수의견 병기.
func TestRun_MajorityWithDissent(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage("Answer A\nCONFIDENCE: 7", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", okUsage("Answer B\nCONFIDENCE: 8", embedded.ChatUsage{}))
	fake.setEndpoint("ep3", okUsage("Answer C\nCONFIDENCE: 6", embedded.ChatUsage{}))
	fake.setAggregator(aggJSON(jsonMajorityDissent, embedded.ChatUsage{}))
	restore := fake.install()
	defer restore()

	opts := stdOptions()
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}

	if !strings.Contains(result.Result, "다수안 종합 답변입니다.") {
		t.Errorf("result should contain synthesis, got %q", result.Result)
	}
	if !strings.Contains(result.Result, "📎 소수의견:") {
		t.Error("result should contain minority opinion suffix")
	}
	if !strings.Contains(result.Result, "소수 의견입니다.") {
		t.Error("result should contain the dissent text")
	}

	if fake.epTotal != 3 {
		t.Errorf("endpoint calls = %d, want 3 (no debate)", fake.epTotal)
	}
	if fake.aggTotal != 1 {
		t.Errorf("aggregator calls = %d, want 1", fake.aggTotal)
	}
}

// Test 3: split → 토론 → 합의 → 호출 수 = 8 (3+1+3+1).
func TestRun_SplitToDebateToConsensus(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage("Round1 A\nCONFIDENCE: 7", embedded.ChatUsage{}),
		okUsage("Revised A\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage("Round1 B\nCONFIDENCE: 6", embedded.ChatUsage{}),
		okUsage("Revised B\nCONFIDENCE: 8", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep3",
		okUsage("Round1 C\nCONFIDENCE: 5", embedded.ChatUsage{}),
		okUsage("Revised C\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setAggregator(
		aggJSON(jsonSplitLow, embedded.ChatUsage{}),
		aggJSON(jsonUnanimousPostDebate, embedded.ChatUsage{}),
	)
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
	if result.Result != "토론 후 종합 답변입니다." {
		t.Errorf("result = %q, want post-debate synthesis", result.Result)
	}

	totalCalls := fake.epTotal + fake.aggTotal
	if totalCalls != 8 {
		t.Errorf("total calls = %d (ep=%d agg=%d), want 8", totalCalls, fake.epTotal, fake.aggTotal)
	}
	if fake.epTotal != 6 {
		t.Errorf("endpoint calls = %d, want 6", fake.epTotal)
	}
	if fake.aggTotal != 2 {
		t.Errorf("aggregator calls = %d, want 2", fake.aggTotal)
	}

	foundDebate := false
	foundConsensus := false
	for _, line := range out {
		if strings.Contains(line, "토론 라운드 진입") {
			foundDebate = true
		}
		if strings.Contains(line, "✅ 합의 도달") {
			foundConsensus = true
		}
	}
	if !foundDebate {
		t.Error("output should contain debate entry message")
	}
	if !foundConsensus {
		t.Error("output should contain consensus message after debate")
	}
}

// Test 4: split → 토론 → 여전히 split → 교착 헤더 + 소수의견 병기.
func TestRun_SplitToDebateToDeadlock(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage("Round1 A\nCONFIDENCE: 7", embedded.ChatUsage{}),
		okUsage("Revised A\nCONFIDENCE: 8", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage("Round1 B\nCONFIDENCE: 6", embedded.ChatUsage{}),
		okUsage("Revised B\nCONFIDENCE: 7", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep3",
		okUsage("Round1 C\nCONFIDENCE: 5", embedded.ChatUsage{}),
		okUsage("Revised C\nCONFIDENCE: 6", embedded.ChatUsage{}),
	)
	fake.setAggregator(
		aggJSON(jsonSplitLow, embedded.ChatUsage{}),
		aggJSON(jsonSplitPostDebate, embedded.ChatUsage{}),
	)
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false even in deadlock")
	}

	if !strings.Contains(result.Result, "⚠️ MAGI 교착") {
		t.Error("result should contain deadlock header")
	}
	if !strings.Contains(result.Result, "토론 후 임시 종합입니다.") {
		t.Error("result should contain synthesis from deadlock verdict")
	}
	if !strings.Contains(result.Result, "📎 소수의견:") {
		t.Error("result should contain minority opinion in deadlock")
	}

	totalCalls := fake.epTotal + fake.aggTotal
	if totalCalls != 8 {
		t.Errorf("total calls = %d (ep=%d agg=%d), want 8", totalCalls, fake.epTotal, fake.aggTotal)
	}
}

// Test 5: 저신뢰(majority, confidence 4 < 7) → 토론 진입 (DOWN 게이트).
func TestRun_LowConfidenceTriggersDebate(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage("Round1 A\nCONFIDENCE: 5", embedded.ChatUsage{}),
		okUsage("Revised A\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage("Round1 B\nCONFIDENCE: 4", embedded.ChatUsage{}),
		okUsage("Revised B\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep3",
		okUsage("Round1 C\nCONFIDENCE: 3", embedded.ChatUsage{}),
		okUsage("Revised C\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setAggregator(
		aggJSON(jsonMajorityLowConf, embedded.ChatUsage{}),
		aggJSON(jsonUnanimousPostDebate, embedded.ChatUsage{}),
	)
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}

	if fake.epTotal != 6 {
		t.Errorf("endpoint calls = %d, want 6 (debate round entered)", fake.epTotal)
	}
	if fake.aggTotal != 2 {
		t.Errorf("aggregator calls = %d, want 2 (re-judge after debate)", fake.aggTotal)
	}

	foundDebate := false
	for _, line := range out {
		if strings.Contains(line, "토론 라운드 진입") {
			foundDebate = true
			break
		}
	}
	if !foundDebate {
		t.Error("low confidence should trigger debate entry")
	}
}

// Test 6: 성공 1개 → ErrInsufficientProposers.
func TestRun_InsufficientProposers(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage("Only answer\nCONFIDENCE: 8", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", errFake("timeout"))
	fake.setEndpoint("ep3", errFake("connection refused"))
	fake.setAggregator(aggJSON(jsonUnanimousHigh, embedded.ChatUsage{}))
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if !errors.Is(err, ErrInsufficientProposers) {
		t.Fatalf("expected ErrInsufficientProposers, got %v", err)
	}
	if result != nil {
		t.Error("result should be nil on insufficient proposers")
	}

	found := false
	for _, line := range out {
		if strings.Contains(line, "심의 불가") {
			found = true
			break
		}
	}
	if !found {
		t.Error("output should contain '심의 불가' warning")
	}

	if fake.aggTotal != 0 {
		t.Errorf("aggregator calls = %d, want 0", fake.aggTotal)
	}
}

// Test 7: 판정 2회 모두 파싱 실패 → SideBySideFallback 정상 완료.
func TestRun_JudgeParseFailureFallback(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage("Answer A\nCONFIDENCE: 8", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", okUsage("Answer B\nCONFIDENCE: 7", embedded.ChatUsage{}))
	fake.setEndpoint("ep3", okUsage("Answer C\nCONFIDENCE: 9", embedded.ChatUsage{}))
	fake.setAggregator(
		aggBroken(embedded.ChatUsage{}),
		aggBroken(embedded.ChatUsage{}),
	)
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.Incomplete {
		t.Error("Incomplete should be false for side-by-side fallback")
	}

	if !strings.Contains(result.Result, "⚠️ MAGI 판정 실패") {
		t.Error("result should contain fallback header")
	}
	if !strings.Contains(result.Result, "MELCHIOR-1") {
		t.Error("result should contain persona name MELCHIOR-1")
	}

	if fake.aggTotal != 2 {
		t.Errorf("aggregator calls = %d, want 2", fake.aggTotal)
	}
}

// Test 8: MaxDebateRounds=0 + split → 토론 없이 즉시 종결.
func TestRun_MaxDebateZeroSplitImmediate(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage("Answer A\nCONFIDENCE: 7", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", okUsage("Answer B\nCONFIDENCE: 6", embedded.ChatUsage{}))
	fake.setEndpoint("ep3", okUsage("Answer C\nCONFIDENCE: 5", embedded.ChatUsage{}))
	fake.setAggregator(aggJSON(jsonSplitLow, embedded.ChatUsage{}))
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.MaxDebateRounds = 0
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}

	if fake.epTotal != 3 {
		t.Errorf("endpoint calls = %d, want 3 (no debate)", fake.epTotal)
	}
	if fake.aggTotal != 1 {
		t.Errorf("aggregator calls = %d, want 1", fake.aggTotal)
	}

	if !strings.Contains(result.Result, "⚠️ MAGI 교착") {
		t.Error("result should contain deadlock header for split without debate")
	}

	foundNoDebate := false
	for _, line := range out {
		if strings.Contains(line, "토론 미설정") {
			foundNoDebate = true
			break
		}
	}
	if !foundNoDebate {
		t.Error("output should mention '토론 미설정'")
	}
}

// Test 9: 토큰 합산이 fake usage 합과 일치.
func TestRun_TokenSummation(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage("Answer A\nCONFIDENCE: 8",
		embedded.ChatUsage{PromptTokens: 1000, CompletionTokens: 200}))
	fake.setEndpoint("ep2", okUsage("Answer B\nCONFIDENCE: 7",
		embedded.ChatUsage{PromptTokens: 1100, CompletionTokens: 250}))
	fake.setEndpoint("ep3", okUsage("Answer C\nCONFIDENCE: 9",
		embedded.ChatUsage{PromptTokens: 1200, CompletionTokens: 300}))
	fake.setAggregator(aggJSON(jsonUnanimousHigh,
		embedded.ChatUsage{PromptTokens: 5000, CompletionTokens: 500}))
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPrompt := int64(1000 + 1100 + 1200 + 5000)
	expectedCompletion := int64(200 + 250 + 300 + 500)

	if result.PromptTokens != expectedPrompt {
		t.Errorf("PromptTokens = %d, want %d", result.PromptTokens, expectedPrompt)
	}
	if result.CompletionTokens != expectedCompletion {
		t.Errorf("CompletionTokens = %d, want %d", result.CompletionTokens, expectedCompletion)
	}

	if result.CostUSD != 0 {
		t.Errorf("CostUSD = %f, want 0", result.CostUSD)
	}

	foundCost := false
	for _, line := range out {
		if strings.Contains(line, "📊 MAGI 심의 비용:") {
			foundCost = true
			expectedTokens := expectedPrompt + expectedCompletion
			expectedStr := fmt.Sprintf("%d 토큰", expectedTokens)
			if !strings.Contains(line, expectedStr) {
				t.Errorf("cost line should contain %q, got %q", expectedStr, line)
			}
			break
		}
	}
	if !foundCost {
		t.Error("output should contain cost summary line")
	}
}

// Test 10 (bonus): 성공 2개 → 경고 출력 후 계속.
func TestRun_TwoSuccessContinues(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage("Answer A\nCONFIDENCE: 8", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", okUsage("Answer B\nCONFIDENCE: 7", embedded.ChatUsage{}))
	fake.setEndpoint("ep3", errFake("timeout"))
	fake.setAggregator(aggJSON(jsonUnanimousHigh, embedded.ChatUsage{}))
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}

	foundWarning := false
	for _, line := range out {
		if strings.Contains(line, "심의자 1명 이탈") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Error("output should contain member departure warning")
	}

	if fake.aggTotal != 1 {
		t.Errorf("aggregator calls = %d, want 1", fake.aggTotal)
	}
}

// Test 11 (bonus): 개시 출력이 페르소나 이름으로 조합됨.
func TestRun_InitOutputWithPersonaNames(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage("A\nCONFIDENCE: 8", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", okUsage("B\nCONFIDENCE: 7", embedded.ChatUsage{}))
	fake.setEndpoint("ep3", okUsage("C\nCONFIDENCE: 9", embedded.ChatUsage{}))
	fake.setAggregator(aggJSON(jsonUnanimousHigh, embedded.ChatUsage{}))
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	Run(context.Background(), opts)

	if len(out) == 0 {
		t.Fatal("no output captured")
	}

	firstLine := out[0]
	for _, name := range []string{"MELCHIOR-1", "BALTHASAR-2", "CASPER-3"} {
		if !strings.Contains(firstLine, name) {
			t.Errorf("init line should contain %q, got %q", name, firstLine)
		}
	}
}

// ─── isAcceptable unit tests (step-03: unanimous threshold-1 exception) ──

func TestIsAcceptable_UnanimousThresholdMinusOne(t *testing.T) {
	v := &Verdict{Verdict: "unanimous", Confidence: 8}
	if !isAcceptable(v, 9) {
		t.Error("unanimous confidence=8 threshold=9 should be acceptable (threshold-1 exception)")
	}
}

func TestIsAcceptable_UnanimousBelowThresholdMinusOne(t *testing.T) {
	v := &Verdict{Verdict: "unanimous", Confidence: 7}
	if isAcceptable(v, 9) {
		t.Error("unanimous confidence=7 threshold=9 should NOT be acceptable")
	}
}

func TestIsAcceptable_MajorityBelowThreshold(t *testing.T) {
	v := &Verdict{Verdict: "majority", Confidence: 8}
	if isAcceptable(v, 9) {
		t.Error("majority confidence=8 threshold=9 should NOT be acceptable (no exception for majority)")
	}
}

func TestIsAcceptable_MajorityAtThreshold(t *testing.T) {
	v := &Verdict{Verdict: "majority", Confidence: 9}
	if !isAcceptable(v, 9) {
		t.Error("majority confidence=9 threshold=9 should be acceptable")
	}
}

func TestIsAcceptable_SplitRejected(t *testing.T) {
	v := &Verdict{Verdict: "split", Confidence: 10}
	if isAcceptable(v, 9) {
		t.Error("split should never be acceptable regardless of confidence")
	}
}

func TestIsAcceptable_NilVerdict(t *testing.T) {
	if isAcceptable(nil, 9) {
		t.Error("nil verdict should not be acceptable")
	}
}

// ─── step-10: abstain second chance tests ─────────────────────────────

// reaskOK returns a fake reask function that produces a gate-passing answer
// with a confidence line.
func reaskOK(answer string, conf int) fakeReaskFunc {
	return func(_ context.Context, _ ProposerSpec, _, _, _, _ string, _ time.Duration, _, _ string, _ func(string)) (string, embedded.ChatUsage, error) {
		return answer + fmt.Sprintf("\nCONFIDENCE: %d", conf), embedded.ChatUsage{}, nil
	}
}

func reaskErr(msg string) fakeReaskFunc {
	return func(_ context.Context, _ ProposerSpec, _, _, _, _ string, _ time.Duration, _, _ string, _ func(string)) (string, embedded.ChatUsage, error) {
		return "", embedded.ChatUsage{}, errors.New(msg)
	}
}

// longAnswer builds a Korean prose block comfortably above minSubstantiveRunes.
func longAnswer(text string) string {
	return text + strings.Repeat("내용입니다. ", 10)
}

// TestRun_AbstainSecondChanceToConsensus: 1차 판정 split + abstained CASPER-3,
// reask 성공 → 재판정 unanimous → 토론 없이 종결.
func TestRun_AbstainSecondChanceToConsensus(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage(longAnswer("Answer A")+"\nCONFIDENCE: 8", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", okUsage(longAnswer("Answer B")+"\nCONFIDENCE: 7", embedded.ChatUsage{}))
	fake.setEndpoint("ep3", okUsage(longAnswer("Answer C")+"\nCONFIDENCE: 5", embedded.ChatUsage{}))
	fake.setAggregator(
		aggJSON(jsonSplitAbstained, embedded.ChatUsage{}),
		aggJSON(jsonUnanimousHigh, embedded.ChatUsage{}),
	)
	fake.setReask(reaskOK(longAnswer("Revised C"), 8))
	restore := fake.install()
	defer restore()

	opts := stdOptions()
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
	if result.Result != "종합 답변입니다." {
		t.Errorf("result = %q, want synthesis from re-judge", result.Result)
	}
	if fake.epTotal != 3 {
		t.Errorf("endpoint calls = %d, want 3 (no debate)", fake.epTotal)
	}
	if fake.aggTotal != 2 {
		t.Errorf("aggregator calls = %d, want 2 (1st + re-judge)", fake.aggTotal)
	}
	if fake.reaskTotal != 1 {
		t.Errorf("reask calls = %d, want 1", fake.reaskTotal)
	}
}

// TestRun_AbstainReaskFailsDebateProceeds: reask 에러 → 재판정 없이 토론 진행.
func TestRun_AbstainReaskFailsDebateProceeds(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage(longAnswer("Round1 A")+"\nCONFIDENCE: 7", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised A")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage(longAnswer("Round1 B")+"\nCONFIDENCE: 6", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised B")+"\nCONFIDENCE: 8", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep3",
		okUsage(longAnswer("Round1 C")+"\nCONFIDENCE: 5", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised C")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setAggregator(
		aggJSON(jsonSplitAbstained, embedded.ChatUsage{}),
		aggJSON(jsonUnanimousPostDebate, embedded.ChatUsage{}),
	)
	fake.setReask(reaskErr("backend down"))
	restore := fake.install()
	defer restore()

	opts := stdOptions()
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
	// 1st judge + post-debate re-judge = 2 aggregator calls (no re-judge from second chance).
	if fake.aggTotal != 2 {
		t.Errorf("aggregator calls = %d, want 2", fake.aggTotal)
	}
	// 3 round1 + 2 debate (ep3/CASPER-3 skipped — abstained) = 5 endpoint calls.
	if fake.epTotal != 5 {
		t.Errorf("endpoint calls = %d, want 5", fake.epTotal)
	}
}

// TestRun_AbstainRejudgeStillSplitGoesToDebate: reask 성공 → 재판정도 split → 토론 진행.
func TestRun_AbstainRejudgeStillSplitGoesToDebate(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage(longAnswer("Round1 A")+"\nCONFIDENCE: 7", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised A")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage(longAnswer("Round1 B")+"\nCONFIDENCE: 6", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised B")+"\nCONFIDENCE: 8", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep3",
		okUsage(longAnswer("Round1 C")+"\nCONFIDENCE: 5", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised C")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setAggregator(
		aggJSON(jsonSplitAbstained, embedded.ChatUsage{}),
		aggJSON(jsonSplitLow, embedded.ChatUsage{}),
		aggJSON(jsonUnanimousPostDebate, embedded.ChatUsage{}),
	)
	fake.setReask(reaskOK(longAnswer("Revised C"), 8))
	restore := fake.install()
	defer restore()

	opts := stdOptions()
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
	if result.Result != "토론 후 종합 답변입니다." {
		t.Errorf("result = %q, want post-debate synthesis", result.Result)
	}
	// 1st judge + re-judge + post-debate re-judge = 3 aggregator calls.
	if fake.aggTotal != 3 {
		t.Errorf("aggregator calls = %d, want 3", fake.aggTotal)
	}
}

// TestRun_AbstainUnknownNameIgnored: abstained에 미지 이름 → reask 호출 0회.
func TestRun_AbstainUnknownNameIgnored(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage(longAnswer("Round1 A")+"\nCONFIDENCE: 7", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised A")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage(longAnswer("Round1 B")+"\nCONFIDENCE: 6", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised B")+"\nCONFIDENCE: 8", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep3",
		okUsage(longAnswer("Round1 C")+"\nCONFIDENCE: 5", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised C")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setAggregator(
		aggJSON(`{"verdict":"split","agreement_axis":"유효표 부족","synthesis":"임시 종합.","dissent":"기권","confidence":5,"abstained":["누군지모름"]}`, embedded.ChatUsage{}),
		aggJSON(jsonUnanimousPostDebate, embedded.ChatUsage{}),
	)
	fake.setReask(reaskOK(longAnswer("Should not be called"), 8))
	restore := fake.install()
	defer restore()

	opts := stdOptions()
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
	if fake.reaskTotal != 0 {
		t.Errorf("reask calls = %d, want 0 (unknown name should not trigger reask)", fake.reaskTotal)
	}
}

// TestRun_AbstainNotTriggeredWhenAcceptable: acceptable verdict에 abstained가 있어도 재기회 미발동.
func TestRun_AbstainNotTriggeredWhenAcceptable(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1", okUsage(longAnswer("Answer A")+"\nCONFIDENCE: 8", embedded.ChatUsage{}))
	fake.setEndpoint("ep2", okUsage(longAnswer("Answer B")+"\nCONFIDENCE: 7", embedded.ChatUsage{}))
	fake.setEndpoint("ep3", okUsage(longAnswer("Answer C")+"\nCONFIDENCE: 9", embedded.ChatUsage{}))
	// unanimous confidence 9 ≥ threshold-1=6 → acceptable, but has abstained field.
	fake.setAggregator(aggJSON(`{"verdict":"unanimous","agreement_axis":"모두 동일","synthesis":"종합 답변입니다.","dissent":"","confidence":9,"abstained":["CASPER-3"]}`, embedded.ChatUsage{}))
	fake.setReask(reaskOK(longAnswer("Should not be called"), 8))
	restore := fake.install()
	defer restore()

	opts := stdOptions()
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
	if fake.reaskTotal != 0 {
		t.Errorf("reask calls = %d, want 0 (acceptable verdict should not trigger second chance)", fake.reaskTotal)
	}
	if fake.aggTotal != 1 {
		t.Errorf("aggregator calls = %d, want 1 (no re-judge)", fake.aggTotal)
	}
}

// TestRun_RejudgeBackendErrorKeepsFirstVerdict: reask 성공 + 재판정 백엔드 에러 → 1차 verdict 유지.
func TestRun_RejudgeBackendErrorKeepsFirstVerdict(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage(longAnswer("Round1 A")+"\nCONFIDENCE: 7", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised A")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage(longAnswer("Round1 B")+"\nCONFIDENCE: 6", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised B")+"\nCONFIDENCE: 8", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep3",
		okUsage(longAnswer("Round1 C")+"\nCONFIDENCE: 5", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised C")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setAggregator(
		aggJSON(jsonSplitAbstained, embedded.ChatUsage{}),
		func(_ context.Context, _ AggregatorSpec, _, _ string) (string, embedded.ChatUsage, error) {
			return "", embedded.ChatUsage{}, errors.New("backend error")
		},
		aggJSON(jsonUnanimousPostDebate, embedded.ChatUsage{}),
	)
	fake.setReask(reaskOK(longAnswer("Revised C"), 8))
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v (should not hard-fail on rejudge backend error)", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}

	// The pipeline should proceed to debate with the original verdict.
	foundWarning := false
	for _, line := range out {
		if strings.Contains(line, "재기회 재판정 실패") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Error("output should contain '재기회 재판정 실패' warning")
	}

	// The updated slot name should be pruned from Abstained — but since the
	// pipeline continues to debate and re-judge, we just verify it didn't
	// hard-fail. The final result should come from the post-debate re-judge.
	if result.Result != "토론 후 종합 답변입니다." {
		t.Errorf("result = %q, want post-debate synthesis", result.Result)
	}
}

// TestMatchAbstainedSlots verifies exact matching, whitespace trimming,
// unknown name ignoring, and duplicate name single-counting.
func TestMatchAbstainedSlots(t *testing.T) {
	specs := stdProposers()
	results := make([]ProposerResult, len(specs))
	for i, s := range specs {
		results[i] = ProposerResult{Spec: s, Slot: i}
	}

	t.Run("exact match", func(t *testing.T) {
		skip, count := matchAbstainedSlots([]string{"CASPER-3"}, results)
		if count != 1 || !skip[2] || skip[0] || skip[1] {
			t.Errorf("skip=%v count=%d, want skip[2]=true count=1", skip, count)
		}
	})

	t.Run("whitespace trimmed", func(t *testing.T) {
		skip, count := matchAbstainedSlots([]string{"  CASPER-3  "}, results)
		if count != 1 || !skip[2] {
			t.Errorf("whitespace-padded name should still match: skip=%v count=%d", skip, count)
		}
	})

	t.Run("unknown name ignored", func(t *testing.T) {
		skip, count := matchAbstainedSlots([]string{"누군지모름"}, results)
		if count != 0 {
			t.Errorf("unknown name should not match: count=%d", count)
		}
		for i := range skip {
			if skip[i] {
				t.Errorf("skip[%d] should be false for unknown name", i)
			}
		}
	})

	t.Run("duplicate name counted once", func(t *testing.T) {
		skip, count := matchAbstainedSlots([]string{"CASPER-3", "CASPER-3"}, results)
		if count != 1 || !skip[2] {
			t.Errorf("duplicate name should count once: skip=%v count=%d", skip, count)
		}
	})

	t.Run("multiple names", func(t *testing.T) {
		skip, count := matchAbstainedSlots([]string{"MELCHIOR-1", "BALTHASAR-2"}, results)
		if count != 2 || !skip[0] || !skip[1] || skip[2] {
			t.Errorf("two names should match two slots: skip=%v count=%d", skip, count)
		}
	})
}

// ─── step-11: debate lightening tests ─────────────────────────────────

// TestRun_DebateExcludesAbstainedSlot: 1차 판정 split + abstained CASPER-3,
// reask 에러(기권 유지) → 토론 진행되나 ep3는 스킵되어 1회만 호출.
func TestRun_DebateExcludesAbstainedSlot(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage(longAnswer("Round1 A")+"\nCONFIDENCE: 7", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised A")+"\nCONFIDENCE: 9", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage(longAnswer("Round1 B")+"\nCONFIDENCE: 6", embedded.ChatUsage{}),
		okUsage(longAnswer("Revised B")+"\nCONFIDENCE: 8", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep3",
		okUsage(longAnswer("Round1 C")+"\nCONFIDENCE: 5", embedded.ChatUsage{}),
		// ep3 should NOT get a second call (skipped in debate).
	)
	fake.setAggregator(
		aggJSON(jsonSplitAbstained, embedded.ChatUsage{}),
		aggJSON(jsonUnanimousPostDebate, embedded.ChatUsage{}),
	)
	fake.setReask(reaskErr("backend down")) // reask fails → abstain stays
	restore := fake.install()
	defer restore()

	opts := stdOptions()
	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
	if result.Result != "토론 후 종합 답변입니다." {
		t.Errorf("result = %q, want post-debate synthesis", result.Result)
	}

	// ep3 should only be called once (round 1) — skipped in debate.
	if fake.epCount["ep3"] != 1 {
		t.Errorf("ep3 calls = %d, want 1 (skipped in debate)", fake.epCount["ep3"])
	}
	// ep1 and ep2 should be called twice (round 1 + debate).
	if fake.epCount["ep1"] != 2 {
		t.Errorf("ep1 calls = %d, want 2", fake.epCount["ep1"])
	}
	if fake.epCount["ep2"] != 2 {
		t.Errorf("ep2 calls = %d, want 2", fake.epCount["ep2"])
	}
}

// TestRun_DebateSkippedWhenActiveBelowTwo: 2 successful proposers, one
// abstained → only 1 active deliberator → debate skipped, deadlock result.
func TestRun_DebateSkippedWhenActiveBelowTwo(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage(longAnswer("Round1 A")+"\nCONFIDENCE: 7", embedded.ChatUsage{}),
	)
	fake.setEndpoint("ep2",
		okUsage(longAnswer("Round1 B")+"\nCONFIDENCE: 6", embedded.ChatUsage{}),
	)
	// ep3 fails in round 1 → successful has only 2 slots (ep1, ep2).
	fake.setEndpoint("ep3",
		errFake("timeout"),
	)
	fake.setAggregator(
		// 1st judge: split with CASPER-3 abstained. But CASPER-3 is not in
		// successful (it failed in round 1), so matchAbstainedSlots finds
		// no match → skipCount=0. We need to abstain one of the remaining two.
		aggJSON(`{"verdict":"split","agreement_axis":"유효표 부족","synthesis":"임시 종합.","dissent":"기권","confidence":5,"abstained":["BALTHASAR-2"]}`, embedded.ChatUsage{}),
	)
	fake.setReask(reaskErr("backend down")) // reask fails → abstain stays
	restore := fake.install()
	defer restore()

	var out []string
	opts := stdOptions()
	opts.OnOutput = func(s string) { out = append(out, s) }

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}

	// Result should be a deadlock.
	if !strings.Contains(result.Result, "MAGI 교착") {
		t.Errorf("result should contain 'MAGI 교착', got %q", result.Result)
	}

	// Only round-1 endpoint calls (3), no debate.
	if fake.epTotal != 3 {
		t.Errorf("endpoint calls = %d, want 3 (no debate)", fake.epTotal)
	}
	// Only 1 aggregator call (1st judge, no re-judge since reask failed
	// and no re-judge happens when no answers were updated).
	if fake.aggTotal != 1 {
		t.Errorf("aggregator calls = %d, want 1", fake.aggTotal)
	}

	// Output should mention debate skip.
	foundSkip := false
	for _, line := range out {
		if strings.Contains(line, "토론을 생략하고 교착 처리로 종결") {
			foundSkip = true
			break
		}
	}
	if !foundSkip {
		t.Error("output should mention debate skip due to < 2 active deliberators")
	}
}

// TestRun_DebateHasNoTools verifies that the debate round does not receive
// ToolDefs/ToolDispatch even when the orchestrator's Options has them set.
func TestRun_DebateHasNoTools(t *testing.T) {
	fake := newDualFake()
	fake.setEndpoint("ep1",
		okUsage(longAnswer("Round1 A")+"\nCONFIDENCE: 7", embedded.ChatUsage{}),
		// Debate round fake: capture tools to verify they are empty.
		func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), tools []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
			if len(tools) > 0 {
				t.Errorf("debate round should have no tools, got %d", len(tools))
			}
			return longAnswer("Revised A") + "\nCONFIDENCE: 9", embedded.ChatUsage{}, nil
		},
	)
	fake.setEndpoint("ep2",
		okUsage(longAnswer("Round1 B")+"\nCONFIDENCE: 6", embedded.ChatUsage{}),
		func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), tools []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
			if len(tools) > 0 {
				t.Errorf("debate round should have no tools, got %d", len(tools))
			}
			return longAnswer("Revised B") + "\nCONFIDENCE: 8", embedded.ChatUsage{}, nil
		},
	)
	fake.setEndpoint("ep3",
		okUsage(longAnswer("Round1 C")+"\nCONFIDENCE: 5", embedded.ChatUsage{}),
		func(_ context.Context, _ EndpointRef, _, _ string, _ float32, _ int, _ func(string), tools []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
			if len(tools) > 0 {
				t.Errorf("debate round should have no tools, got %d", len(tools))
			}
			return longAnswer("Revised C") + "\nCONFIDENCE: 9", embedded.ChatUsage{}, nil
		},
	)
	fake.setAggregator(
		aggJSON(jsonSplitLow, embedded.ChatUsage{}),
		aggJSON(jsonUnanimousPostDebate, embedded.ChatUsage{}),
	)
	restore := fake.install()
	defer restore()

	opts := stdOptions()
	opts.ToolDefs = []embedded.OpenAIToolDef{
		{Type: "function", Function: embedded.OpenAIFunction{Name: "read_file"}},
	}
	opts.ToolDispatch = nil // no real dispatch needed — just verifying absence

	result, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Incomplete {
		t.Error("Incomplete should be false")
	}
}
