package procutil

import (
	"bufio"
	"io"
)

// DefaultMaxLineBytes is the per-line cap for child-process stdout readers
// that used to run a bufio.Scanner with a 1MB limit.
const DefaultMaxLineBytes = 1024 * 1024

// ReadCappedLine reads one newline-terminated line from r.
// At most maxKeep bytes of content are returned; the remainder of an
// oversized line is consumed and discarded. truncated is true when
// content was dropped. err is io.EOF only when no bytes remain.
//
// Use this instead of bufio.Scanner on a child's stdout pipe. A Scanner
// returns ErrTooLong on the first line over its cap and stops reading:
// the pipe fills or every later event is lost, and the caller waits on a
// process whose output nobody reads (grok #8165, Claude image results).
func ReadCappedLine(r *bufio.Reader, maxKeep int) (line string, truncated bool, err error) {
	if maxKeep < 1 {
		maxKeep = DefaultMaxLineBytes
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
