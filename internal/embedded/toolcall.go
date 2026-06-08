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
func tryParseToolCallJSON(s string) *ToolCall {
	s = stripCodeFence(s)
	s = normalizeString(s)

	// Try as direct function call JSON: {"name":"xxx","arguments":{"key":"val"}}
	var fc ToolCallFunction
	if err := json.Unmarshal([]byte(s), &fc); err == nil && fc.Name != "" {
		return &ToolCall{
			ID:   "leaked-" + fc.Name,
			Type: "function",
			Func: fc,
		}
	}

	// Try as full tool call JSON: {"id":"xxx","type":"function","function":{"name":"xxx","arguments":"{}"}}
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
func normalizeJSON(raw string) (map[string]interface{}, error) {
	s := raw
	s = stripCodeFence(s)
	s = stripTrailingCommas(s)
	s = normalizeQuotes(s)
	s = strings.TrimSpace(s)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %w (input: %q)", err, s)
	}
	return result, nil
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
