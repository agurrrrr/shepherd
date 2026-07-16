package embedded

import (
	"strings"
	"unicode/utf8"
)

// handoffMarker separates the handoff summary from the follow-up task prompt
// in the model's final answer. Kept ASCII-only so any model can reproduce it.
const handoffMarker = "===NEXT_TASK==="

// handoffSummaryMinRunes is the minimum length of an acceptable handoff summary
// (after splitting off the NEXT_TASK section). Shorter outputs are treated as
// degenerate and the caller falls back to plain context trimming.
//
// Tuned below grok-build's MIN_SUMMARY_SEED_CHARS (500) because shepherd handoffs
// run on local models that produce denser Korean summaries; still high enough to
// reject empty / one-liner stubs.
const handoffSummaryMinRunes = 200

// handoffRequiredSections lists distinctive substrings that must appear in a
// quality handoff summary. They match the numbered headings in
// buildHandoffInstruction (the five sections required by the Phase 3-1 plan,
// plus a couple of the expanded 9-section set for robustness).
//
// A summary that only restates "done / continue later" without these headings
// forces a re-exploration in the follow-up task — exactly what this upgrade
// exists to prevent.
var handoffRequiredSections = []string{
	"원 요청",   // ① Primary request / intent
	"열람",      // ② Files examined / changed
	"한 일",     // ③ What was done
	"남은 작업", // ④ Remaining work
	"하지 말",   // ⑤ Do-not-do / already finished
}

// buildHandoffInstruction returns the user message that asks the model for a
// structured completion summary plus optional follow-up prompt.
//
// Pattern source: grok-build full_replace_summary_prompt (9 numbered sections).
// This is *not* in-session compaction — it only upgrades the handoff instruction
// quality so a follow-up task can resume without re-exploring the repo.
func buildHandoffInstruction() string {
	var b strings.Builder
	b.WriteString("컨텍스트 한계에 도달했다. 이번 작업은 여기서 마무리한다.\n")
	b.WriteString("후속 에이전트는 이 대화를 볼 수 없다. 그래서 아래 구조로 충실하고 간결한 요약을 작성하라.\n")
	b.WriteString("모든 섹션 제목을 포함하고, 해당 내용이 없으면 \"없음\"이라고 써라.\n")
	b.WriteString("장황한 코드 덤프보다 경로·결정·실패 원인·다음 단계를 우선하라. 도구는 호출하지 마라.\n\n")
	b.WriteString("1. 원 요청/의도: 사용자의 명시적 요청과 의도, 제약·범위·선호.\n")
	b.WriteString("2. 핵심 기술/개념: 사용한 기술·언어·프레임워크·패턴·설정 키.\n")
	b.WriteString("3. 열람·변경 파일: 구체 경로마다 왜 중요한지, 열람/생성/수정 여부, 핵심 변경 요지.\n")
	b.WriteString("4. 한 일: 이미 완료한 작업 목록 (재실행하면 안 되는 것 포함).\n")
	b.WriteString("5. 실패·수정: 겪은 오류·실패 명령·테스트 실패, 원인, 해결 방법.\n")
	b.WriteString("6. 현재 진행: 이 요약 요청 직전에 하던 일 (파일·명령·상태). 중단 지점을 특정할 것.\n")
	b.WriteString("7. 남은 작업: 후속이 바로 이어가도록 경로·결정사항·주의점을 빠짐없이.\n")
	b.WriteString("8. 하지 말 것: 이미 끝난 것, 되돌리면 안 되는 것, 재시도·재탐색 금지 사항.\n")
	b.WriteString("9. 다음 한 걸음: 바로 이어서 할 단일 다음 단계 (없으면 \"없음\").\n\n")
	b.WriteString("아직 남은 작업이 있으면, 요약 본문 마지막에 '")
	b.WriteString(handoffMarker)
	b.WriteString("' 한 줄을 쓰고 그 아래에 남은 작업을 새 작업 프롬프트로 작성하라.\n")
	b.WriteString("새 작업 프롬프트는 위 요약의 핵심(경로·결정·주의점·다음 단계)을 자족적으로 담아야 한다.\n")
	b.WriteString("남은 작업이 없으면 '")
	b.WriteString(handoffMarker)
	b.WriteString("' 섹션을 생략하라.")
	return b.String()
}

// parseHandoffResponse splits model output into the summary body and optional
// follow-up prompt after handoffMarker. Both parts are trimmed. ok is false when
// the summary body is empty (even if a follow-up was present).
func parseHandoffResponse(content string) (summary, followUp string, ok bool) {
	summary = strings.TrimSpace(content)
	if summary == "" {
		return "", "", false
	}
	if i := strings.Index(summary, handoffMarker); i >= 0 {
		followUp = strings.TrimSpace(summary[i+len(handoffMarker):])
		summary = strings.TrimSpace(summary[:i])
	}
	if summary == "" {
		return "", followUp, false
	}
	return summary, followUp, true
}

// isHandoffSummaryAcceptable reports whether a parsed handoff summary is good
// enough to complete the current task and (optionally) enqueue a follow-up.
// Thin / unstructured summaries return false so the caller can fall back to
// plain trimMessages instead of losing work quality.
func isHandoffSummaryAcceptable(summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return false
	}
	if utf8.RuneCountInString(summary) < handoffSummaryMinRunes {
		return false
	}
	// Reject pure repetition / placeholder stubs.
	if isDegenerateHandoffText(summary) {
		return false
	}
	for _, sec := range handoffRequiredSections {
		if !strings.Contains(summary, sec) {
			return false
		}
	}
	return true
}

// isDegenerateHandoffText catches obvious low-signal outputs: mostly the same
// short token repeated, or a near-empty stub padded with whitespace/punctuation.
func isDegenerateHandoffText(s string) bool {
	// Collapse whitespace for signal density.
	compact := strings.Join(strings.Fields(s), "")
	if compact == "" {
		return true
	}
	runes := []rune(compact)
	if len(runes) < 40 {
		return true
	}
	freq := make(map[rune]int, 64)
	max := 0
	for _, r := range runes {
		freq[r]++
		if freq[r] > max {
			max = freq[r]
		}
	}
	// Too few distinct characters, or one character dominates (>70%) → padding.
	if len(freq) < 12 {
		return true
	}
	return max*10 > len(runes)*7
}
