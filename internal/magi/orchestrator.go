package magi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
)

// Options bundles everything the pipeline needs. The wiring layer (worker/
// server) resolves config and prompts; this package stays dependency-free.
type Options struct {
	SheepName           string
	TaskPrompt          string        // the user's task prompt
	BaseSystem          string        // base system prompt (BuildSystemPromptForEmbedded output)
	Proposers           []ProposerSpec
	Aggregator          AggregatorSpec
	ConfidenceThreshold int           // default 7 (caller applies config defaults)
	MaxDebateRounds     int           // 0 = never debate, 1 = design default
	ProposerTimeout     time.Duration // per-proposer (default 120s)
	OnOutput            func(string)  // live output sink, may be nil
}

// ErrInsufficientProposers signals that fewer than 2 proposers answered.
// The wiring layer falls back to a single embedded run (design §5.1).
var ErrInsufficientProposers = errors.New("magi: fewer than 2 proposers succeeded")

// Run executes the advisory consensus pipeline (design §5) and returns an
// embedded.ExecuteResult so the worker wiring can reuse the existing
// conversion path.
//
// Pipeline: Round 1 → Judge → (DOWN gate) → Debate → Re-Judge → Final.
// Every path that produces an answer returns Incomplete=false — true
// failures are propagated as errors (design §5.3, lesson from #7000).
func Run(ctx context.Context, opts Options) (*embedded.ExecuteResult, error) {
	emit := opts.OnOutput
	if emit == nil {
		emit = func(string) {}
	}

	var totalUsage embedded.ChatUsage
	totalCalls := 0

	// ── 1. 개시 출력 ──────────────────────────────────────────────
	names := make([]string, len(opts.Proposers))
	for i, p := range opts.Proposers {
		names[i] = PersonaDisplayName(p, i)
	}
	emit(fmt.Sprintf("🧠 MAGI 심의 개시 — %s\n", strings.Join(names, "·")))

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 2. Round 1: 블라인드 병렬 제안 ────────────────────────────
	timeout := opts.ProposerTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	userPrompts := make([]string, len(opts.Proposers))
	for i := range userPrompts {
		userPrompts[i] = opts.TaskPrompt
	}

	round1 := RunProposers(ctx, RunProposersOptions{
		Proposers:   opts.Proposers,
		BaseSystem:  opts.BaseSystem,
		UserPrompts: userPrompts,
		Timeout:     timeout,
		OnOutput:    emit,
	})
	totalCalls += len(opts.Proposers)

	// Collect usage from round 1.
	for _, r := range round1 {
		totalUsage.PromptTokens += r.Usage.PromptTokens
		totalUsage.CompletionTokens += r.Usage.CompletionTokens
		totalUsage.TotalTokens += r.Usage.TotalTokens
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 3. 성공 수 확인 ────────────────────────────────────────────
	successful := SuccessfulResults(round1)
	successCount := len(successful)

	if successCount <= 1 {
		emit(fmt.Sprintf("⚠️ MAGI 심의 불가 (성공 응답 %d/%d) — 단일 임베디드 실행으로 폴백합니다\n",
			successCount, len(opts.Proposers)))
		return nil, ErrInsufficientProposers
	}

	if successCount == 2 {
		emit("⚠️ 심의자 1명 이탈 — 2인 심의로 계속합니다\n")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 4. 판정 (1차) ─────────────────────────────────────────────
	verdict, judgeUsage, judgeCalls, err := Judge(ctx, opts.Aggregator, successful, opts.TaskPrompt, emit)
	totalUsage.PromptTokens += judgeUsage.PromptTokens
	totalUsage.CompletionTokens += judgeUsage.CompletionTokens
	totalUsage.TotalTokens += judgeUsage.TotalTokens
	totalCalls += judgeCalls

	if err != nil {
		return nil, fmt.Errorf("magi judge failed: %w", err)
	}

	// Verdict == nil → 판정 불능 → SideBySideFallback으로 정상 완료.
	if verdict == nil {
		result := finalize(SideBySideFallback(successful), totalUsage, totalCalls, emit)
		return result, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 5. DOWN 게이트 (설계서 §2.4, §5.3) ────────────────────────
	adopted := false
	finalText := ""

	if isAcceptable(verdict, opts.ConfidenceThreshold) {
		// 채택 경로.
		adopted = true
		finalText = buildAdoptedText(verdict)
		emit(fmt.Sprintf("✅ 합의 도달 (%s, 신뢰도 %d/10) — 종합 응답 채택\n",
			verdict.Verdict, verdict.Confidence))
	} else {
		// split 또는 저신뢰.
		if opts.MaxDebateRounds == 0 {
			// 토론 없이 교착 처리로 종결.
			finalText = DeadlockResult(verdict)
			emit(fmt.Sprintf("⚖️ 합의 판정: %s, 신뢰도 %d/10 — 토론 미설정으로 종결\n",
				verdict.Verdict, verdict.Confidence))
			return finalize(finalText, totalUsage, totalCalls, emit), nil
		}

		// 토론 진입.
		emit(fmt.Sprintf("⚖️ 합의 판정: %s, 신뢰도 %d/10 — 토론 라운드 진입\n",
			verdict.Verdict, verdict.Confidence))
		if verdict.AgreementAxis != "" {
			emit(fmt.Sprintf("  (쟁점: %s)\n", verdict.AgreementAxis))
		}
	}

	if adopted {
		return finalize(finalText, totalUsage, totalCalls, emit), nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 6. 토론 라운드 (최대 1회 — Phase 1 클램프) ─────────────────
	// Phase 1: debate is clamped to exactly 1 round even when
	// MaxDebateRounds > 1. Multi-round debate is Phase 2+ territory.
	debateRound1 := successful // only successful proposers re-debate

	debated := RunDebateRound(ctx, RunProposersOptions{
		BaseSystem:  opts.BaseSystem,
		Timeout:     timeout,
		OnOutput:    emit,
	}, debateRound1, verdict.AgreementAxis, opts.TaskPrompt)
	totalCalls += len(debateRound1)

	// Collect usage from debate round.
	for _, r := range debated {
		totalUsage.PromptTokens += r.Usage.PromptTokens
		totalUsage.CompletionTokens += r.Usage.CompletionTokens
		totalUsage.TotalTokens += r.Usage.TotalTokens
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 7. 재판정 (무조건 종결) ────────────────────────────────────
	verdict2, judgeUsage2, judgeCalls2, err := Judge(ctx, opts.Aggregator, debated, opts.TaskPrompt, emit)
	totalUsage.PromptTokens += judgeUsage2.PromptTokens
	totalUsage.CompletionTokens += judgeUsage2.CompletionTokens
	totalUsage.TotalTokens += judgeUsage2.TotalTokens
	totalCalls += judgeCalls2

	if err != nil {
		return nil, fmt.Errorf("magi re-judge failed: %w", err)
	}

	// 재판정 불능 → SideBySideFallback (토론 후 답변 기준).
	if verdict2 == nil {
		return finalize(SideBySideFallback(debated), totalUsage, totalCalls, emit), nil
	}

	// 합의 도달 → 채택.
	if isAcceptable(verdict2, opts.ConfidenceThreshold) {
		finalText = buildAdoptedText(verdict2)
		emit(fmt.Sprintf("✅ 합의 도달 (%s, 신뢰도 %d/10) — 종합 응답 채택\n",
			verdict2.Verdict, verdict2.Confidence))
		return finalize(finalText, totalUsage, totalCalls, emit), nil
	}

	// 여전히 split → DeadlockResult (casting vote — design §5.4).
	return finalize(DeadlockResult(verdict2), totalUsage, totalCalls, emit), nil
}

// isAcceptable checks the DOWN gate condition: verdict is unanimous or
// majority AND confidence meets the threshold (design §2.4, §5.3).
func isAcceptable(v *Verdict, threshold int) bool {
	if v == nil {
		return false
	}
	if v.Verdict != "unanimous" && v.Verdict != "majority" {
		return false
	}
	return v.Confidence >= threshold
}

// buildAdoptedText constructs the final text from an accepted verdict.
// When the verdict is majority and dissent is present, the minority opinion
// is appended for transparency (design §5.3).
func buildAdoptedText(v *Verdict) string {
	text := v.Synthesis
	if v.Verdict == "majority" && v.Dissent != "" {
		text += "\n\n---\n📎 소수의견: " + v.Dissent
	}
	return text
}

// finalize builds the ExecuteResult and emits the cost summary line.
func finalize(text string, usage embedded.ChatUsage, calls int, emit func(string)) *embedded.ExecuteResult {
	totalTokens := usage.PromptTokens + usage.CompletionTokens
	emit(fmt.Sprintf("📊 MAGI 심의 비용: %d 토큰 (호출 %d회)\n", totalTokens, calls))

	return &embedded.ExecuteResult{
		Result:           text,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CostUSD:          0,
		Incomplete:       false,
	}
}
