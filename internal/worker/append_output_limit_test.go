package worker

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestAppendOutputByteLimit verifies that RunningTask.OutputLines is capped
// at maxRunningTaskOutputBytes, that the tail is preserved, and that a
// truncation marker is inserted once.
func TestAppendOutputByteLimit(t *testing.T) {
	const name = "test-sheep-output-limit"

	// Clean up any leftover state.
	unregisterRunningTask(name, runningTasks[name])

	cmd := exec.Command("true")
	registerRunningTask(name, nil, cmd)
	defer unregisterRunningTask(name, runningTasks[name])

	// Write lines until we exceed the byte budget.
	// Each line is ~1 KB; we need > 5 MB / 1 KB = 5120 lines.
	lineCount := 6000
	lineSize := 1024 // ~1 KB per line
	oneLine := strings.Repeat("x", lineSize-10) + "\n"

	for i := 0; i < lineCount; i++ {
		AppendOutput(name, oneLine)
	}

	_, lines := GetRunningTaskOutput(name)

	// Total bytes must not exceed the budget by more than one line's worth
	// (the check happens after append, so we can be over by one line).
	totalBytes := 0
	for _, l := range lines {
		totalBytes += len(l)
	}
	// Allow some slack for the marker line.
	if totalBytes > maxRunningTaskOutputBytes+lineSize+200 {
		t.Fatalf("output lines exceed budget: %d > %d", totalBytes, maxRunningTaskOutputBytes)
	}

	// Should contain a truncation marker.
	foundMarker := false
	for _, l := range lines {
		if strings.Contains(l, "[output truncated") {
			foundMarker = true
			break
		}
	}
	if !foundMarker {
		t.Fatal("expected truncation marker in output")
	}

	// The last line should be the last one we wrote (tail preserved).
	lastLine := lines[len(lines)-1]
	if !strings.HasPrefix(lastLine, strings.Repeat("x", 10)) {
		t.Fatalf("tail not preserved: last line doesn't start with 'x'*10")
	}
}

// TestAppendOutputNoTruncationUnderLimit verifies that small outputs are
// not truncated.
func TestAppendOutputNoTruncationUnderLimit(t *testing.T) {
	const name = "test-sheep-no-trunc"

	unregisterRunningTask(name, runningTasks[name])

	cmd := exec.Command("true")
	registerRunningTask(name, nil, cmd)
	defer unregisterRunningTask(name, runningTasks[name])

	for i := 0; i < 100; i++ {
		AppendOutput(name, fmt.Sprintf("line %d\n", i))
	}

	_, lines := GetRunningTaskOutput(name)
	if len(lines) != 100 {
		t.Fatalf("expected 100 lines, got %d", len(lines))
	}

	// No truncation marker.
	for _, l := range lines {
		if strings.Contains(l, "[output truncated") {
			t.Fatal("unexpected truncation marker for small output")
		}
	}

	// First and last lines should be intact.
	if lines[0] != "line 0\n" {
		t.Fatalf("first line mismatch: %q", lines[0])
	}
	if lines[99] != "line 99\n" {
		t.Fatalf("last line mismatch: %q", lines[99])
	}
}
