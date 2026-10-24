package worker

import (
	"strings"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/spf13/viper"
)

// withGrokWaitConfig pins grok_background_wait / task_timeout for one test and
// restores whatever was there before. viper.Set's override layer survives across
// tests otherwise, so a leaked "off" would flip later grok arg expectations.
func withGrokWaitConfig(t *testing.T, wait, taskTimeout string) {
	t.Helper()
	prevWait := viper.Get("grok_background_wait")
	prevTimeout := viper.Get("task_timeout")
	t.Cleanup(func() {
		viper.Set("grok_background_wait", prevWait)
		viper.Set("task_timeout", prevTimeout)
	})
	viper.Set("grok_background_wait", wait)
	viper.Set("task_timeout", taskTimeout)
}

// grokBackgroundWaitArgs (#619): grok's headless default wait is 10 minutes,
// after which it stops the background work it started and exits 1. The worker
// must always pass an explicit bound from config.
func TestGrokBackgroundWaitArgs(t *testing.T) {
	tests := []struct {
		name        string
		wait        string
		taskTimeout string
		want        []string
	}{
		// empty → default 60m → 3600s.
		{name: "empty uses 60m default", wait: "", taskTimeout: "4h",
			want: []string{"--background-wait-timeout", "3600"}},

		// explicit durations.
		{name: "90s", wait: "90s", taskTimeout: "4h",
			want: []string{"--background-wait-timeout", "90"}},
		{name: "30m", wait: "30m", taskTimeout: "4h",
			want: []string{"--background-wait-timeout", "1800"}},

		// off-style → exit as soon as the turn ends.
		{name: "off", wait: "off", taskTimeout: "4h",
			want: []string{"--no-wait-for-background"}},
		{name: "zero", wait: "0", taskTimeout: "4h",
			want: []string{"--no-wait-for-background"}},

		// unlimited-style (#625) → wait just under task_timeout (4h - 5m =
		// 3h55m = 14100s), NOT --no-wait-for-background.
		{name: "unlimited", wait: "unlimited", taskTimeout: "4h",
			want: []string{"--background-wait-timeout", "14100"}},
		// unlimited + unlimited task_timeout → the large fixed bound (24h =
		// 86400s). The flag must never be omitted (that would drop back to
		// grok's 600s default).
		{name: "unlimited with unlimited task_timeout", wait: "unlimited", taskTimeout: "off",
			want: []string{"--background-wait-timeout", "86400"}},

		// capped below task_timeout with 5m headroom.
		{name: "capped under task_timeout", wait: "90m", taskTimeout: "1h",
			want: []string{"--background-wait-timeout", "3300"}},
		{name: "default capped too", wait: "", taskTimeout: "30m",
			want: []string{"--background-wait-timeout", "1500"}},

		// typo → default, never the CLI's built-in 600s.
		{name: "typo falls back to default", wait: "60mintues", taskTimeout: "4h",
			want: []string{"--background-wait-timeout", "3600"}},

		// sub-second config must not become "0".
		{name: "sub-second floors to 1", wait: "500ms", taskTimeout: "4h",
			want: []string{"--background-wait-timeout", "1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withGrokWaitConfig(t, tt.wait, tt.taskTimeout)
			got := grokBackgroundWaitArgs()
			if len(got) != len(tt.want) {
				t.Fatalf("grokBackgroundWaitArgs() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("grokBackgroundWaitArgs() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestGrokBackgroundWaitArgsSubSecondFloor guards the seconds conversion: a
// floor-derived wait must never emit "0" (grok would treat 0s as an instant
// timeout with exit 1).
func TestGrokBackgroundWaitArgsSubSecondFloor(t *testing.T) {
	withGrokWaitConfig(t, "5m", "1m")
	got := grokBackgroundWaitArgs()
	if len(got) != 2 || got[0] != "--background-wait-timeout" || got[1] != "60" {
		t.Fatalf("got %v, want [--background-wait-timeout 60]", got)
	}
	if config.GetGrokBackgroundWait() < time.Second {
		t.Fatalf("config wait must stay >= 1s, got %v", config.GetGrokBackgroundWait())
	}
}

// TestGrokBackgroundWaitNote: the prompt note must state the actual configured
// limit, and the off-mode note must not claim a timeout that does not exist.
func TestGrokBackgroundWaitNote(t *testing.T) {
	withGrokWaitConfig(t, "90s", "4h")
	note := grokBackgroundWaitNote()
	// Duration.String() renders 90s as "1m30s" — the note must carry the actual
	// configured value in some form, not a hard-coded default.
	if !strings.Contains(note, "1m30s") {
		t.Fatalf("note should carry the configured value, got %q", note)
	}
	if !strings.Contains(note, "[Execution limit]") {
		t.Fatalf("note should be tagged, got %q", note)
	}

	withGrokWaitConfig(t, "off", "4h")
	note = grokBackgroundWaitNote()
	if !strings.Contains(note, "--no-wait-for-background") {
		t.Fatalf("off-mode note should mention --no-wait-for-background, got %q", note)
	}
	if strings.Contains(note, "waits at most") {
		t.Fatalf("off-mode note should not claim a wait limit, got %q", note)
	}
}

// TestGrokBackgroundWaitRanOut: classify grok's exit-1 background-wait notice
// (stdout streaming error event and/or stderr text) and extract the seconds.
func TestGrokBackgroundWaitRanOut(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantOK   bool
		wantSecs int
	}{
		{
			name:     "issue verbatim (exit notice, 600s)",
			input:    "❌ the wait for background work ran out after 600 s and exit stopped 1 background task (sleep 3600). Pass --background-wait-timeout <secs> to wait longer, or --no-wait-for-background to exit as soon as the turn ends",
			wantOK:   true,
			wantSecs: 600,
		},
		{
			name:     "per-task stopped notice",
			input:    "task #2 (cargo build --release) was stopped after 3600 s: the wait for background work ran out.",
			wantOK:   true,
			wantSecs: 3600,
		},
		{
			name:     "custom timeout value",
			input:    "the wait for background work ran out after 90 s and exit stopped 2 background tasks (...)",
			wantOK:   true,
			wantSecs: 90,
		},
		{
			name:     "notice without seconds still classifies",
			input:    "the wait for background work ran out and exit stopped 1 background task",
			wantOK:   true,
			wantSecs: 0,
		},
		{
			name:     "unrelated after-Ns line does not leak a wrong number",
			input:    "downloaded the patch after 12 s\nthe wait for background work ran out and exit stopped 1 background task",
			wantOK:   true,
			wantSecs: 0,
		},
		{
			name:   "unrelated error is not misclassified",
			input:  "exit status 1\nsome model error happened",
			wantOK: false,
		},
		{
			name:   "rate limit stays separate",
			input:  "exit status 1\n429 too many requests",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secs, ok := grokBackgroundWaitRanOut(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (input: %q)", ok, tt.wantOK, tt.input)
			}
			if ok && secs != tt.wantSecs {
				t.Fatalf("secs = %d, want %d", secs, tt.wantSecs)
			}
		})
	}
}

// TestGrokBackgroundWaitErrorNotRateLimit: the queue retries when the error
// string matches IsRateLimitError's substrings. The classified message must not
// contain any of them (that misclassification re-ran the whole task before).
func TestGrokBackgroundWaitErrorNotRateLimit(t *testing.T) {
	for _, secs := range []int{600, 0} {
		msg := strings.ToLower(grokBackgroundWaitError(secs).Error())
		for _, bad := range []string{"rate limit", "session limit", "429", "too many requests", "limit exceeded"} {
			if strings.Contains(msg, bad) {
				t.Fatalf("secs=%d: message contains rate-limit trigger %q: %s", secs, bad, msg)
			}
		}
		if !strings.Contains(msg, "grok background wait timeout") {
			t.Fatalf("secs=%d: message should say what happened: %s", secs, msg)
		}
	}
	if !strings.Contains(grokBackgroundWaitError(600).Error(), "(600s)") {
		t.Fatal("seconds should be reported when known")
	}
}

// TestGrokBackgroundWaitRanOutACPShape: the notice reaches fullOutput through
// compactGrokEvent's error events; make sure that shape is detected too.
func TestGrokBackgroundWaitRanOutACPShape(t *testing.T) {
	out := `{"type":"text","data":"build still running"}
{"type":"error","message":"the wait for background work ran out after 600 s and exit stopped 1 background task (make -j8)"}
`
	secs, ok := grokBackgroundWaitRanOut(out + " exit status 1")
	if !ok || secs != 600 {
		t.Fatalf("ok=%v secs=%d, want true/600", ok, secs)
	}
}
