package grokstream

import (
	"encoding/json"
	"strings"
)

// Event is one grok headless stream event after format normalization.
//
// Grok CLI 1.0.3 changed --output-format streaming-json from token-delta
// lines ({"type":"thought"|"text"|"end"}) to ACP session/update NDJSON.
// ParseLine accepts both and always returns this legacy-shaped event.
type Event struct {
	Type       string `json:"type"`                 // thought | text | tool | end | error
	Data       string `json:"data,omitempty"`       // token/chunk text, or tool label
	StopReason string `json:"stopReason,omitempty"` // end event
	SessionID  string `json:"sessionId,omitempty"`  // end event (also filled on ACP lines)
	Message    string `json:"message,omitempty"`    // error event
}

type acpLine struct {
	Method string `json:"method"`
	Params struct {
		SessionID string    `json:"sessionId"`
		Update    acpUpdate `json:"update"`
	} `json:"params"`
}

type acpUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
	Title         string          `json:"title"`
	RawInput      json.RawMessage `json:"rawInput"`
	Status        string          `json:"status"`
	StopReason    string          `json:"stop_reason"`
}

// ParseLine decodes one grok streaming-json / ACP NDJSON line.
// Returns nil for blank, non-JSON, or events we do not surface
// (plan, hook_execution, tool_call_update, usage, …).
func ParseLine(line string) *Event {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return nil
	}
	var peek struct {
		Type   string `json:"type"`
		Method string `json:"method"`
	}
	if json.Unmarshal([]byte(line), &peek) != nil {
		return nil
	}
	if peek.Type != "" {
		var ev Event
		if json.Unmarshal([]byte(line), &ev) != nil {
			return nil
		}
		return &ev
	}
	if isACPMethod(peek.Method) {
		return parseACP(line)
	}
	return nil
}

func isACPMethod(m string) bool {
	return m == "session/update" || m == "_x.ai/session/update"
}

func parseACP(line string) *Event {
	var env acpLine
	if json.Unmarshal([]byte(line), &env) != nil {
		return nil
	}
	ev := &Event{SessionID: env.Params.SessionID}
	switch env.Params.Update.SessionUpdate {
	case "agent_thought_chunk":
		ev.Type = "thought"
		ev.Data = acpContentText(env.Params.Update.Content)
		if ev.Data == "" {
			return nil
		}
	case "agent_message_chunk":
		ev.Type = "text"
		ev.Data = acpContentText(env.Params.Update.Content)
		if ev.Data == "" {
			return nil
		}
	case "tool_call":
		ev.Type = "tool"
		ev.Data = toolLabel(env.Params.Update.Title, env.Params.Update.RawInput)
		if ev.Data == "" {
			return nil
		}
	case "turn_completed":
		ev.Type = "end"
		ev.StopReason = env.Params.Update.StopReason
		if ev.StopReason == "" {
			ev.StopReason = "end_turn"
		}
	default:
		// tool_call_update, plan, hook_execution, user_message_chunk, …
		return nil
	}
	return ev
}

func acpContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Text != "" {
		return obj.Text
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// toolLabel builds a short "name → detail" string from an ACP tool_call.
// rawInput can be large on other events; tool_call starts are small.
func toolLabel(title string, raw json.RawMessage) string {
	name := strings.TrimSpace(title)
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		if name == "" || name == "use_tool" {
			if n := jsonString(obj["tool_name"]); n != "" {
				name = n
			}
		}
		for _, key := range []string{
			"command", "target_file", "file_path", "target_directory",
			"query", "pattern", "url",
		} {
			if v := jsonString(obj[key]); v != "" {
				if name == "" {
					name = title
				}
				return name + " → " + truncateRunes(v, 80)
			}
		}
	}
	return name
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func truncateRunes(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// ExtractText concatenates every text (agent_message_chunk / type=text) delta.
func ExtractText(output string) string {
	var b strings.Builder
	for _, line := range strings.Split(output, "\n") {
		ev := ParseLine(line)
		if ev != nil && ev.Type == "text" {
			b.WriteString(ev.Data)
		}
	}
	return strings.TrimSpace(b.String())
}
