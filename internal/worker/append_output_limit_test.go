package worker

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
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

func TestMergeRunningOutput_PrefersLiveWhenTaskMatches(t *testing.T) {
	const name = "test-sheep-merge-output"
	unregisterRunningTask(name, runningTasks[name])

	if !ClaimDispatch(name, 77) {
		t.Fatal("claim")
	}
	token := registerRunningTask(name, nil, exec.Command("true"))
	defer unregisterRunningTask(name, token)

	AppendOutput(name, "live line\n")

	got := MergeRunningOutput(name, 77, []string{"db"})
	if len(got) != 1 || got[0] != "live line\n" {
		t.Fatalf("want live lines, got %#v", got)
	}

	// Different task id on the same sheep must not leak.
	if mixed := MergeRunningOutput(name, 78, []string{"db"}); len(mixed) != 1 || mixed[0] != "db" {
		t.Fatalf("foreign task should keep db output, got %#v", mixed)
	}

	if empty := MergeRunningOutput("no-such-sheep", 77, []string{"db"}); len(empty) != 1 || empty[0] != "db" {
		t.Fatalf("missing sheep should keep db output, got %#v", empty)
	}
}

// TestAppendOutputTrimIntervals verifies that once the buffer crosses the
// byte budget, trimming runs at multi-hundred-KB intervals rather than on
// every appended line, that exactly one truncation marker remains, and that
// the measured 100B × 60,000 line case finishes well under a second.
func TestAppendOutputTrimIntervals(t *testing.T) {
	const name = "test-sheep-trim-interval"
	unregisterRunningTask(name, runningTasks[name])

	token := registerRunningTask(name, nil, exec.Command("true"))
	defer unregisterRunningTask(name, token)

	runningOutputTrimCount.Store(0)

	line := strings.Repeat("z", 99) + "\n" // 100 bytes
	start := time.Now()
	for i := 0; i < 60000; i++ {
		AppendOutput(name, line)
	}
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("60000 append took %v, want < 1s", elapsed)
	}

	trims := runningOutputTrimCount.Load()
	if trims != 1 {
		t.Fatalf("expected exactly 1 trim after crossing the budget, got %d", trims)
	}

	_, lines := GetRunningTaskOutput(name)

	markers := 0
	totalBytes := 0
	for _, l := range lines {
		totalBytes += len(l)
		if strings.Contains(l, "[output truncated") {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("expected exactly 1 truncation marker, got %d", markers)
	}
	if totalBytes > maxRunningTaskOutputBytes {
		t.Fatalf("output exceeds budget: %d > %d", totalBytes, maxRunningTaskOutputBytes)
	}

	// Another 1000 lines (~100 KB) must not trigger another trim thanks to
	// the 80% target leaving ~1 MB of headroom.
	for i := 0; i < 1000; i++ {
		AppendOutput(name, line)
	}
	if got := runningOutputTrimCount.Load(); got != 1 {
		t.Fatalf("expected no further trim within headroom, got %d trims", got)
	}
}

// BenchmarkAppendOutput measures steady-state appends including periodic
// trimming at the 5 MB running-task budget.
func BenchmarkAppendOutput(b *testing.B) {
	const name = "bench-sheep-append-output"
	unregisterRunningTask(name, runningTasks[name])
	token := registerRunningTask(name, nil, exec.Command("true"))
	defer unregisterRunningTask(name, token)

	line := strings.Repeat("x", 99) + "\n"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AppendOutput(name, line)
	}
}
