package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

// TestGetBashTimeoutSeconds pins the read path the shell tool uses: the default
// is 10 minutes, a configured value wins, a value written by the CLI as a
// string still casts, and anything unusable falls back to the default instead of
// leaving a command with no cap at all.
func TestGetBashTimeoutSeconds(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	if got := GetBashTimeoutSeconds(); got != DefaultBashTimeoutSeconds {
		t.Errorf("unset: got %d, want default %d", got, DefaultBashTimeoutSeconds)
	}
	if DefaultBashTimeoutSeconds != 600 {
		t.Errorf("default bash_timeout = %d, want 600 (10 minutes)", DefaultBashTimeoutSeconds)
	}

	viper.Set("bash_timeout", 30)
	if got := GetBashTimeoutSeconds(); got != 30 {
		t.Errorf("configured: got %d, want 30", got)
	}

	// The CLI stores a number, but a hand-written config.yaml may hold "5m"-style
	// text or a quoted number; viper.GetInt covers the quoted-number case.
	viper.Set("bash_timeout", "90")
	if got := GetBashTimeoutSeconds(); got != 90 {
		t.Errorf("string value: got %d, want 90", got)
	}

	for _, bad := range []interface{}{0, -1, BashTimeoutHardCeilSeconds + 1, "not-a-number"} {
		viper.Set("bash_timeout", bad)
		if got := GetBashTimeoutSeconds(); got != DefaultBashTimeoutSeconds {
			t.Errorf("bad value %v: got %d, want default %d", bad, got, DefaultBashTimeoutSeconds)
		}
	}
}

// TestNormalizeBashTimeoutSeconds covers the validation both the config API and
// `shepherd config set` run before storing. Whole seconds and duration form must
// both land on the same stored number, and a typo must be an error rather than a
// silent fallback that leaves the operator believing they raised the cap.
func TestNormalizeBashTimeoutSeconds(t *testing.T) {
	valid := []struct {
		in   interface{}
		want int
	}{
		{30, 30},
		{int64(120), 120},
		{float64(600), 600},
		{"300", 300},
		{" 45 ", 45},
		{"5m", 300},
		{"1h", 3600},
		{"90s", 90},
		{BashTimeoutHardCeilSeconds, BashTimeoutHardCeilSeconds},
	}
	for _, c := range valid {
		got, err := NormalizeBashTimeoutSeconds(c.in)
		if err != nil {
			t.Errorf("NormalizeBashTimeoutSeconds(%v): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeBashTimeoutSeconds(%v) = %d, want %d", c.in, got, c.want)
		}
	}

	invalid := []interface{}{0, -5, "abc", "1Om", "", "   ", 2 * BashTimeoutHardCeilSeconds,
		"2h", []string{"300"}, nil}
	for _, in := range invalid {
		if got, err := NormalizeBashTimeoutSeconds(in); err == nil {
			t.Errorf("NormalizeBashTimeoutSeconds(%v) = %d, want an error", in, got)
		}
	}
}

// TestClampBashTimeoutSeconds guards the per-call `timeout` argument a model
// passes. A hallucinated huge value must be clamped to the ceiling, not trusted,
// or one tool call could park the agent turn for days.
func TestClampBashTimeoutSeconds(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	if got := ClampBashTimeoutSeconds(45); got != 45 {
		t.Errorf("in range: got %d, want 45", got)
	}
	if got := ClampBashTimeoutSeconds(BashTimeoutHardCeilSeconds + 1000); got != BashTimeoutHardCeilSeconds {
		t.Errorf("over ceiling: got %d, want ceiling %d", got, BashTimeoutHardCeilSeconds)
	}

	// Non-positive falls back to the configured default.
	if got := ClampBashTimeoutSeconds(0); got != DefaultBashTimeoutSeconds {
		t.Errorf("zero: got %d, want default %d", got, DefaultBashTimeoutSeconds)
	}
	viper.Set("bash_timeout", 20)
	if got := ClampBashTimeoutSeconds(-1); got != 20 {
		t.Errorf("negative with configured default: got %d, want 20", got)
	}
}

// TestGetToolDispatchTimeoutIsNeverShorterThanTheShellCap is the point of wiring
// the loop watchdog to bash_timeout. With the old fixed 5-minute watchdog, raising
// the shell timeout to 10 minutes would have made the agent loop kill a legitimate
// build first and report a generic tool error instead of the shell path's own
// readable timeout message.
func TestGetToolDispatchTimeoutIsNeverShorterThanTheShellCap(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	// Default: 10 minutes plus the margin beats the 5-minute backstop.
	want := time.Duration(DefaultBashTimeoutSeconds+ShellDispatchMarginSeconds) * time.Second
	if got := GetToolDispatchTimeout(); got != want {
		t.Errorf("default: watchdog = %s, want %s", got, want)
	}

	// A short bash_timeout keeps the backstop, so tools with no timeout of their
	// own (external MCP servers, browser calls) stay bounded as before.
	viper.Set("bash_timeout", 30)
	wantBackstop := time.Duration(ToolDispatchBackstopSeconds) * time.Second
	if got := GetToolDispatchTimeout(); got != wantBackstop {
		t.Errorf("short bash_timeout: watchdog = %s, want backstop %s", got, wantBackstop)
	}

	// A per-call request above the configured default must stretch the watchdog
	// too — execBash honours that longer budget, so the loop may not cut it short.
	viper.Set("bash_timeout", 60)
	want = time.Duration(1800+ShellDispatchMarginSeconds) * time.Second
	if got := ShellDispatchWatchdog(1800); got != want {
		t.Errorf("per-call 1800s: watchdog = %s, want %s", got, want)
	}
	if ShellDispatchWatchdog(60) < wantBackstop {
		t.Errorf("watchdog for a short call dropped below the backstop: %s", ShellDispatchWatchdog(60))
	}

	// The ceiling case stays bounded: even the largest accepted shell timeout
	// yields ceiling + margin, not an unbounded wait.
	maxWant := time.Duration(BashTimeoutHardCeilSeconds+ShellDispatchMarginSeconds) * time.Second
	if got := ShellDispatchWatchdog(BashTimeoutHardCeilSeconds); got != maxWant {
		t.Errorf("ceiling call: watchdog = %s, want %s", got, maxWant)
	}
}
