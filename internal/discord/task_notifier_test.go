package discord

import (
	"testing"

	"github.com/spf13/viper"
)

func TestShepherdReportLabel(t *testing.T) {
	cases := map[string]string{
		"review":  "목자 보고 · 검토",
		"patrol":  "목자 보고 · 순찰",
		"command": "목자 보고 · 지시",
		"weird":   "목자 보고",
	}
	for kind, want := range cases {
		if got := shepherdReportLabel(kind); got != want {
			t.Errorf("kind %q label = %q, want %q", kind, got, want)
		}
	}
}

// SendShepherdReport must be a no-op when the discord master switch is off or
// the webhook is empty, so a notification never blocks or errors task flow.
func TestSendShepherdReport_Gates(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	n := NewTaskNotifier()

	// Master off: nothing to send, no panic.
	viper.Set("discord_notifications_enabled", false)
	n.SendShepherdReport(5, "review", "report", 0.1)

	// Master on but empty webhook: still a no-op (Send returns nil early).
	viper.Set("discord_notifications_enabled", true)
	viper.Set("discord_webhook_url", "")
	n.SendShepherdReport(5, "patrol", "report", 0.1)
}
