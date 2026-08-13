package worker

import (
	"strings"
	"testing"
)

func TestGrokLiveBuf_BuffersThoughtTokens(t *testing.T) {
	var out []string
	buf := newGrokLiveBuf(func(s string) { out = append(out, s) })

	// Simulate the unbuffered path that caused #7201: 💭 + per-token thought.
	buf.Write("💭 ")
	for _, tok := range []string{"The", " user", " is", " attaching", " screens", "hots"} {
		buf.Append(tok)
	}
	buf.Flush()

	if len(out) != 1 {
		t.Fatalf("expected 1 flush, got %d: %#v", len(out), out)
	}
	got := out[0]
	if !strings.HasPrefix(got, "💭 ") {
		t.Errorf("missing thought marker: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("should not insert newlines between tokens: %q", got)
	}
	if want := "💭 The user is attaching screenshots"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGrokLiveBuf_FlushOnNewline(t *testing.T) {
	var out []string
	buf := newGrokLiveBuf(func(s string) { out = append(out, s) })

	buf.Append("hello ")
	buf.Append("world\n")
	buf.Append("next")
	buf.Flush()

	if len(out) != 2 {
		t.Fatalf("expected 2 flushes, got %d: %#v", len(out), out)
	}
	if out[0] != "hello world\n" {
		t.Errorf("line1: got %q", out[0])
	}
	if out[1] != "next" {
		t.Errorf("line2: got %q", out[1])
	}
}

func TestGrokLiveBuf_SafetyFlushPrefersWordBoundary(t *testing.T) {
	var out []string
	buf := newGrokLiveBuf(func(s string) { out = append(out, s) })

	// 80 spaces-separated words of 4 chars + space ≈ well over 120 bytes,
	// with a clear word boundary near the end of the first safety flush.
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("word ")
	}
	// "word " * 40 = 200 bytes
	buf.Append(b.String())
	// Don't Flush — safety flush should have already fired.

	if len(out) == 0 {
		t.Fatal("expected safety flush at >=120 bytes")
	}
	first := out[0]
	if !strings.HasSuffix(first, " ") {
		t.Errorf("safety flush should end on word boundary (space), got %q", first)
	}
	if strings.Contains(strings.TrimSpace(first), "wordword") {
		t.Errorf("mid-word join: %q", first)
	}
	// Remainder stays buffered until Flush.
	buf.Flush()
	combined := strings.Join(out, "")
	if got, want := combined, b.String(); got != want {
		t.Errorf("reconstructed %q != original %q", got, want)
	}
}

func TestGrokLiveBuf_SectionSwitchFlush(t *testing.T) {
	var out []string
	buf := newGrokLiveBuf(func(s string) { out = append(out, s) })

	buf.Write("💭 ")
	buf.Append("thinking")
	buf.Flush() // section switch thought → text
	buf.Append("answer text")
	buf.Flush()

	if len(out) != 2 {
		t.Fatalf("expected 2 flushes, got %d: %#v", len(out), out)
	}
	if out[0] != "💭 thinking" {
		t.Errorf("thought: %q", out[0])
	}
	if out[1] != "answer text" {
		t.Errorf("text: %q", out[1])
	}
}

func TestTagThoughtChunk(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"💭 already tagged", "💭 already tagged"},
		// Mid-line safety-flush remnant: do not inject another 💭.
		{"plain continuation", "plain continuation"},
		// New physical line without a marker: 3-space continuation, not 💭.
		{"\n\nplain after blank", "\n\n   plain after blank"},
		{"   indented cont", "   indented cont"},
		{"\n💭 has marker", "\n💭 has marker"},
	}
	for _, tc := range cases {
		got := tagThoughtChunk(tc.in)
		if got != tc.want {
			t.Errorf("tagThoughtChunk(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIndentThoughtData_DropsBlankLines(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"no newline", "no newline"},
		{"hello\nworld", "hello\n   world"},
		{"\n", "\n   "},
		{"\n\n", "\n   \n   "},
		{"hello\n\nworld", "hello\n   \n   world"},
		{"\nhello", "\n   hello"},
		{"hello\n", "hello\n   "},
		{"  \n\t\nkeep", "\n   \n   keep"},
	}
	for _, tc := range cases {
		got := indentThoughtData(tc.in)
		if got != tc.want {
			t.Errorf("indentThoughtData(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Thought "\n" tokens must not flush a whitespace-only line that the WebUI
// would classify as text (task #8109: Enter flashes then the prompt loops).
func TestGrokLiveBuf_ThoughtNewlineDoesNotLeakText(t *testing.T) {
	var out []string
	section := "thought"
	buf := newGrokLiveBuf(func(s string) {
		if strings.TrimSpace(s) == "" {
			return
		}
		if section == "thought" {
			s = tagThoughtChunk(s)
		}
		out = append(out, s)
	})

	buf.Write("\n💭 ")
	for _, tok := range []string{"The user prompt", "\n", "is being restated", "\n\n", "again"} {
		buf.Append(indentThoughtData(tok))
	}
	buf.Flush()

	if len(out) == 0 {
		t.Fatal("expected thought output")
	}
	combined := strings.Join(out, "")
	if strings.Contains(combined, "\n\n") {
		t.Errorf("blank lines leaked into thought stream: %#v", out)
	}
	for _, chunk := range out {
		if strings.TrimSpace(chunk) == "" {
			t.Errorf("whitespace-only chunk leaked: %q", chunk)
		}
		trimmed := strings.TrimLeft(chunk, "\n\r")
		if !strings.HasPrefix(trimmed, "💭") && !strings.HasPrefix(trimmed, "   ") {
			t.Errorf("chunk not classifiable as thinking: %q", chunk)
		}
	}
	if !strings.Contains(combined, "The user prompt") || !strings.Contains(combined, "again") {
		t.Errorf("lost thought text: %q", combined)
	}
}

func simulateGrokLive(events []grokEvent) []string {
	var out []string
	stream := newGrokStreamState(func(s string) { out = append(out, s) })
	for i := range events {
		stream.handle(&events[i])
	}
	stream.flush()
	return out
}

func coalesceChunks(chunks []string) []string {
	var lines []string
	c := NewLineCoalescer(func(s string) { lines = append(lines, s) })
	for _, ch := range chunks {
		c.Append(ch)
	}
	c.Flush()
	return lines
}

// Grok commonly emits a lone "\n" thought token, then a new sentence with no
// leading space. Dropping that newline (instead of turning it into a
// continuation indent) glues the sentences together.
func TestIndentThoughtData_LoneNewlineKeepsBreakForNextToken(t *testing.T) {
	got := indentThoughtData("\n")
	if got == "" {
		t.Fatalf("lone newline token was discarded; next sentence will glue onto the previous one")
	}
	if !strings.Contains(got, "\n") {
		t.Fatalf("lone newline token lost its line break: %q", got)
	}
}

func TestGrokThoughtPipeline_NewlineTokenDoesNotGlueSentences(t *testing.T) {
	chunks := simulateGrokLive([]grokEvent{
		{Type: "thought", Data: "The user wants to investigate."},
		{Type: "thought", Data: "\n"},
		{Type: "thought", Data: "They mentioned grok-safe."},
		{Type: "thought", Data: "\n\n"},
		{Type: "thought", Data: "This is a restatement of the prompt."},
		{Type: "text", Data: "설정을 정리했습니다."},
	})
	lines := coalesceChunks(chunks)
	joined := strings.Join(lines, "")

	if strings.Contains(joined, "investigate.They") {
		t.Errorf("sentences glued across a thought newline token:\nchunks=%q\nlines=%q", chunks, lines)
	}
	if strings.Contains(joined, "grok-safe.This") {
		t.Errorf("paragraph break collapsed into glued sentences:\nchunks=%q\nlines=%q", chunks, lines)
	}
	if !strings.Contains(joined, "The user wants to investigate.") {
		t.Errorf("lost first thought sentence: %q", joined)
	}
	if !strings.Contains(joined, "They mentioned grok-safe.") {
		t.Errorf("lost second thought sentence: %q", joined)
	}
	if !strings.Contains(joined, "설정을 정리했습니다.") {
		t.Errorf("lost answer text: %q", joined)
	}

	// After LineCoalescer, no whitespace-only line should sit between
	// thinking lines in a way that classifyLine would treat as text —
	// except the thought→text "\n\n" separator.
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			continue
		}
		// Allowed: the explicit thought→text separator.
		if i > 0 && i < len(lines)-1 && strings.Contains(lines[i+1], "설정을") {
			continue
		}
		// Leading "" from "\n💭 " at section start is also expected.
		if i == 0 {
			continue
		}
		t.Errorf("whitespace-only line %d leaked into coalesced output: %#v", i, lines)
	}
}

// Live grok 1.0.3 streaming-json (2026-08-13) attaches the newline to the
// last thought token (".\n"), not as a lone "\n". The next thought sentence
// still has no leading space.
func TestGrokThoughtPipeline_TrailingNLOnLastToken(t *testing.T) {
	chunks := simulateGrokLive([]grokEvent{
		{Type: "thought", Data: "The user wants me to reply with exactly the word OK"},
		{Type: "thought", Data: ".\n"},
		{Type: "thought", Data: "They said think one short sentence first."},
		{Type: "text", Data: "OK"},
	})
	joined := strings.Join(coalesceChunks(chunks), "")
	if strings.Contains(joined, "OKThey") || strings.Contains(joined, "OK.They") && !strings.Contains(joined, "OK.\n") {
		// "OKThey" would mean the period+NL was dropped and glued.
		t.Errorf("trailing thought newline glued the next sentence: %q", joined)
	}
	if strings.Contains(joined, "OKThey") {
		t.Errorf("glued across .\\n token: %q", joined)
	}
	if !strings.Contains(joined, "They said think") {
		t.Errorf("lost continuation thought: %q", joined)
	}
	// Prefer a line break (or at least a space) between the two sentences.
	if strings.Contains(joined, "OK.They") {
		t.Errorf("sentences glued after trailing .\\n token: %q", joined)
	}
}

func TestGrokThoughtPipeline_LiveCaptureOKTokens(t *testing.T) {
	// Exact thought/text deltas from grok 1.0.3 streaming-json
	// (`-p "Reply with exactly the word OK..."` on 2026-08-13).
	thoughts := []string{
		"The", " user", " wants", " me", " to", " reply", " with",
		" exactly", " the", " word", " OK", ".", " They", " said",
		" think", " one", " short", " sentence", " first", ",", " and",
		" do", " not", " use", " tools", ".\n",
	}
	var evs []grokEvent
	for _, tok := range thoughts {
		evs = append(evs, grokEvent{Type: "thought", Data: tok})
	}
	evs = append(evs, grokEvent{Type: "text", Data: "OK"})

	chunks := simulateGrokLive(evs)
	lines := coalesceChunks(chunks)
	joined := strings.Join(lines, "")
	if !strings.Contains(joined, "💭") {
		t.Fatalf("missing thought marker: %#v", lines)
	}
	if !strings.Contains(joined, "The user wants me to reply") {
		t.Errorf("lost thought text: %q", joined)
	}
	if !strings.Contains(joined, "OK") {
		t.Errorf("lost answer: %q", joined)
	}
	// Answer must not be classified inside the thought marker line.
	for _, line := range lines {
		if strings.Contains(line, "💭") && strings.HasSuffix(strings.TrimSpace(line), "OK") && !strings.Contains(line, "word OK") {
			t.Errorf("answer glued onto thinking line: %q", line)
		}
	}
}

// A long thought paragraph without newlines used to pick up a 💭 every
// ≥120B safety flush. LineCoalescer then joined those onto one line, so
// the Thinking card showed a marker at every wrap.
func TestGrokThoughtPipeline_SafetyFlushDoesNotInsertMidLineMarker(t *testing.T) {
	// ~250B of space-separated English, typical Grok thought shape.
	words := []string{
		"The", " user", " wants", " me", " to", " review", " recent",
		" commits", " related", " to", " Grok", " parsing", " improvements",
		" and", " then", " test", " them", " thoroughly", " before",
		" declaring", " the", " work", " complete", " so", " we", " do",
		" not", " miss", " a", " regression", " in", " the", " live",
		" output", " pipeline", " when", " thinking", " blocks", " wrap",
		" across", " multiple", " visual", " lines", ".",
	}
	var evs []grokEvent
	for _, tok := range words {
		evs = append(evs, grokEvent{Type: "thought", Data: tok})
	}
	evs = append(evs, grokEvent{Type: "text", Data: "확인했습니다."})

	chunks := simulateGrokLive(evs)
	joined := strings.Join(chunks, "")
	if n := strings.Count(joined, "💭"); n != 1 {
		t.Errorf("expected a single 💭 section marker, got %d in chunks=%q", n, chunks)
	}
	if strings.Contains(joined, " 💭 ") {
		t.Errorf("mid-line 💭 re-tag leaked into thought stream: %q", joined)
	}

	lines := coalesceChunks(chunks)
	coalesced := strings.Join(lines, "")
	if n := strings.Count(coalesced, "💭"); n != 1 {
		t.Errorf("LineCoalescer should keep a single 💭, got %d in lines=%q", n, lines)
	}
	if !strings.Contains(coalesced, "review recent commits") {
		t.Errorf("lost thought text: %q", coalesced)
	}
	if !strings.Contains(coalesced, "확인했습니다.") {
		t.Errorf("lost answer text: %q", coalesced)
	}
}

func TestParseGrokOutput_RealStreamShape(t *testing.T) {
	// Minimal NDJSON matching grok 1.0.3: available_commands + thought + text + end.
	raw := strings.Join([]string{
		`{"type":"available_commands","tools":["read_file"]}`,
		`{"type":"thought","data":"The user wants "}`,
		`{"type":"thought","data":"OK.\n"}`,
		`{"type":"text","data":"OK"}`,
		`{"type":"usage","usage":{"input_tokens":10,"output_tokens":2}}`,
		`{"type":"end","stopReason":"end_turn","sessionId":"sess-1"}`,
	}, "\n")
	got := parseGrokOutput(raw)
	if got.Result != "OK" {
		t.Errorf("result=%q want OK", got.Result)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("session=%q", got.SessionID)
	}
	if got.Incomplete {
		t.Errorf("unexpected incomplete: %s", got.IncompleteReason)
	}
}
