package embedded

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// leakedToolCallMarkers defines pairs of markers that indicate a leaked tool call
// embedded in the model's text output.
type leakedMarkerPair struct {
	open string
	close string
}

var leakedMarkerPairs = []leakedMarkerPair{
	{"<tool_call>", "</tool_call>"},
	{"<|tool▁calls▁begin|>", "<|tool▁calls▁end|>"},
	{"<function=", "</function>"},
	{"<tools_call>", "</tools_call>"},
	{"[tool_call", "[/tool_call]"},
}

// looksLikeLeakedToolCall checks if the text contains tool call markers that
// leaked into the model's text output instead of being structured as tool_calls.
func looksLikeLeakedToolCall(text string) bool {
	for _, pair := range leakedMarkerPairs {
		if strings.Contains(text, pair.open) {
			return true
		}
	}
	return false
}

// ParsedToolCall is a tool call extracted from leaked text.
type ParsedToolCall struct {
	ToolCall
	rawText string // for debugging
}

// parseLeakedToolCalls extracts tool calls from text that contains leaked
// tool call markers. Returns parsed tool calls or empty slice if none found.
func parseLeakedToolCalls(text string) []*ParsedToolCall {
	var results []*ParsedToolCall

	for _, pair := range leakedMarkerPairs {
		// Find all occurrences between open/close markers
		index := 0
		for {
			openIdx := strings.Index(text[index:], pair.open)
			if openIdx == -1 {
				break
			}
			openIdx += index

			closeIdx := strings.Index(text[openIdx+len(pair.open):], pair.close)
			if closeIdx == -1 {
				break
			}
			closeIdx += openIdx + len(pair.open)

			block := text[openIdx : closeIdx+len(pair.close)]
			parsed := tryParseToolCallBlock(block, pair)
			if parsed != nil {
				results = append(results, parsed...)
			}

			index = closeIdx + len(pair.close)
		}
	}

	return results
}

// tryParseToolCallBlock attempts to extract tool call JSON from a marker block.
func tryParseToolCallBlock(block string, pair leakedMarkerPair) []*ParsedToolCall {
	// Remove markers
	content := block
	content = strings.TrimPrefix(content, pair.open)
	content = strings.TrimSuffix(content, pair.close)
	content = strings.TrimSpace(content)

	if content == "" {
		return nil
	}

	// Try to parse as direct JSON object
	tc := tryParseToolCallJSON(content)
	if tc != nil {
		return []*ParsedToolCall{{ToolCall: *tc, rawText: block}}
	}

	// Try to find JSON objects within the block
	return findJSONObjects(content, block)
}

// tryParseToolCallJSON attempts to parse a string as a tool call JSON object.
// Handles three formats:
//
//  1. {"name":"bash","arguments":{"command":"ls"}}   — Qwen3 <tool_call> style,
//     arguments is an inline JSON object
//  2. {"name":"bash","arguments":"{\"command\":\"ls\"}"} — arguments is a JSON string
//  3. {"id":"…","type":"function","function":{"name":"…","arguments":"…"}}  — OpenAI format
func tryParseToolCallJSON(s string) *ToolCall {
	s = stripCodeFence(s)
	s = normalizeString(s)

	// Format 1 & 2: {"name":"xxx","arguments": <object or string>}
	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err == nil && raw.Name != "" {
		argsStr := string(raw.Arguments)
		// If arguments is an object/array, keep it as JSON string; otherwise unwrap quotes.
		if len(argsStr) > 0 && (argsStr[0] == '{' || argsStr[0] == '[') {
			// already a JSON object — use as-is
		} else {
			// It's a JSON-encoded string: unwrap the outer quotes
			var decoded string
			if err := json.Unmarshal(raw.Arguments, &decoded); err == nil {
				argsStr = decoded
			}
		}
		return &ToolCall{
			ID:   "leaked-" + raw.Name,
			Type: "function",
			Func: ToolCallFunction{Name: raw.Name, Args: argsStr},
		}
	}

	// Format 3: full OpenAI tool call JSON
	var tc ToolCall
	if err := json.Unmarshal([]byte(s), &tc); err == nil && tc.Func.Name != "" {
		return &tc
	}

	return nil
}

