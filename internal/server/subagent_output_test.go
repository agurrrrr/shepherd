package server

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestWrapSubagentOnOutputPrefixesEveryPhysicalLine verifies the #7564
// contract: multi-line chunks and incomplete fragments all become
// "[SUB:name] …" lines (every physical line, not only the first).
func TestWrapSubagentOnOutputPrefixesEveryPhysicalLine(t *testing.T) {
	var mu sync.Mutex
	var got []string
	out := func(s string) { got = append(got, s) }

	wrapped, flush := wrapSubagentOnOutput("logo-analysis", out, &mu)
	// Multi-line chunk (emitOutput-style): all physical lines need prefix.
	wrapped("🔧 glob → **/*logo*\n  No files found\n💭 next step\n")
	// Incomplete fragment then completion.
	wrapped("partial")
	wrapped(" answer\n")
	flush()

	if len(got) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(got), strings.Join(got, ""))
	}
	re := regexp.MustCompile(`^\[SUB:logo-analysis\] `)
	for i, line := range got {
		if !re.MatchString(line) {
			t.Errorf("line %d missing prefix: %q", i, line)
		}
	}
	wantBodies := []string{
		"🔧 glob → **/*logo*\n",
		"  No files found\n",
		"💭 next step\n",
		"partial answer\n",
	}
	for i, want := range wantBodies {
		gotBody := strings.TrimPrefix(got[i], "[SUB:logo-analysis] ")
		if gotBody != want {
			t.Errorf("line %d body = %q, want %q", i, gotBody, want)
		}
	}
}

// TestWrapSubagentOnOutputConcurrentAgentsNoCrossGlue ensures two agents
// streaming incomplete chunks concurrently never produce a single line that
// mixes both names (root failure mode from task #7560 bare streams).
func TestWrapSubagentOnOutputConcurrentAgentsNoCrossGlue(t *testing.T) {
	var mu sync.Mutex
	var gotMu sync.Mutex
	var got []string
	out := func(s string) {
		gotMu.Lock()
		got = append(got, s)
		gotMu.Unlock()
	}

	a, flushA := wrapSubagentOnOutput("logo-analysis", out, &mu)
	b, flushB := wrapSubagentOnOutput("redirect-analysis", out, &mu)

	const n = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			// Incomplete then complete — forces open-line buffer per agent.
			a(fmt.Sprintf("A%d", i))
			a("-chunk\n")
		}
		flushA()
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			b(fmt.Sprintf("B%d", i))
			b("-chunk\n")
		}
		flushB()
	}()
	wg.Wait()

	if len(got) != 2*n {
		t.Fatalf("got %d lines, want %d", len(got), 2*n)
	}

	reA := regexp.MustCompile(`^\[SUB:logo-analysis\] A\d+-chunk\n$`)
	reB := regexp.MustCompile(`^\[SUB:redirect-analysis\] B\d+-chunk\n$`)
	// A single stored line must never contain both agent prefixes or both
	// agent bodies (the #7209-style cross-glue).
	mixed := regexp.MustCompile(`\[SUB:logo-analysis\].*\[SUB:redirect-analysis\]|\[SUB:redirect-analysis\].*\[SUB:logo-analysis\]`)
	crossBody := regexp.MustCompile(`A\d+-chunk.*B\d+-chunk|B\d+-chunk.*A\d+-chunk`)

	var countA, countB int
	for i, line := range got {
		if mixed.MatchString(line) {
			t.Fatalf("line %d mixes both prefixes: %q", i, line)
		}
		if crossBody.MatchString(line) {
			t.Fatalf("line %d mixes both agent bodies: %q", i, line)
		}
		switch {
		case reA.MatchString(line):
			countA++
		case reB.MatchString(line):
			countB++
		default:
			t.Fatalf("line %d unexpected form: %q", i, line)
		}
	}
	if countA != n || countB != n {
		t.Fatalf("countA=%d countB=%d, want %d each", countA, countB, n)
	}
}

// TestWrapSubagentOnOutputFlushEmitsOpenLine covers residual buffer on exit.
func TestWrapSubagentOnOutputFlushEmitsOpenLine(t *testing.T) {
	var mu sync.Mutex
	var got []string
	wrapped, flush := wrapSubagentOnOutput("solo", func(s string) {
		got = append(got, s)
	}, &mu)

	wrapped("no trailing newline yet")
	if len(got) != 0 {
		t.Fatalf("incomplete chunk should buffer, got %v", got)
	}
	flush()
	if len(got) != 1 {
		t.Fatalf("flush want 1 line, got %v", got)
	}
	if got[0] != "[SUB:solo] no trailing newline yet" {
		t.Fatalf("got %q", got[0])
	}
}

// TestWrapSubagentOnOutputNilIsNoop ensures nil parent sink is safe.
func TestWrapSubagentOnOutputNilIsNoop(t *testing.T) {
	wrapped, flush := wrapSubagentOnOutput("x", nil, nil)
	wrapped("anything\n")
	flush() // must not panic
}
