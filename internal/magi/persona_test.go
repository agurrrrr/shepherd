package magi

import (
	"strings"
	"testing"
)

func TestGetPersona_Melchior(t *testing.T) {
	p, ok := GetPersona("melchior")
	if !ok {
		t.Fatal("expected melchior persona to exist")
	}
	if p.DisplayName != "MELCHIOR-1" {
		t.Errorf("DisplayName = %q, want %q", p.DisplayName, "MELCHIOR-1")
	}
	if p.Emoji != "🔬" {
		t.Errorf("Emoji = %q, want %q", p.Emoji, "🔬")
	}
	if !strings.Contains(p.Prompt, "과학자") {
		t.Errorf("Prompt should contain '과학자', got: %q", p.Prompt)
	}
}

func TestGetPersona_Balthasar(t *testing.T) {
	p, ok := GetPersona("balthasar")
	if !ok {
		t.Fatal("expected balthasar persona to exist")
	}
	if p.DisplayName != "BALTHASAR-2" {
		t.Errorf("DisplayName = %q, want %q", p.DisplayName, "BALTHASAR-2")
	}
	if p.Emoji != "🛡" {
		t.Errorf("Emoji = %q, want %q", p.Emoji, "🛡")
	}
	if !strings.Contains(p.Prompt, "어머니") {
		t.Errorf("Prompt should contain '어머니', got: %q", p.Prompt)
	}
}

func TestGetPersona_Casper(t *testing.T) {
	p, ok := GetPersona("casper")
	if !ok {
		t.Fatal("expected casper persona to exist")
	}
	if p.DisplayName != "CASPER-3" {
		t.Errorf("DisplayName = %q, want %q", p.DisplayName, "CASPER-3")
	}
	if p.Emoji != "🎭" {
		t.Errorf("Emoji = %q, want %q", p.Emoji, "🎭")
	}
	if !strings.Contains(p.Prompt, "여성") {
		t.Errorf("Prompt should contain '여성', got: %q", p.Prompt)
	}
}

func TestGetPersona_UnknownKey(t *testing.T) {
	_, ok := GetPersona("nonexistent")
	if ok {
		t.Fatal("expected false for unknown persona key")
	}
}

func TestAllPersonasContainCommonRules(t *testing.T) {
	for _, key := range []string{"melchior", "balthasar", "casper"} {
		p, ok := GetPersona(key)
		if !ok {
			t.Fatalf("persona %q not found", key)
		}
		if !strings.Contains(p.Prompt, "심의 규칙") {
			t.Errorf("persona %q: prompt missing common rules block", key)
		}
		if !strings.Contains(p.Prompt, "CONFIDENCE") {
			t.Errorf("persona %q: prompt missing CONFIDENCE instruction", key)
		}
		if !strings.Contains(p.Prompt, "도구를 사용할 수 없다") {
			t.Errorf("persona %q: prompt missing tool restriction", key)
		}
		if !strings.Contains(p.Prompt, "독립적 결론") {
			t.Errorf("persona %q: prompt missing independence instruction", key)
		}
	}
}

func TestBuildProposerSystemPrompt_BuiltIn(t *testing.T) {
	base := "You are a helpful assistant."
	spec := ProposerSpec{
		PersonaKey: "melchior",
	}
	result := BuildProposerSystemPrompt(base, spec, 0)

	// Should contain the base prompt.
	if !strings.Contains(result, base) {
		t.Error("result should contain base prompt")
	}
	// Should contain persona block.
	if !strings.Contains(result, "MELCHIOR-1") {
		t.Error("result should contain MELCHIOR-1")
	}
	if !strings.Contains(result, "과학자") {
		t.Error("result should contain persona description")
	}
	// Should contain common deliberation rules.
	if !strings.Contains(result, "심의 규칙") {
		t.Error("result should contain common deliberation rules")
	}
}

func TestBuildProposerSystemPrompt_Custom(t *testing.T) {
	base := "Base system prompt."
	spec := ProposerSpec{
		PersonaKey:   "custom",
		CustomPrompt: "너는 실용주의 개발자다. 가장 빠른 해결책을 우선하라.",
	}
	result := BuildProposerSystemPrompt(base, spec, 2)

	// Should contain base prompt.
	if !strings.Contains(result, base) {
		t.Error("result should contain base prompt")
	}
	// Should contain custom prompt text.
	if !strings.Contains(result, "실용주의 개발자") {
		t.Error("result should contain custom prompt text")
	}
	// Should use CUSTOM-3 (slot 2, 0-based → N=3).
	if !strings.Contains(result, "CUSTOM-3") {
		t.Error("result should contain CUSTOM-3 for slot 2")
	}
	// Should contain common deliberation rules.
	if !strings.Contains(result, "심의 규칙") {
		t.Error("result should contain common deliberation rules")
	}
	if !strings.Contains(result, "CONFIDENCE") {
		t.Error("result should contain CONFIDENCE instruction")
	}
}

func TestBuildProposerSystemPrompt_EmptyBase(t *testing.T) {
	spec := ProposerSpec{
		PersonaKey: "balthasar",
	}
	result := BuildProposerSystemPrompt("", spec, 0)

	// With empty base, result should be just the persona block.
	if !strings.Contains(result, "BALTHASAR-2") {
		t.Error("result should contain BALTHASAR-2")
	}
	if strings.HasPrefix(result, "\n") {
		t.Error("result should not start with newline when base is empty")
	}
}

func TestPersonaDisplayName_BuiltIn(t *testing.T) {
	spec := ProposerSpec{PersonaKey: "casper"}
	if name := PersonaDisplayName(spec, 0); name != "CASPER-3" {
		t.Errorf("PersonaDisplayName = %q, want %q", name, "CASPER-3")
	}
}

func TestPersonaDisplayName_Custom(t *testing.T) {
	spec := ProposerSpec{PersonaKey: "custom"}
	if name := PersonaDisplayName(spec, 1); name != "CUSTOM-2" {
		t.Errorf("PersonaDisplayName = %q, want %q", name, "CUSTOM-2")
	}
}

func TestPersonaEmoji_BuiltIn(t *testing.T) {
	spec := ProposerSpec{PersonaKey: "melchior"}
	if emoji := PersonaEmoji(spec); emoji != "🔬" {
		t.Errorf("PersonaEmoji = %q, want %q", emoji, "🔬")
	}
}

func TestPersonaEmoji_Custom(t *testing.T) {
	spec := ProposerSpec{PersonaKey: "custom"}
	if emoji := PersonaEmoji(spec); emoji != "🔮" {
		t.Errorf("PersonaEmoji = %q, want %q", emoji, "🔮")
	}
}
