package embedded

import (
	"strings"
	"unicode/utf8"
)

// reasoningLive turns per-token OpenAI reasoning_content deltas into Live
// Output Thinking-card chunks. Contract matches Grok tagThoughtChunk
// (#7275 / #7202):
//
//   - first flush of a thought section is prefixed with "💭 "
//   - later physical lines use a 3-space indent ("\n   ")
//   - mid-line safety-flush remnants are not re-tagged
//   - whitespace-only flushes are dropped so classifyLine does not close
//     the thinking card
//
// onToken stays content-only (MAGI salvage). Callers must pass OnOutput
// directly — not emitOutput — so LineCoalescer can keep an open line.
type reasoningLive struct {
	buf         strings.Builder
	out         func(string)
	started     bool
	lastEndedNL bool
}

func newReasoningLive(out func(string)) *reasoningLive {
	if out == nil {
		return nil
	}
	return &reasoningLive{out: out}
}

// Append adds a reasoning_content delta. The first delta of a section
// writes the 💭 opener; newlines become 3-space continuations.
func (b *reasoningLive) Append(delta string) {
	if b == nil || b.out == nil || delta == "" {
		return
	}
	if !b.started {
		b.started = true
		b.buf.WriteString("💭 ")
	}
	b.buf.WriteString(indentReasoningDelta(delta))
	for {
		s := b.buf.String()
		nlIdx := strings.IndexByte(s, '\n')
		if nlIdx < 0 {
			break
		}
		chunk := s[:nlIdx+1]
		b.buf.Reset()
		b.buf.WriteString(s[nlIdx+1:])
		b.emitThought(chunk)
	}
	if b.buf.Len() >= 120 {
		b.flushSafety()
	}
}

// Flush emits any remainder (section end / stream end).
func (b *reasoningLive) Flush() {
	if b == nil || b.out == nil || b.buf.Len() == 0 {
		return
	}
	b.emitThought(b.buf.String())
	b.buf.Reset()
}

// Close flushes remainder and, if a thought section was opened, ends it
// with a newline so a later emitOutput (tool header / answer text) is not
// glued onto the last thought line by LineCoalescer. The newline is
// appended to the remainder when possible so we don't emit a blank line
// that classifyLine would treat as text.
func (b *reasoningLive) Close() {
	if b == nil {
		return
	}
	if b.buf.Len() > 0 && !strings.HasSuffix(b.buf.String(), "\n") {
		b.buf.WriteByte('\n')
	}
	b.Flush()
	if b.started && !b.lastEndedNL {
		b.out("\n")
		b.lastEndedNL = true
	}
}

func (b *reasoningLive) emitThought(chunk string) {
	if b == nil || b.out == nil || chunk == "" {
		return
	}
	tagged := tagReasoningChunk(chunk)
	if strings.TrimSpace(tagged) == "" {
		return
	}
	b.out(tagged)
	b.lastEndedNL = strings.HasSuffix(tagged, "\n")
}

// flushSafety emits without a newline, preferring a word boundary so BPE
// token cuts do not become mid-word line breaks. CJK punctuation is a
// fallback split (same heuristic as grokLiveBuf).
func (b *reasoningLive) flushSafety() {
	if b == nil || b.out == nil || b.buf.Len() == 0 {
		return
	}
	s := b.buf.String()

	if sp := strings.LastIndexByte(s, ' '); sp >= len(s)/2 {
		b.emitThought(s[:sp+1])
		b.buf.Reset()
		b.buf.WriteString(s[sp+1:])
		return
	}

	for i := len(s); i > len(s)/2; {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if r == utf8.RuneError || size == 0 {
			break
		}
		i -= size
		switch r {
		case '。', '！', '？', '，', '、', '；', '：', '.', '!', '?':
			end := i + size
			b.emitThought(s[:end])
			b.buf.Reset()
			b.buf.WriteString(s[end:])
			return
		}
	}

	b.emitThought(s)
	b.buf.Reset()
}

// indentReasoningDelta turns thought newlines into 3-space continuations.
// Empty line bodies are omitted; the indent itself is kept so classifyLine
// does not treat a paragraph break as text and close the thinking card.
func indentReasoningDelta(data string) string {
	if data == "" || !strings.Contains(data, "\n") {
		return data
	}
	var b strings.Builder
	for i, line := range strings.Split(data, "\n") {
		if i > 0 {
			b.WriteString("\n   ")
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// tagReasoningChunk keeps a flushed thought chunk inside the thinking
// block without injecting a 💭 on every safety flush. Only the section
// opener should carry the marker; a new physical line without one is
// indented (3 spaces).
func tagReasoningChunk(s string) string {
	if s == "" {
		return s
	}
	i := 0
	for i < len(s) && (s[i] == '\n' || s[i] == '\r') {
		i++
	}
	prefix, rest := s[:i], s[i:]
	if rest == "" {
		return s
	}
	trimmed := strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(trimmed, "💭") {
		return s
	}
	if strings.HasPrefix(rest, "   ") {
		return s
	}
	if prefix != "" {
		return prefix + "   " + rest
	}
	return s
}
