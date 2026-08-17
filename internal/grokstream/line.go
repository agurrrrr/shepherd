package grokstream

import (
	"bufio"
	"io"
)

// MaxLineBytes is how much of each NDJSON line we keep for parsing.
//
// Grok 1.0.3 ACP tool_call_update lines can exceed 1MB (observed 1.36MB).
// A bufio.Scanner with a 1MB cap returns ErrTooLong and stops the reader,
// which fills the 64KB stdout pipe and leaves grok blocked in pipe_write.
// Thought / message / tool_call-start lines are small; 256KB is enough to
// parse them. Longer lines are truncated here and the rest is discarded
// up to the next newline so the reader stays alive.
const MaxLineBytes = 256 * 1024

// ReadCappedLine reads one newline-terminated line from r.
// At most maxKeep bytes of content are returned; the remainder of an
// oversized line is consumed and discarded. truncated is true when
// content was dropped. err is io.EOF only when no bytes remain.
func ReadCappedLine(r *bufio.Reader, maxKeep int) (line string, truncated bool, err error) {
	if maxKeep < 1 {
		maxKeep = MaxLineBytes
	}
	var buf []byte
	for {
		fragment, readErr := r.ReadSlice('\n')
		content := fragment
		if readErr == nil || readErr == io.EOF {
			content = dropCRLF(fragment)
		}
		if len(content) > 0 {
			switch {
			case len(buf) >= maxKeep:
				truncated = true
			case len(buf)+len(content) > maxKeep:
				buf = append(buf, content[:maxKeep-len(buf)]...)
				truncated = true
			default:
				buf = append(buf, content...)
			}
		}
		switch readErr {
		case nil:
			return string(buf), truncated, nil
		case io.EOF:
			if len(buf) == 0 && !truncated {
				return "", false, io.EOF
			}
			return string(buf), truncated, nil
		case bufio.ErrBufferFull:
			continue
		default:
			return "", truncated, readErr
		}
	}
}

func dropCRLF(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
		if n = len(b); n > 0 && b[n-1] == '\r' {
			b = b[:n-1]
		}
	}
	return b
}
