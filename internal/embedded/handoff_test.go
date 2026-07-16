package embedded

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// healthyHandoffSummary is a realistic structured summary that satisfies the
// minimum length and required-section checks.
func healthyHandoffSummary() string {
	// Deliberately avoids embedding handoffMarker so parse tests stay independent.
	return strings.TrimSpace(`
1. 원 요청/의도: edit_file 강건화와 handoff 요약 구조화를 Phase 3-1로 이식한다. compaction 엔진은 도입하지 않는다.
2. 핵심 기술/개념: Go embedded 프로바이더, llama.cpp, 컨텍스트 핸드오프, NEXT_TASK 마커 경로.
3. 열람·변경 파일:
   - internal/embedded/loop.go — attemptHandoff 연결
   - internal/embedded/handoff.go — 지시문·품질 검사 신설
   - grok-build/.../full_replace_summary_prompt.txt — 참고용 9섹션 패턴
4. 한 일: 구조화 지시문 추가, 파서/품질 게이트 분리, 단위 테스트 작성.
5. 실패·수정: 짧은 한 줄 요약이 후속을 재탐색하게 만든 문제 → 섹션 필수 검사로 차단.
6. 현재 진행: handoff_test.go 작성 직후, 빌드·테스트 검증 단계.
7. 남은 작업: go test ./internal/embedded/... 통과 확인 후 커밋 메시지 작성.
8. 하지 말 것: handoff 제거, full compaction 엔진 도입, NEXT_TASK 마커 변경 금지.
9. 다음 한 걸음: 테스트 실행 후 git commit.
`)
}

func TestBuildHandoffInstruction_Structure(t *testing.T) {
	inst := buildHandoffInstruction()
	if inst == "" {
		t.Fatal("empty instruction")
	}
	// Marker must appear so models know how to emit the follow-up split.
	if !strings.Contains(inst, handoffMarker) {
		t.Fatalf("instruction missing %q", handoffMarker)
	}
	// Nine numbered section headings.
	needles := []string{
		"1. 원 요청",
		"2. 핵심 기술",
		"3. 열람·변경 파일",
		"4. 한 일",
		"5. 실패·수정",
		"6. 현재 진행",
		"7. 남은 작업",
		"8. 하지 말 것",
		"9. 다음 한 걸음",
	}
	for _, needle := range needles {
		if !strings.Contains(inst, needle) {
			t.Errorf("missing section heading %q", needle)
		}
	}
	// Required quality markers must all be present in the instruction so the
	// model is told to emit them (and isHandoffSummaryAcceptable can match).
	for _, sec := range handoffRequiredSections {
		if !strings.Contains(inst, sec) {
			t.Errorf("instruction missing required section marker %q", sec)
		}
	}
	// No tools.
	if !strings.Contains(inst, "도구는 호출하지 마라") {
		t.Error("instruction should forbid tool calls")
	}
}

func TestParseHandoffResponse_WithMarker(t *testing.T) {
	body := healthyHandoffSummary()
	follow := "Continue Phase 3-1: run go test ./internal/embedded/... and commit.\n" +
		"Files: internal/embedded/handoff.go. Do not re-explore the whole repo."
	raw := body + "\n\n" + handoffMarker + "\n" + follow

	summary, gotFollow, ok := parseHandoffResponse(raw)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if summary != body {
		t.Errorf("summary mismatch:\n got: %q\nwant: %q", summary, body)
	}
	if gotFollow != follow {
		t.Errorf("follow-up mismatch:\n got: %q\nwant: %q", gotFollow, follow)
	}
	// Marker must not leak into either part.
	if strings.Contains(summary, handoffMarker) {
		t.Error("summary still contains marker")
	}
	if strings.Contains(gotFollow, handoffMarker) {
		t.Error("follow-up still contains marker")
	}
}

func TestParseHandoffResponse_NoMarker(t *testing.T) {
	body := healthyHandoffSummary()
	summary, follow, ok := parseHandoffResponse(body)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if summary != body {
		t.Errorf("summary changed: %q", summary)
	}
	if follow != "" {
		t.Errorf("unexpected follow-up: %q", follow)
	}
}

func TestParseHandoffResponse_EmptyAndMarkerOnly(t *testing.T) {
	if _, _, ok := parseHandoffResponse(""); ok {
		t.Error("empty content should fail")
	}
	if _, _, ok := parseHandoffResponse("   \n  "); ok {
		t.Error("whitespace-only should fail")
	}
	// Marker with empty summary body → fail (but follow-up may be present).
	raw := handoffMarker + "\ncontinue from here with file X"
	summary, follow, ok := parseHandoffResponse(raw)
	if ok {
		t.Fatalf("marker-only summary should fail, got summary=%q follow=%q", summary, follow)
	}
	if follow == "" {
		// parse keeps follow-up even when ok=false so callers could inspect it,
		// but attemptHandoff only checks ok.
		t.Log("follow-up discarded with empty summary (ok=false path)")
	}
}

