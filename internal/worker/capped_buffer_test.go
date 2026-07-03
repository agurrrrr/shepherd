package worker

import (
	"strings"
	"testing"
)

func TestCappedBuffer_NoTruncation(t *testing.T) {
	b := NewCappedBuffer(1024)
	b.WriteString("hello ")
	b.WriteString("world")
	got := b.String()
	if got != "hello world" {
		t.Fatalf("expected 'hello world', got %q", got)
	}
	if b.Len() != 11 {
		t.Fatalf("expected Len 11, got %d", b.Len())
	}
}

func TestCappedBuffer_TruncationMarker(t *testing.T) {
	// Use a buffer large enough for the truncation marker (~53 bytes).
	b := NewCappedBuffer(200)
	// Write 100 bytes — within budget.
	b.WriteString(strings.Repeat("A", 100))
	// Write 100 more bytes — total 200, exactly at budget.
	b.WriteString(strings.Repeat("B", 100))
	// Write 50 more — overflow.
	b.WriteString(strings.Repeat("C", 50))

	got := b.String()
	if !strings.Contains(got, "[output truncated") {
		t.Fatalf("expected truncation marker, got: %q", got)
	}
	// Must end with the new data (C's).
	if !strings.HasSuffix(got, strings.Repeat("C", 50)) {
		t.Fatalf("expected tail to end with C's, got last 10: %q", got[len(got)-10:])
	}
	// Total must not exceed maxBytes.
	if len(got) > 200 {
		t.Fatalf("buffer exceeded maxBytes: %d > 200", len(got))
	}
}

func TestCappedBuffer_AlreadyTruncated(t *testing.T) {
	b := NewCappedBuffer(100)
	// Fill to trigger truncation.
	b.WriteString(strings.Repeat("0", 60)) // 60 bytes
	b.WriteString(strings.Repeat("1", 60)) // 120 → overflow → truncated

	got := b.String()
	if !strings.Contains(got, "[output truncated") {
		t.Fatalf("expected truncation marker on first overflow")
	}

	// Write more data — should stay within budget without re-adding marker.
	b.WriteString("XYZ")
	got = b.String()
	if strings.Count(got, "[output truncated") > 1 {
		t.Fatalf("truncation marker inserted more than once: %q", got)
	}
	if len(got) > 100 {
		t.Fatalf("buffer exceeded maxBytes after subsequent writes: %d > 100", len(got))
	}
}

func TestCappedBuffer_Reset(t *testing.T) {
	b := NewCappedBuffer(200)
	b.WriteString("hello")
	b.WriteString(strings.Repeat("X", 200)) // triggers truncation

	b.Reset()
	if b.Len() != 0 {
		t.Fatalf("expected Len 0 after Reset, got %d", b.Len())
	}
	if b.String() != "" {
		t.Fatalf("expected empty string after Reset, got %q", b.String())
	}

	// After reset, new writes should not have a truncation marker.
	b.WriteString("fresh")
	got := b.String()
	if strings.Contains(got, "[output truncated") {
		t.Fatalf("truncation marker present after Reset: %q", got)
	}
	if got != "fresh" {
		t.Fatalf("expected 'fresh', got %q", got)
	}
}

func TestCappedBuffer_ExactFit(t *testing.T) {
	b := NewCappedBuffer(100)
	b.WriteString(strings.Repeat("0", 100)) // exactly 100 bytes
	if b.String() != strings.Repeat("0", 100) {
		t.Fatalf("exact fit failed")
	}
	// One more byte triggers overflow.
	b.WriteString("X")
	got := b.String()
	if !strings.Contains(got, "[output truncated") {
		t.Fatalf("expected truncation at exact boundary + 1")
	}
	if len(got) > 100 {
		t.Fatalf("buffer exceeded maxBytes: %d > 100", len(got))
	}
}

func TestCappedBuffer_ConcurrentAccess(t *testing.T) {
	b := NewCappedBuffer(500)
	done := make(chan struct{})

	// Writer 1
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			b.WriteString("writer1\n")
		}
	}()

	// Writer 2
	for i := 0; i < 100; i++ {
		b.WriteString("writer2\n")
	}

	<-done

	got := b.String()
	if len(got) > 500 {
		t.Fatalf("buffer exceeded maxBytes: %d > 500", len(got))
	}
	// Should contain data from both writers.
	if len(got) == 0 {
		t.Fatal("buffer is empty after concurrent writes")
	}
}
