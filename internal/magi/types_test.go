package magi

import (
	"strings"
	"testing"
)

func TestParseVerdict_PureJSON(t *testing.T) {
	raw := `{"verdict":"unanimous","agreement_axis":"all agree on approach","synthesis":"use option A","dissent":"","confidence":9}`
	v, err := ParseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Verdict != "unanimous" {
		t.Errorf("verdict = %q, want %q", v.Verdict, "unanimous")
	}
	if v.Synthesis != "use option A" {
		t.Errorf("synthesis = %q, want %q", v.Synthesis, "use option A")
	}
	if v.Confidence != 9 {
		t.Errorf("confidence = %d, want 9", v.Confidence)
	}
}

func TestParseVerdict_JSONFence(t *testing.T) {
	raw := "```json\n" +
		`{"verdict":"majority","agreement_axis":"2:1 on method","synthesis":"refactor first","dissent":"CASPER wants rollback","confidence":7}` + "\n```"
	v, err := ParseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Verdict != "majority" {
		t.Errorf("verdict = %q, want %q", v.Verdict, "majority")
	}
	if v.Dissent != "CASPER wants rollback" {
		t.Errorf("dissent = %q, want %q", v.Dissent, "CASPER wants rollback")
	}
}

func TestParseVerdict_ProseBeforeJSON(t *testing.T) {
	raw := `Here is my judgment:

` + "```json\n" +
		`{"verdict":"split","agreement_axis":"order of operations","synthesis":"proceed cautiously","dissent":"strong disagreement on rollback","confidence":5}` + "\n```"
	v, err := ParseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Verdict != "split" {
		t.Errorf("verdict = %q, want %q", v.Verdict, "split")
	}
}

func TestParseVerdict_BrokenJSON(t *testing.T) {
	raw := `{"verdict":"unanimous","synthesis":"`
	_, err := ParseVerdict(raw)
	if err == nil {
		t.Fatal("expected error for broken JSON, got nil")
	}
}

func TestParseVerdict_InvalidVerdictValue(t *testing.T) {
	raw := `{"verdict":"maybe","agreement_axis":"unsure","synthesis":"something","confidence":5}`
	_, err := ParseVerdict(raw)
	if err == nil {
		t.Fatal("expected error for invalid verdict value, got nil")
	}
	if !strings.Contains(err.Error(), "invalid verdict") {
		t.Errorf("error should mention invalid verdict, got: %v", err)
	}
}

func TestParseVerdict_EmptySynthesis(t *testing.T) {
	raw := `{"verdict":"unanimous","agreement_axis":"agree","synthesis":"","confidence":8}`
	_, err := ParseVerdict(raw)
	if err == nil {
		t.Fatal("expected error for empty synthesis, got nil")
	}
	if !strings.Contains(err.Error(), "empty synthesis") {
		t.Errorf("error should mention empty synthesis, got: %v", err)
	}
}

func TestParseVerdict_ConfidenceClamp(t *testing.T) {
	raw := `{"verdict":"unanimous","agreement_axis":"agree","synthesis":"ok","confidence":15}`
	v, err := ParseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Confidence != 10 {
		t.Errorf("confidence = %d, want 10 (clamped)", v.Confidence)
	}
}

func TestParseVerdict_ConfidenceNegativeClamp(t *testing.T) {
	raw := `{"verdict":"unanimous","agreement_axis":"agree","synthesis":"ok","confidence":-3}`
	v, err := ParseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Confidence != 0 {
		t.Errorf("confidence = %d, want 0 (clamped)", v.Confidence)
	}
}

func TestParseVerdict_JSONWithBracesInString(t *testing.T) {
	raw := `{"verdict":"unanimous","agreement_axis":"code block","synthesis":"use {\n  key: value\n}","dissent":"","confidence":8}`
	v, err := ParseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(v.Synthesis, "key") {
		t.Errorf("synthesis should contain 'key', got: %q", v.Synthesis)
	}
}

func TestParseVerdict_NoJSON(t *testing.T) {
	raw := "This is just plain text with no JSON."
	_, err := ParseVerdict(raw)
	if err == nil {
		t.Fatal("expected error for no JSON, got nil")
	}
}

// --- ExtractConfidence tests ---

func TestExtractConfidence_SimpleNumber(t *testing.T) {
	answer := "Here is my answer.\n\nCONFIDENCE: 8"
	cleaned, conf := ExtractConfidence(answer)
	if conf != 8 {
		t.Errorf("confidence = %d, want 8", conf)
	}
	if strings.Contains(cleaned, "CONFIDENCE") {
		t.Errorf("cleaned should not contain CONFIDENCE line, got: %q", cleaned)
	}
}

