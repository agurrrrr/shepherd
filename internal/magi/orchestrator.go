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
	ProjectPath         string // needed for ToolRegistry (read_file, grep, glob)
	TaskPrompt          string // the user's task prompt
	BaseSystem          string // base system prompt (BuildSystemPromptForEmbedded output)
	Proposers           []ProposerSpec
	Aggregator          AggregatorSpec
	ConfidenceThreshold int                         // default 7 (caller applies config defaults)
	MaxDebateRounds     int                         // 0 = never debate, 1 = design default
	ProposerTimeout     time.Duration               // per-proposer (default 120s)
	OnOutput            func(string)                // live output sink, may be nil
	OnProposerToken     func(slot int, text string) // live token stream, may be nil

	// Phase 1.5: read-only tool injection. All proposers share the same tool
	// set — per-proposer divergence would make the blind comparison unfair.
	// ToolDefs is the filtered (read-only) OpenAI tool definitions.
	// ToolDispatch routes MCP tool calls to the shepherd MCP server or external
	// MCP servers. Native tools (read_file/grep/glob) are handled by a
	// per-proposer ToolRegistry created inside RunProposers.
	ToolDefs     []embedded.OpenAIToolDef
	ToolDispatch embedded.MCPDispatcher
}

// ErrInsufficientProposers signals that fewer than 2 proposers answered.
// The wiring layer falls back to a single embedded run (design §5.1).
var ErrInsufficientProposers = errors.New("magi: fewer than 2 proposers succeeded")

// abstainReaskDirective is the second-chance prompt for a slot the judge
// abstained: demand a committed conclusion from what the proposer already
// knows. Low certainty must surface as a low confidence score, not as a
// content-free answer (task #7182).
const abstainReaskDirective = `판정자가 너의 답변을 '기권'으로 분류했다 — 명확한 결론이 없거나 절차 안내만 있었기 때문이다.
지금 가진 정보만으로 입장을 정하고, 근거와 함께 완결된 최종 답변을 작성하라.
도구는 호출할 수 없다. 확신이 낮으면 낮은 신뢰도 점수로 표현하라.
마지막 줄에 반드시 "CONFIDENCE: <0-10 정수>"를 추가하라.`

