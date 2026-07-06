package magi

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBuildDebatePrompt verifies that the debate prompt:
// 1. Contains the proposer's own answer
// 2. Labels peer answers as "심의자 A/B" without persona names or model names
// 3. Includes the anti-conformity instruction
// 4. Includes the agreement axis when provided
func TestBuildDebatePrompt(t *testing.T) {
	results := []ProposerResult{
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior"},
			Answer:     "첫 번째 심의자의 답변입니다.",
			Confidence: 8,
		},
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep2", "llama-3.3-70b"), PersonaKey: "balthasar"},
			Answer:     "두 번째 심의자의 답변입니다.",
			Confidence: 6,
		},
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep3", "mistral-small"), PersonaKey: "casper"},
			Answer:     "세 번째 심의자의 답변입니다.",
			Confidence: 9,
		},
	}

	prompt := BuildDebatePrompt("원본 태스크 프롬프트", results, 0, "마이그레이션 순서 쟁점")

	// 1. Own answer included.
	if !strings.Contains(prompt, "첫 번째 심의자의 답변입니다.") {
		t.Error("prompt should contain the proposer's own answer")
	}
	if !strings.Contains(prompt, "너의 이전 답변:") {
		t.Error("prompt should have '너의 이전 답변:' header")
	}

	// 2. Peer answers anonymized — no persona names or model names in labels.
	if !strings.Contains(prompt, "심의자 A") {
		t.Error("prompt should label first peer as '심의자 A'")
	}
	if !strings.Contains(prompt, "심의자 B") {
		t.Error("prompt should label second peer as '심의자 B'")
	}
	// Persona names must not appear as labels/headers.
	if strings.Contains(prompt, "### BALTHASAR") {
		t.Error("prompt must not use persona name 'BALTHASAR' as a peer label")
	}
	if strings.Contains(prompt, "### CASPER") {
		t.Error("prompt must not use persona name 'CASPER' as a peer label")
	}
	if strings.Contains(prompt, "llama-3.3-70b") {
		t.Error("prompt must not contain model name 'llama-3.3-70b'")
	}
	if strings.Contains(prompt, "mistral-small") {
		t.Error("prompt must not contain model name 'mistral-small'")
	}

	// Peer answer content is still visible (identity hidden, content shown).
	if !strings.Contains(prompt, "두 번째 심의자의 답변입니다.") {
		t.Error("peer answer content should be present")
	}
	if !strings.Contains(prompt, "세 번째 심의자의 답변입니다.") {
		t.Error("peer answer content should be present")
	}

	// 3. Anti-conformity instruction.
	if !strings.Contains(prompt, "동의 자체는 목표가 아니다") {
		t.Error("prompt should contain anti-conformity instruction")
	}
	if !strings.Contains(prompt, "근거가 유지되면 답을 바꾸지 마라") {
		t.Error("prompt should contain 'do not change if justified' instruction")
	}
	if !strings.Contains(prompt, "CONFIDENCE:") {
		t.Error("prompt should request CONFIDENCE line")
	}

	// 4. Agreement axis included.
	if !strings.Contains(prompt, "쟁점:") {
		t.Error("prompt should contain agreement axis header")
	}
	if !strings.Contains(prompt, "마이그레이션 순서 쟁점") {
		t.Error("prompt should contain the agreement axis text")
	}
}