func TestExtractConfidence_SlashFormat(t *testing.T) {
	answer := "Some answer text.\nconfidence: 8/10"
	cleaned, conf := ExtractConfidence(answer)
	if conf != 8 {
		t.Errorf("confidence = %d, want 8", conf)
	}
	if strings.Contains(cleaned, "confidence") {
		t.Errorf("cleaned should not contain confidence line, got: %q", cleaned)
	}
}

func TestExtractConfidence_KoreanVariant(t *testing.T) {
	answer := "답변 내용입니다.\n신뢰도: 9"
	cleaned, conf := ExtractConfidence(answer)
	if conf != 9 {
		t.Errorf("confidence = %d, want 9", conf)
	}
	if strings.Contains(cleaned, "신뢰도") {
		t.Errorf("cleaned should not contain 신뢰도 line, got: %q", cleaned)
	}
}

func TestExtractConfidence_DecimalRounded(t *testing.T) {
	answer := "Answer here.\nCONFIDENCE: 8.5"
	_, conf := ExtractConfidence(answer)
	if conf != 9 {
		t.Errorf("confidence = %d, want 9 (rounded from 8.5)", conf)
	}
}

func TestExtractConfidence_NotFound(t *testing.T) {
	answer := "Just a regular answer with no confidence line."
	cleaned, conf := ExtractConfidence(answer)
	if conf != -1 {
		t.Errorf("confidence = %d, want -1", conf)
	}
	if cleaned != answer {
		t.Errorf("cleaned should equal original when no confidence found")
	}
}

func TestExtractConfidence_IgnoreMidText(t *testing.T) {
	// CONFIDENCE appears in the body (line 1 of a 10-line answer) — should be
	// ignored because we only scan the last 5 lines.
	lines := []string{
		"Line 1: CONFIDENCE: 3 (this should be ignored)",
		"Line 2",
		"Line 3",
		"Line 4",
		"Line 5",
		"Line 6",
		"Line 7",
		"Line 8",
	}
	answer := strings.Join(lines, "\n")
	cleaned, conf := ExtractConfidence(answer)
	if conf != -1 {
		t.Errorf("confidence = %d, want -1 (mid-text should be ignored)", conf)
	}
	if cleaned != answer {
		t.Errorf("cleaned should be unchanged when no valid confidence in last 5 lines")
	}
}

func TestExtractConfidence_ClampToTen(t *testing.T) {
	answer := "Answer.\nCONFIDENCE: 12"
	_, conf := ExtractConfidence(answer)
	if conf != 10 {
		t.Errorf("confidence = %d, want 10 (clamped)", conf)
	}
}

// --- capText tests ---

func TestCapText_NoTruncation(t *testing.T) {
	s := "hello world"
	got := capText(s, 100)
	if got != s {
		t.Errorf("capText = %q, want %q (no truncation)", got, s)
	}
}

func TestCapText_Truncation(t *testing.T) {
	s := "abcdefghij"
	got := capText(s, 5)
	if !strings.HasPrefix(got, "abcde") {
		t.Errorf("capText should start with first 5 chars, got: %q", got)
	}
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("capText should contain [truncated] marker, got: %q", got)
	}
}

func TestCapText_RuneAware(t *testing.T) {
	s := "한글테스트입니다"
	got := capText(s, 3)
	if !strings.HasPrefix(got, "한글테") {
		t.Errorf("capText should be rune-aware (first 3 runes), got: %q", got)
	}
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("capText should contain [truncated] marker, got: %q", got)
	}
}

// --- ParseVerdict abstained field tests (step-10) ---

func TestParseVerdict_AbstainedField(t *testing.T) {
	// JSON with abstained field populated.
	raw := `{"verdict":"split","agreement_axis":"유효표 부족","synthesis":"임시 종합.","dissent":"CASPER-3 기권 처리","confidence":5,"abstained":["CASPER-3"]}`
	v, err := ParseVerdict(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v.Abstained) != 1 || v.Abstained[0] != "CASPER-3" {
		t.Errorf("Abstained = %v, want [\"CASPER-3\"]", v.Abstained)
	}

	// Existing JSON without abstained field — backward compatible.
	rawNoAbstain := `{"verdict":"unanimous","agreement_axis":"all agree","synthesis":"ok","confidence":9}`
	v2, err := ParseVerdict(rawNoAbstain)
	if err != nil {
		t.Fatalf("unexpected error for JSON without abstained: %v", err)
	}
	if len(v2.Abstained) != 0 {
		t.Errorf("Abstained should be empty, got %v", v2.Abstained)
	}
}
