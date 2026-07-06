package magi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Content gate (lesson from task #7031): Phase 1 deliberation is tool-free,
// but models trained for agentic use may still emit tool-call markup as their
// entire answer. Such output carries no content and must count as a failure —
// otherwise SuccessfulResults passes it through, the judge packages a single
// substantive answer as "consensus", and the insufficient-proposers fallback
// (which DOES have tools) never engages.

// toolCallMarkers are tool-invocation tokens observed across local model
// families (qwen, GLM, mistral, ...). Presence alone is not a failure —
// the answer fails only when little prose remains after stripping the blocks.
var toolCallMarkers = []string{
	"<tool_call>",
	"<|tool_call|>",
	"<function_call>",
	"<tool_code>",
	"[TOOL_CALLS]",
	"[TOOL_REQUEST]",
}

// toolCallBlockRe strips paired tool-call blocks so the remaining prose can
// be measured.
var toolCallBlockRe = regexp.MustCompile(
	`(?s)<tool_call>.*?</tool_call>` +
		`|<\|tool_call\|>.*?<\|/tool_call\|>` +
		`|<function_call>.*?</function_call>` +
		`|<tool_code>.*?</tool_code>`)

// minSubstantiveRunes is the minimum prose length that must remain after
// stripping tool-call markup. A short preamble like "먼저 코드를 확인하겠습니다."
// followed by a tool call is not an answer.
const minSubstantiveRunes = 120

// CheckAnswerContent returns a non-nil error when the answer carries no
// substantive content: empty text, a bare tool-call JSON object, or tool-call
// markup with (almost) no prose around it. Callers treat the error like a
// transport failure so the wiring-layer fallback can engage (design §5.1).
func CheckAnswerContent(answer string) error {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return fmt.Errorf("no substantive answer (empty)")
	}

	// Whole answer is one tool-call JSON object (bare or code-fenced).
	if isBareToolCallJSON(trimmed) || isBareToolCallJSON(stripCodeFence(trimmed)) {
		return fmt.Errorf("no substantive answer (tool-call attempt)")
	}

	hasMarker := false
	for _, m := range toolCallMarkers {
		if strings.Contains(trimmed, m) {
			hasMarker = true
			break
		}
	}
	if !hasMarker {
		return nil
	}

	// Strip paired blocks, then truncate at the first unpaired marker —
	// an unclosed block (stream cut mid-call) runs to the end of the answer.
	remainder := toolCallBlockRe.ReplaceAllString(trimmed, "")
	for _, m := range toolCallMarkers {
		if idx := strings.Index(remainder, m); idx >= 0 {
			remainder = remainder[:idx]
		}
	}

	if len([]rune(strings.TrimSpace(remainder))) < minSubstantiveRunes {
		return fmt.Errorf("no substantive answer (tool-call attempt)")
	}

	return nil
}

// isBareToolCallJSON reports whether s is a single JSON object shaped like a
// tool invocation: {"name": ..., "arguments"/"parameters": ...}.
func isBareToolCallJSON(s string) bool {
	if !strings.HasPrefix(s, "{") {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return false
	}
	if _, ok := obj["name"]; !ok {
		return false
	}
	_, hasArgs := obj["arguments"]
	_, hasParams := obj["parameters"]
	return hasArgs || hasParams
}
