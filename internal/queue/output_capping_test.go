package queue

import (
	"fmt"
	"strings"
	"testing"
)

func TestCapOutputLinesHeadTail_NoTrim(t *testing.T) {
	lines := []string{"hello", "world"}
	got, bytes := capOutputLinesHeadTail(lines)
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	if got[0] != "hello" || got[1] != "world" {
		t.Fatalf("unexpected content: %v", got)
	}
	_ = bytes
}

func TestCapOutputLinesHeadTail_Trim(t *testing.T) {
	// Generate enough lines to exceed the 20 MB budget.
	// Each line is ~30 KB, 1000 lines = ~30 MB.
	var lines []string
	for i := 0; i < 1000; i++ {
		lines = append(lines, strings.Repeat(fmt.Sprintf("line%d ", i), 6000)) // ~30 KB
	}

	got, totalBytes := capOutputLinesHeadTail(lines)

	if totalBytes > maxOutputLinesBytes {
		t.Fatalf("trimmed output exceeds budget: %d > %d", totalBytes, maxOutputLinesBytes)
	}

	// Should contain the truncation marker.
	foundMarker := false
	for _, l := range got {
		if strings.Contains(l, "[output truncated") {
			foundMarker = true
			break
		}
	}
	if !foundMarker {
		t.Fatal("expected truncation marker in output")
	}

	// First line should be from the head (line0...).
	if !strings.Contains(got[0], "line0") {
		t.Fatalf("expected head to start with first lines, got: %q", got[0][:min(50, len(got[0]))])
	}

	// Last line should be from the tail (line999 or close).
	last := got[len(got)-1]
	if !strings.Contains(last, "line999") && !strings.Contains(last, "line998") {
		t.Fatalf("expected tail to end with last lines, got: %q", last[:min(50, len(last))])
	}
}

func TestCapOutputLinesHeadTail_HeadTailDisjoint(t *testing.T) {
	// Ensure head and tail don't overlap.
	// Total: 1000 lines * ~30 KB = ~30 MB > 20 MB budget.
	var lines []string
	for i := 0; i < 1000; i++ {
		lines = append(lines, strings.Repeat(fmt.Sprintf("L%d ", i), 6000)) // ~30 KB
	}

	got, _ := capOutputLinesHeadTail(lines)

	// Find the marker index.
	markerIdx := -1
	for i, l := range got {
		if strings.Contains(l, "[output truncated") {
			markerIdx = i
			break
		}
	}
	if markerIdx == -1 {
		t.Fatal("truncation marker not found")
	}

	// Head lines (before marker) should have smaller line numbers than tail lines (after marker).
	headLast := got[markerIdx-1]
	tailFirst := got[markerIdx+1]

	// Extract line number from head.
	headNum := extractLineNum(headLast)
	tailNum := extractLineNum(tailFirst)

	if headNum >= tailNum {
		t.Fatalf("head line number (%d) >= tail line number (%d) — overlap!", headNum, tailNum)
	}
}

func extractLineNum(s string) int {
	// Find "L" followed by digits.
	idx := strings.Index(s, "L")
	if idx == -1 {
		return -1
	}
	num := 0
	for i := idx + 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		num = num*10 + int(s[i]-'0')
	}
	return num
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
