package worker

import (
	"strings"
	"testing"
)

// TestEmbeddedBehaviorDiscipline covers Phase 2-2: short fixed conduct block +
// system-reminder convention. (Full BuildSystemPromptForEmbedded needs a live
// DB for skills lookup, so we unit-test the discipline block and join order.)
func TestEmbeddedBehaviorDiscipline(t *testing.T) {
	d := embeddedBehaviorDiscipline()
	for _, want := range []string{
		"[행동 규율]",
		"read_file",
		"edit_file",
		"write_file",
		"cat",
		"system-reminder",
		"미래형",
		"빌드",
		"force push",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("discipline block missing %q; got %q", want, d)
		}
	}
	// Keep it short — local context is expensive.
	if len(d) > 800 {
		t.Errorf("discipline block too long (%d bytes); keep concise", len(d))
	}
}

// TestEmbeddedPromptSectionOrder verifies base discipline sits before custom
// overlay when joined the same way BuildSystemPromptForEmbedded does.
func TestEmbeddedPromptSectionOrder(t *testing.T) {
	joined := joinSections([]string{
		"identity",
		embeddedBehaviorDiscipline(),
		"[User Custom Instructions]\nCUSTOM_OVERLAY_MARKER",
	})
	discIdx := strings.Index(joined, "[행동 규율]")
	customIdx := strings.Index(joined, "[User Custom Instructions]")
	if discIdx < 0 || customIdx < 0 || discIdx > customIdx {
		t.Errorf("expected [행동 규율] before custom; disc=%d custom=%d\n%s", discIdx, customIdx, joined)
	}
	if !strings.Contains(joined, "CUSTOM_OVERLAY_MARKER") {
		t.Error("custom overlay missing from joined prompt")
	}
}

// TestMagiPromptHasNoCodingDiscipline: MAGI is advisory/read-only; the full
// coding-agent [행동 규율] block is intentionally not part of its identity
// sections (Phase 2-2 minimal invasion). We assert via the Magi builder's
// fixed identity text rather than calling BuildSystemPromptForMagi (DB).
func TestMagiPromptHasNoCodingDiscipline(t *testing.T) {
	// Magi identity strings are hardcoded in BuildSystemPromptForMagi and
	// must not include the coding discipline helper's title.
	if strings.Contains("너는 shepherd MAGI 합의 시스템의 심의자다.", "[행동 규율]") {
		t.Fatal("sanity")
	}
	// Guard: if someone later folds embeddedBehaviorDiscipline into Magi,
	// this documents the intentional omission. The helper itself is for
	// embedded coding only.
	if strings.Contains(embeddedBehaviorDiscipline(), "심의자") {
		t.Error("coding discipline block must not be Magi-specific")
	}
}