// findJSONObjects searches for JSON objects embedded in text.
func findJSONObjects(text, rawBlock string) []*ParsedToolCall {
	var results []*ParsedToolCall

	// Find all {...} blocks
	re := regexp.MustCompile(`\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}`)
	matches := re.FindAllString(text, -1)

	for _, match := range matches {
		tc := tryParseToolCallJSON(match)
		if tc != nil {
			results = append(results, &ParsedToolCall{ToolCall: *tc, rawText: rawBlock})
		}
	}

	return results
}

// normalizeJSON attempts to fix common JSON issues from model output.
// If standard parsing fails, it attempts to repair truncated JSON before giving up.
func normalizeJSON(raw string) (map[string]interface{}, error) {
	s := raw
	s = stripCodeFence(s)
	s = stripTrailingCommas(s)
	s = normalizeQuotes(s)
	s = strings.TrimSpace(s)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(s), &result); err == nil {
		return result, nil
	}

	// Standard parse failed — attempt structural repair (truncated strings/objects).
	repaired := repairTruncatedJSON(s)
	if repaired != s {
		if err := json.Unmarshal([]byte(repaired), &result); err == nil {
			return result, nil
		}
	}

	// If repair still fails, try truncating at the last valid comma-separated pair
	// (handles mid-key truncation like `{"command":"ls", "desc`).
	if trimmed := trimToLastCompletePair(repaired); trimmed != repaired {
		if err := json.Unmarshal([]byte(trimmed), &result); err == nil {
			return result, nil
		}
	}

	return nil, fmt.Errorf("JSON parse failed: %w (input: %q)", fmt.Errorf("unable to parse or repair"), s)
}

// repairTruncatedJSON closes any open JSON string and brace/bracket pairs that
// were cut off mid-stream. This handles the most common Qwen3 truncation patterns:
//
//   - `{"command":"ls -la`               → adds `"}`
//   - `{"command":"ls -la"`              → adds `}`
//   - `{"command":"curl -A \"`           → the \" is an escaped quote still inside the
//     string — adds `"}` (value = `curl -A "`)
//   - `{"command":"ls\`                  → dangling backslash removed, then adds `"}`
func repairTruncatedJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	inString := false
	escaped := false
	depth := 0

	for _, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if !inString {
			switch ch {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}

	// If the string ended with a dangling backslash (incomplete escape sequence),
	// remove it so the string can be closed cleanly.
	if escaped && len(s) > 0 && s[len(s)-1] == '\\' {
		s = s[:len(s)-1]
	}

	if inString {
		s += `"`
	}

	for depth > 0 {
		s += "}"
		depth--
	}

	return s
}

// trimToLastCompletePair truncates a JSON object at the last comma, returning
// a closed object that ends at a clean key-value boundary. Used when a key or
// value was only partially written before truncation.
//
// Example: `{"command":"ls", "desc` → `{"command":"ls"}`
func trimToLastCompletePair(s string) string {
	// Only operate on object-like strings
	if !strings.HasPrefix(s, "{") {
		return s
	}
	lastComma := strings.LastIndex(s, ",")
	if lastComma <= 0 {
		return s
	}
	candidate := strings.TrimRight(s[:lastComma], " \t\r\n") + "}"
	var probe map[string]interface{}
	if json.Unmarshal([]byte(candidate), &probe) == nil {
		return candidate
	}
	return s
}

// stripCodeFence removes markdown code fences (```json ... ```).
func stripCodeFence(s string) string {
	backticks := "```"
	// Remove ```json or ``` at start
	s = regexp.MustCompile("^"+backticks+"(?:json)?\\s*").ReplaceAllString(s, "")
	// Remove ``` at end
	s = regexp.MustCompile("\\s*" + backticks + "\\s*$").ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// stripTrailingCommas removes trailing commas before } or ].
func stripTrailingCommas(s string) string {
	// Remove comma before } or ]
	s = regexp.MustCompile(`,\s*([}\]])`).ReplaceAllString(s, "$1")
	return s
}

// normalizeQuotes converts single quotes to double quotes for JSON compatibility.
// This is a simple heuristic and may not handle all edge cases.
func normalizeQuotes(s string) string {
	// Only convert if there are no double quotes (to avoid breaking already-valid JSON)
	if !strings.Contains(s, `"`) && strings.Contains(s, `'`) {
		return strings.ReplaceAll(s, `'`, `"`)
	}
	return s
}

// normalizeString applies common normalizations to a string.
func normalizeString(s string) string {
	s = strings.TrimSpace(s)
	s = stripCodeFence(s)
	s = stripTrailingCommas(s)
	return s
}
