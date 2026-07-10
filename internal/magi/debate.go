package magi

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// debatePeerCap bounds each peer answer inside a debate prompt — half the
// own-answer cap. Peers are context to argue against, not the answer under
// revision, and three 12K peer sections made the debate round heavier than
// round 1 (task #7182: 232K-token debate).
const debatePeerCap = 6000

// BuildDebatePrompt renders the debate-round user prompt for slot i:
// the proposer's own previous answer plus the other answers anonymized as
// 심의자 A/B (design §2.3 ④ — identity exposure causes authority bias).
//
// Persona names and model identifiers are omitted from peer-answer sections
// so no authority hierarchy can form around a known persona or model family.
func BuildDebatePrompt(taskPrompt string, results []ProposerResult, slot int, agreementAxis string, skip []bool) string {
	var b strings.Builder

	// 1. Original task prompt.
	b.WriteString("[원 태스크]\n")
	b.WriteString(capText(taskPrompt, 4000))
	b.WriteString("\n\n")

	// 2. Own previous answer.
	b.WriteString("너의 이전 답변:\n")
	b.WriteString(capText(results[slot].Answer, 12000))
	b.WriteString("\n\n")

	// 3. Other deliberators' answers — anonymized as 심의자 A/B/etc.
	b.WriteString("다른 심의자들의 답변:\n")
	letter := 'A'
	for i := range results {
		if i == slot {
			continue
		}
		// Abstained slots contribute no position to argue against — leaving
		// their content-free answers out keeps the prompt lean (task #7182).
		if i < len(skip) && skip[i] {
			continue
		}
		fmt.Fprintf(&b, "\n### 심의자 %c\n", letter)
		b.WriteString(capText(results[i].Answer, debatePeerCap))
		b.WriteString("\n")
		letter++
	}
	b.WriteString("\n")

	// 4. Agreement axis from the judge (optional).
	if agreementAxis != "" {
		fmt.Fprintf(&b, "쟁점: %s\n\n", agreementAxis)
	}

	// 5. Instructions (design §5.4 verbatim).
	b.WriteString(`다른 답변에서 너의 답의 실제 오류를 발견하면 수정하라.
근거가 유지되면 답을 바꾸지 마라. 동의 자체는 목표가 아니다.
재조사는 금지한다 — 도구를 호출하지 말고, 위에 제시된 답변들과 이미 확보한 근거만으로 판단하라.
수정 여부와 무관하게 완결된 최종 답변을 다시 작성하고,
마지막 줄에 "CONFIDENCE: <0-10 정수>"를 추가하라.`)

	return b.String()
}

// RunDebateRound re-runs all successful proposers with debate prompts.
// A proposer that fails in the debate round keeps its round-1 answer
// (losing a member mid-debate must not shrink the deliberation).
//
// This function is designed for exactly one invocation — multi-round debate
// is prohibited (design §2.3 ②). Round-count control lives in the orchestrator.
//
// The 개시 출력 ("⚖️ 합의 판정 ...") is the orchestrator's responsibility;
// this function only emits per-proposer fallback warnings on failure.
func RunDebateRound(ctx context.Context, opts RunProposersOptions, round1 []ProposerResult, agreementAxis string, taskPrompt string) []ProposerResult {
	// Derive proposers from round1 so only successful members re-debate.
	opts.Proposers = make([]ProposerSpec, len(round1))
	// OriginalSlots keeps [MAGI:N] / OnProposerToken on the pipeline panels
	// when round1 was compacted by SuccessfulResults (task #7234).
	opts.OriginalSlots = originalSlotsFor(round1)
	for i := range round1 {
		opts.Proposers[i] = round1[i].Spec
	}

	// Build per-slot debate prompts. Skipped compact indices leave prompts[i]
	// as "" — RunProposers checks Skip before reading UserPrompts.
	prompts := make([]string, len(round1))
	for i := range round1 {
		if i < len(opts.Skip) && opts.Skip[i] {
			continue
		}
		prompts[i] = BuildDebatePrompt(taskPrompt, round1, i, agreementAxis, opts.Skip)
	}
	opts.UserPrompts = prompts

	// Reuse RunProposers for parallel execution (design §5.4).
	// Intentionally no ToolDefs/ToolDispatch — debate is tools-off (step-11).
	// CLI providers only get a soft "재조사 금지" instruction (known limit).
	debated := RunProposers(ctx, opts)

	// Replace failed slots with round-1 answers.
	for i, r := range debated {
		if r.Err != nil {
			slot := opts.OriginalSlots[i]
			displayName := PersonaDisplayName(round1[i].Spec, slot)
			if opts.OnOutput != nil {
				if errors.Is(r.Err, errSlotSkipped) {
					opts.OnOutput(fmt.Sprintf("  ⚠️ %s 기권 유지 — 토론 제외\n", displayName))
				} else {
					opts.OnOutput(fmt.Sprintf("  ⚠️ %s 토론 라운드 실패 — 1라운드 답변 유지\n", displayName))
				}
			}
			// Preserve Slot from round1 (restored answer already has it).
			debated[i] = round1[i]
		}
	}

	return debated
}

// originalSlotsFor returns pipeline slot indices for a (possibly compacted)
// result slice. When every Slot field is still zero (hand-built fixtures that
// never set Slot) and there is more than one entry, falls back to compact
// indices 0..n-1 so multi-slot tests stay self-consistent.
func originalSlotsFor(results []ProposerResult) []int {
	slots := make([]int, len(results))
	anySet := false
	for i, r := range results {
		slots[i] = r.Slot
		if r.Slot != 0 {
			anySet = true
		}
	}
	if !anySet && len(results) > 1 {
		for i := range slots {
			slots[i] = i
		}
	}
	return slots
}

// DeadlockResult renders the final output when the debate round still ends
// split: casting vote — adopt the majority synthesis but attach the dissent
// so the user gets the material for a final call (design §5.4).
func DeadlockResult(v *Verdict) string {
	var b strings.Builder

	b.WriteString("⚠️ MAGI 교착 (2:1) — 다수안을 채택하되 소수의견을 병기합니다.\n\n")
	b.WriteString(v.Synthesis)

	if v.Dissent != "" {
		b.WriteString("\n\n---\n📎 소수의견: ")
		b.WriteString(v.Dissent)
	}

	return b.String()
}