// TestBuildDebatePrompt_NoAxis verifies that an empty agreementAxis is
// omitted from the prompt.
func TestBuildDebatePrompt_NoAxis(t *testing.T) {
	results := []ProposerResult{
		{
			Spec:   ProposerSpec{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
			Answer: "answer 1",
		},
		{
			Spec:   ProposerSpec{Endpoint: testEndpoint("ep2", "model-b"), PersonaKey: "balthasar"},
			Answer: "answer 2",
		},
	}

	prompt := BuildDebatePrompt("task prompt", results, 0, "")

	if strings.Contains(prompt, "쟁점:") {
		t.Error("prompt should not contain agreement axis when empty")
	}
}

// TestBuildDebatePrompt_TwoPeers verifies correct labeling with exactly two
// peers (slot 1 of 3 — peers are slot 0 and slot 2).
func TestBuildDebatePrompt_TwoPeers(t *testing.T) {
	results := []ProposerResult{
		{
			Spec:   ProposerSpec{Endpoint: testEndpoint("ep1", "model-a"), PersonaKey: "melchior"},
			Answer: "answer from slot 0",
		},
		{
			Spec:   ProposerSpec{Endpoint: testEndpoint("ep2", "model-b"), PersonaKey: "balthasar"},
			Answer: "answer from slot 1 (self)",
		},
		{
			Spec:   ProposerSpec{Endpoint: testEndpoint("ep3", "model-c"), PersonaKey: "casper"},
			Answer: "answer from slot 2",
		},
	}

	prompt := BuildDebatePrompt("task", results, 1, "")

	if !strings.Contains(prompt, "심의자 A") {
		t.Error("first peer should be labeled '심의자 A'")
	}
	if !strings.Contains(prompt, "심의자 B") {
		t.Error("second peer should be labeled '심의자 B'")
	}
	if !strings.Contains(prompt, "answer from slot 0") {
		t.Error("first peer answer should be present")
	}
	if !strings.Contains(prompt, "answer from slot 2") {
		t.Error("second peer answer should be present")
	}
	if !strings.Contains(prompt, "answer from slot 1 (self)") {
		t.Error("self answer should be present under '너의 이전 답변:'")
	}
}

// TestRunDebateRound_AllSuccess verifies that when all proposers succeed in
// the debate round, answers are replaced with new ones.
func TestRunDebateRound_AllSuccess(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("Revised answer A\nCONFIDENCE: 9"),
			"ep2": okFake("Revised answer B\nCONFIDENCE: 7"),
			"ep3": okFake("Revised answer C\nCONFIDENCE: 8"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	// Round-1 results (all successful — only successful slots are passed).
	round1 := []ProposerResult{
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior"},
			Answer:     "Original answer A\nCONFIDENCE: 8",
			Confidence: 8,
		},
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep2", "llama-3.3-70b"), PersonaKey: "balthasar"},
			Answer:     "Original answer B\nCONFIDENCE: 6",
			Confidence: 6,
		},
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep3", "mistral-small"), PersonaKey: "casper"},
			Answer:     "Original answer C\nCONFIDENCE: 9",
			Confidence: 9,
		},
	}

	opts := RunProposersOptions{
		BaseSystem:  "base system prompt",
		Temperature: 0.7,
		Timeout:     5 * time.Second,
	}

	results := RunDebateRound(context.Background(), opts, round1, "마이그레이션 순서 쟁점", "original task")

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// All slots should have revised answers.
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("slot %d unexpected error: %v", i, r.Err)
			continue
		}
		if !strings.HasPrefix(r.Answer, "Revised answer") {
			t.Errorf("slot %d answer: expected revised answer, got %q", i, r.Answer)
		}
	}

	// Verify confidence values were extracted.
	if results[0].Confidence != 9 {
		t.Errorf("slot 0 confidence: expected 9, got %d", results[0].Confidence)
	}
	if results[1].Confidence != 7 {
		t.Errorf("slot 1 confidence: expected 7, got %d", results[1].Confidence)
	}
	if results[2].Confidence != 8 {
		t.Errorf("slot 2 confidence: expected 8, got %d", results[2].Confidence)
	}
}

