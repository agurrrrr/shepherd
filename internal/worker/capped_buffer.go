package worker

import (
	"sync"
)

// CappedBuffer is a thread-safe byte buffer that retains only the tail of
// written data up to maxBytes. Once the limit is exceeded, older data is
// discarded and a truncation marker is inserted once. It is designed to
// replace unbounded strings.Builder instances that accumulate CLI output
// during long-running tasks, preventing OOM kills.
//
// The buffer is safe for concurrent use.
type CappedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	maxBytes  int
	truncated bool
}

// truncationMarker is inserted once when the buffer first overflows.
var truncationMarker = []byte("\n...[output truncated — showing tail only]...\n")

// NewCappedBuffer creates a new CappedBuffer with the given byte limit.
// The limit must be positive; values ≤ 0 are treated as 1 to avoid
// degenerate cases.
func NewCappedBuffer(maxBytes int) *CappedBuffer {
	if maxBytes < 1 {
		maxBytes = 1
	}
	return &CappedBuffer{
		buf:      make([]byte, 0, maxBytes),
		maxBytes: maxBytes,
	}
}

// Write appends data to the buffer. If adding the data would exceed maxBytes,
// the oldest data is discarded to make room. A truncation marker is inserted
// once when the first overflow occurs. Returns the number of bytes written
// from p (always len(p)).
func (b *CappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Fast path: still within budget.
	if len(b.buf)+len(p) <= b.maxBytes {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}

	// Overflow path.
	if !b.truncated {
		// Append new data to existing buffer first.
		b.buf = append(b.buf, p...)

		// We want: marker + tail of current buf ≤ maxBytes.
		// Keep as much of the tail as possible after reserving space for marker.
		keep := b.maxBytes - len(truncationMarker)
		if keep < 0 {
			keep = 0
		}
		if keep > len(b.buf) {
			keep = len(b.buf)
		}

		// Build: marker + tail.
		newBuf := make([]byte, 0, b.maxBytes)
		newBuf = append(newBuf, truncationMarker...)
		newBuf = append(newBuf, b.buf[len(b.buf)-keep:]...)
		b.buf = newBuf
		b.truncated = true

		// Final safety trim (marker could push us slightly over if maxBytes
		// is very small).
		if len(b.buf) > b.maxBytes {
			b.buf = b.buf[len(b.buf)-b.maxBytes:]
		}
	} else {
		// Already truncated: just append and trim from the front.
		b.buf = append(b.buf, p...)
		if len(b.buf) > b.maxBytes {
			b.buf = b.buf[len(b.buf)-b.maxBytes:]
		}
	}

	return len(p), nil
}

// WriteString is a convenience wrapper around Write for string input.
func (b *CappedBuffer) WriteString(s string) (int, error) {
	return b.Write([]byte(s))
}

// String returns the buffered content as a string.
func (b *CappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// Len returns the current length of the buffered data in bytes.
func (b *CappedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}

// Reset resets the buffer to empty and clears the truncated flag.
func (b *CappedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = b.buf[:0]
	b.truncated = false
}