func TestParseHandoffResponse_MarkerRegression(t *testing.T) {
	// Exact constant used by queue path — never rename casually.
	if handoffMarker != "===NEXT_TASK===" {
		t.Fatalf("handoffMarker regression: got %q", handoffMarker)
	}
	// Multiple occurrences: first wins (same as historical strings.Index).
	raw := "summary part A 원 요청 열람 한 일 남은 작업 하지 말 " +
		strings.Repeat("상세 ", 40) +
		"\n" + handoffMarker + "\nfirst follow\n" + handoffMarker + "\nsecond"
	summary, follow, ok := parseHandoffResponse(raw)
	if !ok {
		t.Fatal("expected ok")
	}
	if strings.Contains(summary, handoffMarker) {
		t.Error("summary must stop at first marker")
	}
	if !strings.Contains(follow, "first follow") {
		t.Errorf("follow should start after first marker, got %q", follow)
	}
	// Second marker remains inside follow-up text (historical behavior).
	if !strings.Contains(follow, handoffMarker) {
		t.Error("second marker should remain inside follow-up")
	}
}

func TestIsHandoffSummaryAcceptable_Healthy(t *testing.T) {
	s := healthyHandoffSummary()
	if utf8.RuneCountInString(s) < handoffSummaryMinRunes {
		t.Fatalf("fixture too short: %d runes", utf8.RuneCountInString(s))
	}
	if !isHandoffSummaryAcceptable(s) {
		t.Fatal("healthy structured summary should be accepted")
	}
}

func TestIsHandoffSummaryAcceptable_TooShort(t *testing.T) {
	// Has all section keywords but is shorter than the floor.
	short := "1. 원 요청: x\n3. 열람: f.go\n4. 한 일: y\n7. 남은 작업: z\n8. 하지 말: w"
	if utf8.RuneCountInString(short) >= handoffSummaryMinRunes {
		t.Fatalf("test fixture not short enough: %d", utf8.RuneCountInString(short))
	}
	if isHandoffSummaryAcceptable(short) {
		t.Error("short summary should be rejected (trim fallback)")
	}
}

func TestIsHandoffSummaryAcceptable_MissingSections(t *testing.T) {
	// Long enough, but missing required section markers (old 2-line style).
	legacy := strings.Repeat("지금까지 작업을 수행했고 일부를 수정했다. ", 30) +
		"남은 일은 테스트를 돌리고 커밋하는 것이다. 파일 경로는 기억 못 함."
	if utf8.RuneCountInString(legacy) < handoffSummaryMinRunes {
		t.Fatalf("legacy fixture too short")
	}
	if isHandoffSummaryAcceptable(legacy) {
		t.Error("unstructured long summary should be rejected")
	}

	// Drop one required section from an otherwise healthy body.
	base := healthyHandoffSummary()
	missing := strings.ReplaceAll(base, "하지 말", "주의 사항")
	if isHandoffSummaryAcceptable(missing) {
		t.Error("summary missing '하지 말' section should be rejected")
	}
}

func TestIsHandoffSummaryAcceptable_Degenerate(t *testing.T) {
	// Repeated single character padding past the length floor.
	pad := strings.Repeat("가", handoffSummaryMinRunes+50)
	// Inject the section keywords so only the diversity check fails.
	padded := "원 요청 열람 한 일 남은 작업 하지 말 " + pad
	if isHandoffSummaryAcceptable(padded) {
		t.Error("low-diversity padding should be rejected as degenerate")
	}
	if isHandoffSummaryAcceptable("") {
		t.Error("empty should be rejected")
	}
	if isHandoffSummaryAcceptable("   ") {
		t.Error("whitespace should be rejected")
	}
}

func TestHandoffQualityGate_ImpliesTrimFallbackContract(t *testing.T) {
	// Documents the attemptHandoff contract: unacceptable summary → ok=false
	// → caller keeps using trimMessages. This test only covers the pure
	// predicates; the loop wiring is covered by integration elsewhere.
	bad := "요약: 일단 여기까지. 다음에 이어서 하겠습니다."
	if isHandoffSummaryAcceptable(bad) {
		t.Fatal("precondition: bad summary must fail quality gate")
	}
	// After parse, empty follow-up is fine — quality is about the summary body.
	summary, follow, ok := parseHandoffResponse(bad)
	if !ok {
		t.Fatal("non-empty unstructured text still parses")
	}
	if follow != "" {
		t.Fatalf("unexpected follow: %q", follow)
	}
	if isHandoffSummaryAcceptable(summary) {
		t.Fatal("parsed bad summary must still fail quality → trim fallback")
	}
}
