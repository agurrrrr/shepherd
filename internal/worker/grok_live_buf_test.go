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
		{"plain continuation", "💭 plain continuation"},
		{"\n\nplain after blank", "\n\n💭 plain after blank"},
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
		{"\n", ""},
		{"\n\n", ""},
		{"hello\n\nworld", "hello\n   world"},
		{"\nhello", "hello"},
		{"hello\n", "hello"},
		{"  \n\t\nkeep", "keep"},
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
