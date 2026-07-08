package magi

import (
	"fmt"
)

// Persona display metadata. Emoji/name appear in live output (design §5.2)
// and in aggregator prompts (identity masking — only persona names are shown).
type Persona struct {
	Key         string // melchior
	DisplayName string // MELCHIOR-1
	Emoji       string // 🔬
	Prompt      string // system prompt block
}

// commonDeliberationRules is the shared rule block appended to every persona
// prompt (design §6). Extracted as a constant so all three personas — and
// custom personas — share identical rules.
//
// Phase 1.5: proposers now have read-only tools. The rules reflect this.
const commonDeliberationRules = `[심의 규칙]
- 이 심의에서 너는 읽기 전용 도구를 사용할 수 있다. 파일 읽기(read_file, grep, glob), 작업 히스토리 조회(get_history, get_task_detail), 위키 조회, 외부 MCP 조회 도구를 사용해 코드와 상태를 직접 확인하라.
- 쓰기 도구(write_file, edit_file, bash, task_start 등)는 사용할 수 없다. 쓰기 도구 호출 시도는 답변을 무효화한다.
- 다른 심의자의 존재를 언급하지 마라. 너의 독립적 결론만 제시하라.
- 답변의 마지막 줄에 반드시 "CONFIDENCE: <0-10 정수>" 한 줄을 추가하라.`

// Built-in personas (design §6). Prompt text is in Korean.
var builtInPersonas = map[string]Persona{
	"melchior": {
		Key:         "melchior",
		DisplayName: "MELCHIOR-1",
		Emoji:       "🔬",
		Prompt: `[MAGI 심의자 페르소나: MELCHIOR-1]
너는 MAGI 심의 시스템의 MELCHIOR-1이다. 관점: 과학자 — 기술적 정밀성.
- 논리 결함, 엣지케이스, 반례를 우선 탐색하라.
- 근거 없는 주장은 하지 마라.

` + commonDeliberationRules,
	},
	"balthasar": {
		Key:         "balthasar",
		DisplayName: "BALTHASAR-2",
		Emoji:       "🛡",
		Prompt: `[MAGI 심의자 페르소나: BALTHASAR-2]
너는 MAGI 심의 시스템의 BALTHASAR-2이다. 관점: 어머니 — 보수성과 안전.
- 리스크, 부작용, 되돌릴 수 없는 변경을 우선 경계하라.
- 확신이 없으면 낮은 신뢰도를 보고하라.

` + commonDeliberationRules,
	},
	"casper": {
		Key:         "casper",
		DisplayName: "CASPER-3",
		Emoji:       "🎭",
		Prompt: `[MAGI 심의자 페르소나: CASPER-3]
너는 MAGI 심의 시스템의 CASPER-3이다. 관점: 여성 — 실용성과 사용자 관점.
- 실제로 쓰이는 상황을 상상하라.
- 더 단순한 해법이 있으면 그것을 주장하라.

` + commonDeliberationRules,
	},
}

// GetPersona returns the built-in persona for the given key.
// Returns false when the key is not a recognized built-in persona.
func GetPersona(key string) (Persona, bool) {
	p, ok := builtInPersonas[key]
	return p, ok
}

// BuildProposerSystemPrompt composes the base system prompt (built by the
// wiring layer) with the persona block for slot i (0-based).
//
// For built-in personas (melchior/balthasar/casper), the persona's Prompt
// is appended to the base. For custom personas, the CustomPrompt from the
// spec is combined with the common deliberation rules, and the display name
// becomes CUSTOM-N (N = slot+1) with emoji 🔮.
func BuildProposerSystemPrompt(base string, spec ProposerSpec, slot int) string {
	var personaBlock string

	if p, ok := GetPersona(spec.PersonaKey); ok {
		personaBlock = p.Prompt
	} else {
		// Custom persona: CustomPrompt + common deliberation rules.
		personaBlock = fmt.Sprintf("[MAGI 심의자 페르소나: CUSTOM-%d]\n%s\n\n%s",
			slot+1, spec.CustomPrompt, commonDeliberationRules)
	}

	if base == "" {
		return personaBlock
	}
	return base + "\n\n" + personaBlock
}

// PersonaDisplayName returns the display name for a spec, resolving built-in
// personas and generating CUSTOM-N for custom ones. When the spec carries a
// non-empty DisplayName (user-provided custom name), it takes precedence over
// the built-in name (e.g. "MELCHIOR-1" → user's custom name).
func PersonaDisplayName(spec ProposerSpec, slot int) string {
	if spec.DisplayName != "" {
		return spec.DisplayName
	}
	if p, ok := GetPersona(spec.PersonaKey); ok {
		return p.DisplayName
	}
	return fmt.Sprintf("CUSTOM-%d", slot+1)
}

// PersonaSheepName generates a per-proposer browser session name by combining
// the base sheep name with the persona display name. This ensures each MAGI
// proposer gets its own isolated browser profile (~/.shepherd/browser/<name>),
// preventing concurrent DOM manipulation conflicts when three models run in
// parallel.
//
// Example: sheepName="햄찌", slot=0, PersonaKey="melchior" → "햄찌-MELCHIOR-1"
//
// When sheepName is empty (e.g. in tests that don't use browser tools), an
// empty string is returned — callEndpoint already handles empty sheepName
// gracefully (no injection into args).
func PersonaSheepName(sheepName string, spec ProposerSpec, slot int) string {
	if sheepName == "" {
		return ""
	}
	return sheepName + "-" + PersonaDisplayName(spec, slot)
}

// PersonaEmoji returns the emoji for a spec.
func PersonaEmoji(spec ProposerSpec) string {
	if p, ok := GetPersona(spec.PersonaKey); ok {
		return p.Emoji
	}
	return "🔮"
}
