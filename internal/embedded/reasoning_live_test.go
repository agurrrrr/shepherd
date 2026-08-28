package embedded

import (
	"strings"
	"testing"
)

func TestReasoningLive_BuffersTokensAndTagsOnce(t *testing.T) {
	var out []string
	buf := newReasoningLive(func(s string) { out = append(out, s) })

	for _, tok := range []string{"The", " user", " is", " attaching", " screens", "hots"} {
		buf.Append(tok)
	}
	buf.Close()

	joined := strings.Join(out, "")
	if n := strings.Count(joined, "💭"); n != 1 {
		t.Errorf("expected a single 💭, got %d in %#v", n, out)
	}
	if !strings.HasPrefix(joined, "💭 ") {
		t.Errorf("missing thought marker: %q", joined)
	}
	if !strings.Contains(joined, "The user is attaching screenshots") {
		t.Errorf("tokens did not coalesce: %q", joined)
	}
	if strings.Contains(joined, "screens\n") || strings.Contains(joined, "screens hots") {
		t.Errorf("mid-word split leaked: %q", joined)
	}
}

func TestReasoningLive_IndentContinuations(t *testing.T) {
	var out []string
	buf := newReasoningLive(func(s string) { out = append(out, s) })
	buf.Append("first line\nsecond line\nthird")
	buf.Close()

	joined := strings.Join(out, "")
	if !strings.Contains(joined, "💭 first line\n") {
		t.Errorf("first line should carry 💭: %q", joined)
	}
	if !strings.Contains(joined, "\n   second line\n") {
		t.Errorf("second line should be 3-space continuation: %q", joined)
	}
	if !strings.Contains(joined, "\n   third") {
		t.Errorf("third line should be 3-space continuation: %q", joined)
	}
	if n := strings.Count(joined, "💭"); n != 1 {
		t.Errorf("expected a single 💭, got %d in %q", n, joined)
	}
}

func TestReasoningLive_NilIsNoop(t *testing.T) {
	var buf *reasoningLive
	buf.Append("hello")
	buf.Flush()
	buf.Close()
}

func TestReasoningLive_CloseAddsTrailingNewline(t *testing.T) {
	var out []string
	buf := newReasoningLive(func(s string) { out = append(out, s) })
	buf.Append("planning")
	buf.Close()

	joined := strings.Join(out, "")
	if !strings.HasSuffix(joined, "\n") {
		t.Errorf("Close should end the thinking block with newline, got %q", joined)
	}
}

func TestReasoningLive_CloseDoesNotDoubleNewline(t *testing.T) {
	var out []string
	buf := newReasoningLive(func(s string) { out = append(out, s) })
	buf.Append("planning\n")
	buf.Close()

	joined := strings.Join(out, "")
	if strings.HasSuffix(joined, "\n\n") {
		t.Errorf("Close should not add a second newline after a flushed line, got %q", joined)
	}
}

func TestReasoningLive_SafetyFlushDoesNotRetag(t *testing.T) {
	var out []string
	buf := newReasoningLive(func(s string) { out = append(out, s) })

	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("word ")
	}
	buf.Append(b.String())
	buf.Append("more")
	buf.Close()

	joined := strings.Join(out, "")
	if n := strings.Count(joined, "💭"); n != 1 {
		t.Errorf("safety flush must not re-tag 💭, got %d in %#v", n, out)
	}
	if strings.Contains(joined, " 💭 ") {
		t.Errorf("mid-line 💭 re-tag leaked: %q", joined)
	}
}

func TestTagReasoningChunk(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"💭 already tagged", "💭 already tagged"},
		{"plain continuation", "plain continuation"},
		{"\n\nplain after blank", "\n\n   plain after blank"},
		{"   indented cont", "   indented cont"},
		{"\n💭 has marker", "\n💭 has marker"},
	}
	for _, tc := range cases {
		got := tagReasoningChunk(tc.in)
		if got != tc.want {
			t.Errorf("tagReasoningChunk(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIndentReasoningDelta(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"no newline", "no newline"},
		{"hello\nworld", "hello\n   world"},
		{"hello\n\nworld", "hello\n   \n   world"},
		{"\nhello", "\n   hello"},
	}
	for _, tc := range cases {
		got := indentReasoningDelta(tc.in)
		if got != tc.want {
			t.Errorf("indentReasoningDelta(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReasoningLive_DropsWhitespaceOnlyFlush(t *testing.T) {
	var out []string
	buf := newReasoningLive(func(s string) { out = append(out, s) })
	buf.Append("The user prompt")
	buf.Append("\n")
	buf.Append("is being restated")
	buf.Close()

	joined := strings.Join(out, "")
	if strings.Contains(joined, "\n\n") {
		t.Errorf("blank lines leaked into thought stream: %#v", out)
	}
	for _, chunk := range out {
		if strings.TrimSpace(chunk) == "" {
			t.Errorf("whitespace-only chunk leaked: %#v", out)
		}
	}
}