// TestRunDebateRound_OneFails verifies that when one proposer fails in the
// debate round, its round-1 answer is preserved.
func TestRunDebateRound_OneFails(t *testing.T) {
	fake := &fakeCallEndpoint{
		funcs: map[string]fakeFunc{
			"ep1": okFake("Revised answer A\nCONFIDENCE: 9"),
			"ep2": errFake("debate round timeout"),
			"ep3": okFake("Revised answer C\nCONFIDENCE: 8"),
		},
	}
	restore := withFakeCallEndpoint(fake)
	defer restore()

	var outputLines []string
	onOutput := func(s string) { outputLines = append(outputLines, s) }

	round1 := []ProposerResult{
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep1", "qwen3-27b"), PersonaKey: "melchior"},
			Answer:     "Original answer A",
			Confidence: 8,
		},
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep2", "llama-3.3-70b"), PersonaKey: "balthasar"},
			Answer:     "Original answer B",
			Confidence: 6,
		},
		{
			Spec:       ProposerSpec{Endpoint: testEndpoint("ep3", "mistral-small"), PersonaKey: "casper"},
			Answer:     "Original answer C",
			Confidence: 9,
		},
	}

	opts := RunProposersOptions{
		BaseSystem:  "base system prompt",
		Temperature: 0.7,
		Timeout:     5 * time.Second,
		OnOutput:    onOutput,
	}

	results := RunDebateRound(context.Background(), opts, round1, "", "original task")

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Slot 0 — revised.
	if results[0].Err != nil {
		t.Errorf("slot 0 unexpected error: %v", results[0].Err)
	} else if results[0].Answer != "Revised answer A" {
		t.Errorf("slot 0 answer: expected 'Revised answer A', got %q", results[0].Answer)
	}

	// Slot 1 — failed in debate, should keep round-1 answer.
	if results[1].Err != nil {
		t.Errorf("slot 1 should have Err cleared (round-1 fallback), got err: %v", results[1].Err)
	}
	if results[1].Answer != "Original answer B" {
		t.Errorf("slot 1 answer: expected round-1 'Original answer B', got %q", results[1].Answer)
	}
	if results[1].Confidence != 6 {
		t.Errorf("slot 1 confidence: expected round-1 value 6, got %d", results[1].Confidence)
	}

	// Slot 2 — revised.
	if results[2].Err != nil {
		t.Errorf("slot 2 unexpected error: %v", results[2].Err)
	} else if results[2].Answer != "Revised answer C" {
		t.Errorf("slot 2 answer: expected 'Revised answer C', got %q", results[2].Answer)
	}

	// Verify the fallback warning was emitted.
	foundWarning := false
	for _, line := range outputLines {
		if strings.Contains(line, "토론 라운드 실패") && strings.Contains(line, "1라운드 답변 유지") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Error("expected fallback warning for slot 1 in live output")
	}
}

// TestDeadlockResult_WithDissent verifies the deadlock output format when a
// dissent is present.
func TestDeadlockResult_WithDissent(t *testing.T) {
	v := &Verdict{
		Synthesis: "다수안 종합 답변입니다.",
		Dissent:   "소수의견: 다른 접근을 주장합니다.",
	}

	result := DeadlockResult(v)

	if !strings.Contains(result, "⚠️ MAGI 교착 (2:1)") {
		t.Error("should contain deadlock header")
	}
	if !strings.Contains(result, "다수안을 채택하되 소수의견을 병기합니다") {
		t.Error("should contain casting vote explanation")
	}
	if !strings.Contains(result, "다수안 종합 답변입니다.") {
		t.Error("should contain synthesis")
	}
	if !strings.Contains(result, "📎 소수의견:") {
		t.Error("should contain dissent header")
	}
	if !strings.Contains(result, "소수의견: 다른 접근을 주장합니다.") {
		t.Error("should contain dissent text")
	}
}

// TestDeadlockResult_NoDissent verifies that an empty dissent omits the
// minority opinion block.
func TestDeadlockResult_NoDissent(t *testing.T) {
	v := &Verdict{
		Synthesis: "다수안 종합 답변입니다.",
		Dissent:   "",
	}

	result := DeadlockResult(v)

	if !strings.Contains(result, "⚠️ MAGI 교착 (2:1)") {
		t.Error("should contain deadlock header")
	}
	if !strings.Contains(result, "다수안 종합 답변입니다.") {
		t.Error("should contain synthesis")
	}
	if strings.Contains(result, "📎 소수의견:") {
		t.Error("should not contain dissent block when empty")
	}
	if strings.Contains(result, "---") {
		t.Error("should not contain separator when dissent is empty")
	}
}
