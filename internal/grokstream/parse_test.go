package grokstream

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestParseLine_LegacyThoughtTextEnd(t *testing.T) {
	thought := ParseLine(`{"type":"thought","data":"The user wants "}`)
	if thought == nil || thought.Type != "thought" || thought.Data != "The user wants " {
		t.Fatalf("thought: %+v", thought)
	}
	text := ParseLine(`{"type":"text","data":"OK"}`)
	if text == nil || text.Type != "text" || text.Data != "OK" {
		t.Fatalf("text: %+v", text)
	}
	end := ParseLine(`{"type":"end","stopReason":"end_turn","sessionId":"sess-1"}`)
	if end == nil || end.Type != "end" || end.SessionID != "sess-1" || end.StopReason != "end_turn" {
		t.Fatalf("end: %+v", end)
	}
}

func TestParseLine_IgnoresNoise(t *testing.T) {
	cases := []string{
		"",
		"not json",
		`{"type":"available_commands","tools":["read_file"]}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"plan","entries":[]}}}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","status":"completed"}}}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"hook_execution"}}}`,
	}
	// available_commands has type set so it is returned, but is not thought/text.
	if ev := ParseLine(cases[2]); ev == nil || ev.Type != "available_commands" {
		t.Fatalf("legacy noise should still decode type, got %+v", ev)
	}
	for _, line := range []string{cases[0], cases[1], cases[3], cases[4], cases[5]} {
		if ev := ParseLine(line); ev != nil && ev.Type != "" && ev.Type != "available_commands" {
			if ev.Type == "thought" || ev.Type == "text" || ev.Type == "end" || ev.Type == "tool" {
				t.Errorf("ParseLine(%q) surfaced %+v", line, ev)
			}
		}
	}
	if ev := ParseLine(cases[3]); ev != nil {
		t.Errorf("plan should be ignored, got %+v", ev)
	}
	if ev := ParseLine(cases[4]); ev != nil {
		t.Errorf("tool_call_update should be ignored, got %+v", ev)
	}
}

func TestParseLine_ACPThoughtMessageEnd(t *testing.T) {
	thought := ParseLine(`{"method":"session/update","params":{"sessionId":"sid-1","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"Let me look.\n"}}}}`)
	if thought == nil || thought.Type != "thought" || thought.Data != "Let me look.\n" || thought.SessionID != "sid-1" {
		t.Fatalf("thought: %+v", thought)
	}
	text := ParseLine(`{"method":"session/update","params":{"sessionId":"sid-1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"확인했습니다."}}}}`)
	if text == nil || text.Type != "text" || text.Data != "확인했습니다." {
		t.Fatalf("text: %+v", text)
	}
	end := ParseLine(`{"method":"_x.ai/session/update","params":{"sessionId":"sid-1","update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn"}}}`)
	if end == nil || end.Type != "end" || end.SessionID != "sid-1" || end.StopReason != "end_turn" {
		t.Fatalf("end: %+v", end)
	}
}

func TestParseLine_ACPToolCallLabel(t *testing.T) {
	read := ParseLine(`{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","title":"read_file","rawInput":{"target_file":"/tmp/a.go"}}}}`)
	if read == nil || read.Type != "tool" || read.Data != "read_file → /tmp/a.go" {
		t.Fatalf("read_file: %+v", read)
	}
	mcp := ParseLine(`{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","title":"use_tool","rawInput":{"tool_name":"shepherd__get_history","tool_input":{"project_name":"shepherd"}}}}}`)
	if mcp == nil || mcp.Type != "tool" || mcp.Data != "shepherd__get_history" {
		t.Fatalf("use_tool: %+v", mcp)
	}
	cmd := ParseLine(`{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","title":"run_terminal_command","rawInput":{"command":"git status"}}}}`)
	if cmd == nil || cmd.Data != "run_terminal_command → git status" {
		t.Fatalf("command: %+v", cmd)
	}
}

func TestParseLine_ACPToolDetailTruncated(t *testing.T) {
	long := strings.Repeat("x", 120)
	line := `{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","title":"run_terminal_command","rawInput":{"command":"` + long + `"}}}}`
	ev := ParseLine(line)
	if ev == nil || !strings.HasPrefix(ev.Data, "run_terminal_command → ") {
		t.Fatalf("got %+v", ev)
	}
	detail := strings.TrimPrefix(ev.Data, "run_terminal_command → ")
	if !strings.HasSuffix(detail, "...") || len([]rune(detail)) != 83 { // 80 + "..."
		t.Fatalf("detail not truncated to 80 runes: %q (%d runes)", detail, len([]rune(detail)))
	}
}

func TestReadAndParse_RealDebrisSessionIfPresent(t *testing.T) {
	p := os.Getenv("HOME") + "/.grok/sessions/%2Fhome%2Fagurrrrr%2Fcode%2Fdebris/01a01041-d318-7b73-b3b9-430fff3b16b4/updates.jsonl"
	f, err := os.Open(p)
	if err != nil {
		t.Skip("local #8165 session not present")
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)
	var thought, text, tool, end, trunc int
	var answer strings.Builder
	var sid, stop string
	for {
		line, truncated, err := ReadCappedLine(r, MaxLineBytes)
		if err != nil {
			break
		}
		if truncated {
			trunc++
		}
		ev := ParseLine(line)
		if ev == nil {
			continue
		}
		switch ev.Type {
		case "thought":
			thought++
		case "text":
			text++
			answer.WriteString(ev.Data)
		case "tool":
			tool++
		case "end":
			end++
			stop = ev.StopReason
		}
		if ev.SessionID != "" {
			sid = ev.SessionID
		}
	}
	if thought == 0 || text == 0 || tool == 0 || end != 1 {
		t.Fatalf("counts thought=%d text=%d tool=%d end=%d trunc=%d", thought, text, tool, end, trunc)
	}
	if trunc == 0 {
		t.Fatal("expected some truncated 1MB+ tool_call_update lines")
	}
	if sid != "01a01041-d318-7b73-b3b9-430fff3b16b4" {
		t.Errorf("session %q", sid)
	}
	if stop != "end_turn" {
		t.Errorf("stop %q", stop)
	}
	if !strings.Contains(answer.String(), "이슈") {
		t.Errorf("answer missing expected hangul: %q", truncateRunes(answer.String(), 80))
	}
}

func TestExtractText_ACPAndLegacy(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"thought","data":"ignore"}`,
		`{"type":"text","data":"Hello "}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"text":"world"}}}}`,
		`{"method":"session/update","params":{"update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn"}}}`,
	}, "\n")
	if got := ExtractText(raw); got != "Hello world" {
		t.Fatalf("ExtractText = %q", got)
	}
}
