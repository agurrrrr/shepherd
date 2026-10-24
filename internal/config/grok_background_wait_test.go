package config

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

// GetGrokBackgroundWait (#619): grok's headless run stops the background work it
// started once its built-in 10-minute wait expires and exits 1, which failed
// every task whose real work (remote build / model download) runs longer. The
// grok_background_wait key replaces that cap, and a positive result is always
// kept below task_timeout so the daemon's own deadline never fires first.
func TestGetGrokBackgroundWait(t *testing.T) {
	tests := []struct {
		name        string
		setting     string
		taskTimeout string
		want        time.Duration
	}{
		// empty → default 60m (task_timeout 4h is above it, so no cap).
		{name: "empty uses default", setting: "", taskTimeout: "4h", want: DefaultGrokBackgroundWait},

		// explicit durations pass through.
		{name: "explicit 90s", setting: "90s", taskTimeout: "4h", want: 90 * time.Second},
		{name: "explicit 30m", setting: "30m", taskTimeout: "4h", want: 30 * time.Minute},

		// off-style sentinels → no wait (caller passes --no-wait-for-background).
		{name: "off", setting: "off", taskTimeout: "4h", want: GrokBackgroundWaitOff},
		{name: "zero", setting: "0", taskTimeout: "4h", want: GrokBackgroundWaitOff},
		{name: "disable", setting: "disable", taskTimeout: "4h", want: GrokBackgroundWaitOff},
		{name: "disabled", setting: "disabled", taskTimeout: "4h", want: GrokBackgroundWaitOff},
		{name: "negative duration", setting: "-10m", taskTimeout: "4h", want: GrokBackgroundWaitOff},

		// unlimited-style sentinels (#625) → wait just under task_timeout, the
		// same convention task_timeout itself uses — NOT "no wait".
		{name: "unlimited capped below task_timeout", setting: "unlimited", taskTimeout: "4h", want: 3*time.Hour + 55*time.Minute},
		{name: "none capped below task_timeout", setting: "none", taskTimeout: "4h", want: 3*time.Hour + 55*time.Minute},
		{name: "negative one capped below task_timeout", setting: "-1", taskTimeout: "4h", want: 3*time.Hour + 55*time.Minute},
		// unlimited + unlimited task_timeout → the large fixed bound, never 0/off.
		{name: "unlimited with unlimited task_timeout", setting: "unlimited", taskTimeout: "off", want: MaxGrokBackgroundWait},
		{name: "none with zero task_timeout", setting: "none", taskTimeout: "0", want: MaxGrokBackgroundWait},
		{name: "negative one with unlimited task_timeout", setting: "-1", taskTimeout: "unlimited", want: MaxGrokBackgroundWait},
		// unlimited + tiny task_timeout still keeps the usable floor.
		{name: "unlimited floors on tiny task_timeout", setting: "unlimited", taskTimeout: "2m", want: time.Minute},

		// typo → default, never grok's own 10m (mirrors GetTaskTimeout's guard).
		{name: "typo falls back to default", setting: "60mintues", taskTimeout: "4h", want: DefaultGrokBackgroundWait},

		// task_timeout ceiling: the wait must stay strictly below the task
		// deadline, with headroom, so grok can stop its background work and
		// report before the daemon SIGKILLs the process group.
		{name: "capped below task_timeout", setting: "90m", taskTimeout: "1h", want: 55 * time.Minute},
		{name: "default capped when task_timeout short", setting: "", taskTimeout: "45m", want: 40 * time.Minute},
		{name: "equal to task_timeout is capped", setting: "4h", taskTimeout: "4h", want: 3*time.Hour + 55*time.Minute},

		// very short task_timeout keeps a usable floor instead of 0/negative.
		{name: "floor for tiny task_timeout", setting: "", taskTimeout: "2m", want: time.Minute},
		{name: "floor for one-minute task_timeout", setting: "5m", taskTimeout: "1m", want: time.Minute},

		// task_timeout unlimited (off/0) → no cap on the wait.
		{name: "unlimited task_timeout no cap", setting: "2h", taskTimeout: "off", want: 2 * time.Hour},
		{name: "zero task_timeout no cap", setting: "", taskTimeout: "0", want: DefaultGrokBackgroundWait},

		// off wins over any task_timeout.
		{name: "off ignores task_timeout", setting: "off", taskTimeout: "10m", want: GrokBackgroundWaitOff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			defer viper.Reset()
			viper.SetDefault("task_timeout", "4h")
			viper.SetDefault("grok_background_wait", "")
			viper.Set("grok_background_wait", tt.setting)
			viper.Set("task_timeout", tt.taskTimeout)

			if got := GetGrokBackgroundWait(); got != tt.want {
				t.Fatalf("GetGrokBackgroundWait(grok_background_wait=%q, task_timeout=%q) = %v, want %v",
					tt.setting, tt.taskTimeout, got, tt.want)
			}
		})
	}
}

// TestGetGrokBackgroundWaitDefaultRegistered checks the key is registered in
// Init()'s viper.SetDefault block, so a config.yaml without the key still gets a
// sane value instead of an accidental empty-string read path.
func TestGetGrokBackgroundWaitDefaultRegistered(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.SetDefault("grok_background_wait", "")
	if viper.GetString("grok_background_wait") != "" {
		t.Fatal("expected the registered default to be empty (auto mode)")
	}
	// Empty + no task_timeout → DefaultGrokBackgroundWait.
	if got := GetGrokBackgroundWait(); got != DefaultGrokBackgroundWait {
		t.Fatalf("unset: got %v, want %v", got, DefaultGrokBackgroundWait)
	}
}
