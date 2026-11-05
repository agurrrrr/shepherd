package embedded

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/internal/config"
)

// setBashTimeout points the bash_timeout key at value for one test and restores
// whatever was there, so a case cannot leak its cap into the next one.
func setBashTimeout(t *testing.T, value interface{}) {
	t.Helper()
	orig := viper.Get("bash_timeout")
	viper.Set("bash_timeout", value)
	t.Cleanup(func() { viper.Set("bash_timeout", orig) })
}

// TestBashToolDefAdvertisesConfiguredDefault checks the schema text a model
// reads. A hardcoded "default 120" while the daemon is configured for 600 makes
// models cut their own builds short, so the number must come from config.
func TestBashToolDefAdvertisesConfiguredDefault(t *testing.T) {
	setBashTimeout(t, 45)

	tr := NewToolRegistry(t.TempDir(), "test", nil, nil)
	var found int
	for _, d := range tr.OpenAIToolDefs() {
		if d.Function.Name != "bash" && d.Function.Name != "shell" {
			continue
		}
		params, ok := d.Function.Parameters["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: parameters.properties missing", d.Function.Name)
		}
		spec, ok := params["timeout"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: timeout parameter missing from the schema", d.Function.Name)
		}
		desc, _ := spec["description"].(string)
		if !strings.Contains(desc, "45") {
			t.Errorf("%s timeout description = %q, want it to name the configured default 45",
				d.Function.Name, desc)
		}
		if strings.Contains(desc, "120") {
			t.Errorf("%s timeout description still hardcodes the old 120 default: %q", d.Function.Name, desc)
		}
		found++
	}
	if found == 0 {
		t.Fatal("no shell tool definition found")
	}
}

// TestExecBashUsesConfiguredDefaultTimeout runs a real command longer than the
// configured cap and requires the shell path's own readable timeout message, with
// the configured number named — not the old 120.
func TestExecBashUsesConfiguredDefaultTimeout(t *testing.T) {
	setBashTimeout(t, 1)

	tr := NewToolRegistry(t.TempDir(), "test", nil, nil)
	out, err := tr.execBash(context.Background(), map[string]interface{}{"command": "sleep 5"})
	if err != nil {
		t.Fatalf("execBash must report the timeout in its output, not as a tool error: %v", err)
	}
	if !strings.Contains(out, "timed out after 1s") {
		t.Errorf("output = %q, want the configured 1s cap named", out)
	}
	if strings.Contains(out, "120s") {
		t.Errorf("output = %q, must not name the removed 120 default", out)
	}
}

// TestExecBashHonoursPerCallTimeout checks that an explicit `timeout` argument
// still wins over the configured default — that argument existed before this
// setting and must keep working.
func TestExecBashHonoursPerCallTimeout(t *testing.T) {
	setBashTimeout(t, 600)

	tr := NewToolRegistry(t.TempDir(), "test", nil, nil)
	out, err := tr.execBash(context.Background(), map[string]interface{}{
		"command": "sleep 5",
		"timeout": 1,
	})
	if err != nil {
		t.Fatalf("execBash: unexpected error %v", err)
	}
	if !strings.Contains(out, "timed out after 1s") {
		t.Errorf("output = %q, want the per-call 1s cap named", out)
	}
}

// TestExecBashClampsRidiculousPerCallTimeout guards against a hallucinated
// timeout argument parking the turn: the effective cap is the hard ceiling, and
// the message reports that ceiling rather than the number the model asked for.
func TestExecBashClampsRidiculousPerCallTimeout(t *testing.T) {
	setBashTimeout(t, 1)

	tr := NewToolRegistry(t.TempDir(), "test", nil, nil)
	// Way over the ceiling. The command must still die at the configured default
	// scale for this test (the clamp keeps it bounded), so the run cannot hang:
	// we assert on the clamp arithmetic itself plus a short-timeout run.
	if got := config.ClampBashTimeoutSeconds(999999); got != config.BashTimeoutHardCeilSeconds {
		t.Errorf("clamp of 999999 = %d, want ceiling %d",
			got, config.BashTimeoutHardCeilSeconds)
	}

	out, err := tr.execBash(context.Background(), map[string]interface{}{
		"command": "sleep 5",
		"timeout": -3,
	})
	if err != nil {
		t.Fatalf("execBash: unexpected error %v", err)
	}
	if !strings.Contains(out, "timed out after 1s") {
		t.Errorf("output = %q, want the configured default (1s) to apply for a negative argument", out)
	}
}

// TestToolDispatchTimeoutFollowsBashTimeout is the loop-side half of the fix: the
// watchdog around a tool call must stretch with bash_timeout, or a 10-minute build
// dies at five minutes with a generic error.
func TestToolDispatchTimeoutFollowsBashTimeout(t *testing.T) {
	setBashTimeout(t, 600)
	if got := toolDispatchTimeout().String(); got != "10m30s" {
		t.Errorf("toolDispatchTimeout() = %s, want 10m30s", got)
	}

	setBashTimeout(t, 30)
	if got := toolDispatchTimeout(); got.Minutes() != 5 {
		t.Errorf("short bash_timeout: toolDispatchTimeout() = %s, want the 5m backstop", got)
	}
}