// matchAbstainedSlots maps judge-reported abstained display names onto slots
// of results. Names echo the "### <name>" headers of BuildJudgePrompt, which
// uses PersonaDisplayName with each result's pipeline Slot, so the same
// function matches them back. Unknown names are ignored — a judge may
// misquote one, and a no-match must degrade to the pre-existing behavior
// (task #7182).
func matchAbstainedSlots(names []string, results []ProposerResult) (skip []bool, count int) {
	skip = make([]bool, len(results))
	for _, n := range names {
		n = strings.TrimSpace(n)
		for i, r := range results {
			if !skip[i] && n == PersonaDisplayName(r.Spec, r.Slot) {
				skip[i] = true
				count++
				break
			}
		}
	}
	return skip, count
}

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
	emit(fmt.Sprintf("[MAGI:*] 🧠 MAGI 심의 개시 — %s\n", strings.Join(names, "·")))

	// Per-slot name announcement so the frontend can render custom display
	// names in proposer headers before any tokens arrive.
	for i, p := range opts.Proposers {
		emit(fmt.Sprintf("[MAGI:%d] 🧩 %s %s\n", i, PersonaEmoji(p), PersonaDisplayName(p, i)))
	}

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

	tRound1 := time.Now()
	round1 := RunProposers(ctx, RunProposersOptions{
		Proposers:       opts.Proposers,
		BaseSystem:      opts.BaseSystem,
		UserPrompts:     userPrompts,
		Timeout:         timeout,
		OnOutput:        emit,
		OnProposerToken: opts.OnProposerToken,
		ToolDefs:        opts.ToolDefs,
		ToolDispatch:    opts.ToolDispatch,
		ProjectPath:     opts.ProjectPath,
		SheepName:       opts.SheepName,
	})
	totalCalls += len(opts.Proposers)
	// Confidence-nudge reasks (and any other ExtraCalls) count toward cost.
	totalCalls += sumExtraCalls(round1)

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

	emit(fmt.Sprintf("[MAGI:*] ⏱️ 1라운드 %d초 — 성공 %d/%d\n",
		int(time.Since(tRound1).Seconds()), successCount, len(opts.Proposers)))

	if successCount <= 1 {
		emit(fmt.Sprintf("[MAGI:*] ⚠️ MAGI 심의 불가 (성공 응답 %d/%d) — 단일 임베디드 실행으로 폴백합니다\n",
			successCount, len(opts.Proposers)))
		return nil, ErrInsufficientProposers
	}

	if successCount == 2 {
		emit("[MAGI:*] ⚠️ 심의자 1명 이탈 — 2인 심의로 계속합니다\n")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 4. 판정 (1차) ─────────────────────────────────────────────
	tJudge := time.Now()
	verdict, judgeUsage, judgeCalls, err := Judge(ctx, opts.Aggregator, successful, opts.TaskPrompt, emit)
	totalUsage.PromptTokens += judgeUsage.PromptTokens
	totalUsage.CompletionTokens += judgeUsage.CompletionTokens
	totalUsage.TotalTokens += judgeUsage.TotalTokens
	totalCalls += judgeCalls

	if err != nil {
		return nil, fmt.Errorf("magi judge failed: %w", err)
	}

	emit(fmt.Sprintf("[MAGI:*] ⏱️ 판정 %d초\n", int(time.Since(tJudge).Seconds())))

	// Verdict == nil → 판정 불능 → SideBySideFallback으로 정상 완료.
	if verdict == nil {
		result := finalize(SideBySideFallback(successful), totalUsage, totalCalls, emit)
		return result, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 4.5 기권 슬롯 재기회 (task #7182) ──
	//
	// A failed first verdict with judge-declared abstentions gets one recovery
	// attempt before the debate decision. When the re-judge turns acceptable,
	// the normal adoption path below ends the pipeline without a debate.
	if !isAcceptable(verdict, opts.ConfidenceThreshold) && len(verdict.Abstained) > 0 {
		verdict = secondChanceForAbstained(ctx, opts, successful, verdict, timeout, &totalUsage, &totalCalls, emit)
	}

	// ── 5. DOWN 게이트 (설계서 §2.4, §5.3) ────────────────────────
	adopted := false
	finalText := ""

	if isAcceptable(verdict, opts.ConfidenceThreshold) {
		// 채택 경로.
		adopted = true
		finalText = buildAdoptedText(verdict)
		emit(fmt.Sprintf("[MAGI:*] ✅ 합의 도달 (%s, 신뢰도 %d/10) — 종합 응답 채택\n",
			verdict.Verdict, verdict.Confidence))
	} else {
		// split 또는 저신뢰.
		if opts.MaxDebateRounds == 0 {
			// 토론 없이 교착 처리로 종결.
			finalText = DeadlockResult(verdict)
			emit(fmt.Sprintf("[MAGI:*] ⚖️ 합의 판정: %s, 신뢰도 %d/10 — 토론 미설정으로 종결\n",
				verdict.Verdict, verdict.Confidence))
			return finalize(finalText, totalUsage, totalCalls, emit), nil
		}

		// 토론 진입.
		emit(fmt.Sprintf("[MAGI:*] ⚖️ 합의 판정: %s, 신뢰도 %d/10 — 토론 라운드 진입\n",
			verdict.Verdict, verdict.Confidence))
		if verdict.AgreementAxis != "" {
			emit(fmt.Sprintf("[MAGI:*]   (쟁점: %s)\n", verdict.AgreementAxis))
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

	// skip is recomputed against the operative verdict from §4.5:
	//   - re-judge succeeded → verdict2.Abstained (may have cleared recoveries)
	//   - re-judge failed    → first verdict with recovered names pruned
	//   - no second chance   → first verdict.Abstained as-is
	// So a slot that just produced a real answer is never debate-skipped.
	//
	// Slots still abstained after their second chance are excluded from the
	// debate (task #7182): they hold no position to argue, and their answers
	// must not weigh down peer prompts. When exclusion leaves fewer than two
	// active deliberators, the debate is pointless — a two-way exchange is the
	// minimum for it to change anything — so end as a deadlock immediately.
	skip, skipCount := matchAbstainedSlots(verdict.Abstained, successful)
	if len(successful)-skipCount < 2 {
		emit("[MAGI:*] ⚖️ 기권 제외 후 유효 심의자 2명 미만 — 토론을 생략하고 교착 처리로 종결합니다\n")
		return finalize(DeadlockResult(verdict), totalUsage, totalCalls, emit), nil
	}

	tDebate := time.Now()
	debated := RunDebateRound(ctx, RunProposersOptions{
		BaseSystem:      opts.BaseSystem,
		Timeout:         timeout,
		Skip:            skip,
		OnOutput:        emit,
		OnProposerToken: opts.OnProposerToken,
		ProjectPath:     opts.ProjectPath,
		SheepName:       opts.SheepName,
	}, debateRound1, verdict.AgreementAxis, opts.TaskPrompt)
	totalCalls += len(debateRound1) - skipCount
	totalCalls += sumExtraCalls(debated)

	// Collect usage from debate round.
	for _, r := range debated {
		totalUsage.PromptTokens += r.Usage.PromptTokens
		totalUsage.CompletionTokens += r.Usage.CompletionTokens
		totalUsage.TotalTokens += r.Usage.TotalTokens
	}

	emit(fmt.Sprintf("[MAGI:*] ⏱️ 토론 라운드 %d초\n", int(time.Since(tDebate).Seconds())))

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 7. 재판정 (무조건 종결) ────────────────────────────────────
	tJudge2 := time.Now()
	verdict2, judgeUsage2, judgeCalls2, err := Judge(ctx, opts.Aggregator, debated, opts.TaskPrompt, emit)
	totalUsage.PromptTokens += judgeUsage2.PromptTokens
	totalUsage.CompletionTokens += judgeUsage2.CompletionTokens
	totalUsage.TotalTokens += judgeUsage2.TotalTokens
	totalCalls += judgeCalls2

	if err != nil {
		return nil, fmt.Errorf("magi re-judge failed: %w", err)
	}

	emit(fmt.Sprintf("[MAGI:*] ⏱️ 재판정 %d초\n", int(time.Since(tJudge2).Seconds())))

	// 재판정 불능 → SideBySideFallback (토론 후 답변 기준).
	if verdict2 == nil {
		return finalize(SideBySideFallback(debated), totalUsage, totalCalls, emit), nil
	}

	// 합의 도달 → 채택.
	if isAcceptable(verdict2, opts.ConfidenceThreshold) {
		finalText = buildAdoptedText(verdict2)
		emit(fmt.Sprintf("[MAGI:*] ✅ 합의 도달 (%s, 신뢰도 %d/10) — 종합 응답 채택\n",
			verdict2.Verdict, verdict2.Confidence))
		return finalize(finalText, totalUsage, totalCalls, emit), nil
	}

	// 여전히 split → DeadlockResult (casting vote — design §5.4).
	return finalize(DeadlockResult(verdict2), totalUsage, totalCalls, emit), nil
}

// secondChanceForAbstained gives each judge-abstained slot one tools-off
// reask, then re-judges once when any answer actually improved (task #7182:
// an abstention shrinks the tally to a 2:1 deadlock — one recovery attempt
// can restore a 3-vote verdict). Runs at most once per pipeline (straight-line
// call site). Mutates successful in place and returns the operative verdict:
// the re-judged one when it parsed, otherwise the original (conservative).
func secondChanceForAbstained(
	ctx context.Context,
	opts Options,
	successful []ProposerResult,
	verdict *Verdict,
	globalTimeout time.Duration,
	totalUsage *embedded.ChatUsage,
	totalCalls *int,
	emit func(string),
) *Verdict {
	skip, count := matchAbstainedSlots(verdict.Abstained, successful)
	if count == 0 || ctx.Err() != nil {
		return verdict
	}
	emit(fmt.Sprintf("[MAGI:*] 🔁 기권 재기회 — %d개 슬롯에 재질문합니다\n", count))

	updatedNames := make(map[string]bool)
	for i := range successful {
		if !skip[i] || ctx.Err() != nil {
			continue
		}
		spec := successful[i].Spec
		// Pipeline slot (not compact index) so [MAGI:N] / OnProposerToken
		// stay on the original panel after SuccessfulResults reindexing.
		slot := successful[i].Slot
		displayName := PersonaDisplayName(spec, slot)

		effTimeout := globalTimeout
		if spec.Timeout > 0 {
			effTimeout = spec.Timeout
		}
		budget := reaskBudget(effTimeout)

		tokenCb := func(text string) {
			if opts.OnProposerToken != nil {
				opts.OnProposerToken(slot, text)
			}
		}

		systemPrompt := BuildProposerSystemPrompt(opts.BaseSystem, spec, slot)
		answer, usage, err := reaskProposer(ctx, spec, systemPrompt, opts.TaskPrompt,
			successful[i].Answer, abstainReaskDirective, budget,
			opts.ProjectPath, PersonaSheepName(opts.SheepName, spec, slot), tokenCb)
		totalUsage.PromptTokens += usage.PromptTokens
		totalUsage.CompletionTokens += usage.CompletionTokens
		totalUsage.TotalTokens += usage.TotalTokens
		*totalCalls++ // abstain reask: same policy as confidence-nudge ExtraCalls

		if err != nil {
			emit(fmt.Sprintf("[MAGI:%d] ⚠️ %s 재질문 실패 — 기권 유지 (%v)\n", slot, displayName, err))
			continue
		}
		cleaned, conf := ExtractConfidence(answer)
		// Adopt only a gate-passing answer with a reported confidence — a
		// failed reask must never destroy the existing answer (#7000 principle).
		if conf < 0 || CheckAnswerContent(cleaned) != nil {
			emit(fmt.Sprintf("[MAGI:%d] ⚠️ %s 재질문 답변이 요건 미달 — 기권 유지\n", slot, displayName))
			continue
		}
		successful[i].Answer = cleaned
		successful[i].Confidence = conf
		updatedNames[displayName] = true
		emit(fmt.Sprintf("[MAGI:%d] ✅ %s 재질문 성공 — 신뢰도 %d/10\n", slot, displayName, conf))
	}

	if len(updatedNames) == 0 || ctx.Err() != nil {
		return verdict
	}

	// Re-judge once with the updated answers.
	// On success the new verdict (including a fresh Abstained list) becomes
	// operative for §5/§6; on failure we keep the first verdict but prune
	// recovered names so debate exclusion does not drop real answers.
	tRejudge := time.Now()
	verdict2, judgeUsage, judgeCalls, err := Judge(ctx, opts.Aggregator, successful, opts.TaskPrompt, emit)
	totalUsage.PromptTokens += judgeUsage.PromptTokens
	totalUsage.CompletionTokens += judgeUsage.CompletionTokens
	totalUsage.TotalTokens += judgeUsage.TotalTokens
	*totalCalls += judgeCalls

	if err != nil || verdict2 == nil {
		emit("[MAGI:*] ⚠️ 재기회 재판정 실패 — 1차 판정을 유지합니다\n")
		// The first verdict's abstained list is now stale for the slots whose
		// answers were just updated — prune them so the debate exclusion
		// (step-11) doesn't drop a slot that holds a substantive answer.
		kept := make([]string, 0, len(verdict.Abstained))
		for _, n := range verdict.Abstained {
			if !updatedNames[strings.TrimSpace(n)] {
				kept = append(kept, n)
			}
		}
		verdict.Abstained = kept
		return verdict
	}
	emit(fmt.Sprintf("[MAGI:*] ⏱️ 재기회 재판정 %d초\n", int(time.Since(tRejudge).Seconds())))
	return verdict2
}

// isAcceptable checks the DOWN gate condition (design §2.4, §5.3).
// A unanimous verdict is accepted at threshold-1: unanimity is already the
// strongest agreement signal, and sending an 8/10-unanimous round to debate
// doubled the cost and worsened the outcome to deadlock (task #7182).
func isAcceptable(v *Verdict, threshold int) bool {
	if v == nil {
		return false
	}
	switch v.Verdict {
	case "unanimous":
		return v.Confidence >= threshold-1
	case "majority":
		return v.Confidence >= threshold
	}
	return false
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

// finalize builds the ExecuteResult and emits the final synthesis + cost summary.
// The synthesis text is emitted with [MAGI:*] prefix so the frontend's unified
// panel displays the actual answer — without this, only status messages appear
// in the main window and the final result is invisible until task completion.
func finalize(text string, usage embedded.ChatUsage, calls int, emit func(string)) *embedded.ExecuteResult {
	// Emit the final synthesis so it appears in the unified panel.
	if text != "" {
		emit(fmt.Sprintf("[MAGI:*] 📋 최종 종합:\n%s\n", text))
	}
	totalTokens := usage.PromptTokens + usage.CompletionTokens
	emit(fmt.Sprintf("[MAGI:*] 📊 MAGI 심의 비용: %d 토큰 (호출 %d회)\n", totalTokens, calls))

	return &embedded.ExecuteResult{
		Result:           text,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CostUSD:          0,
		Incomplete:       false,
	}
}
