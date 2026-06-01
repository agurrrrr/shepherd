package config

import (
	"testing"

	"github.com/spf13/viper"
)

// TestGetConcurrencyLimitsInProcessSet is a regression test for a bug where a
// concurrency limit saved through the settings UI read back as empty until the
// daemon restarted. viper.Set stores the value in the override layer as a typed
// map[string]int, and viper.GetStringMap (and cast.ToStringMap) return an empty
// map for that shape — so the settings page showed no value and the dispatch
// gate ignored the limit. GetConcurrencyLimits must read it via viper.Get and
// normalize the typed map itself.
func TestGetConcurrencyLimitsInProcessSet(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.SetDefault("concurrency_limits", map[string]interface{}{})

	// Shape written by viper.Set in the running process (override layer).
	viper.Set("concurrency_limits", map[string]int{"claude": 2, "opencode": 1})
	got := GetConcurrencyLimits()
	if got["claude"] != 2 || got["opencode"] != 1 {
		t.Fatalf("in-process Set: got %v, want claude=2 opencode=1", got)
	}
}

// TestGetConcurrencyLimitsFromFileShape covers the shape viper produces after
// parsing the YAML config file on startup (map[string]interface{} with numeric
// values), which is the path that already worked before the fix.
func TestGetConcurrencyLimitsFromFileShape(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.Set("concurrency_limits", map[string]interface{}{
		"claude":   int(2),
		"opencode": float64(1), // YAML/JSON numbers can arrive as float64
		"auto":     "3",        // tolerate string-encoded ints
	})
	got := GetConcurrencyLimits()
	if got["claude"] != 2 || got["opencode"] != 1 || got["auto"] != 3 {
		t.Fatalf("file shape: got %v, want claude=2 opencode=1 auto=3", got)
	}
}

// TestGetConcurrencyLimitsEmpty returns nil when nothing is configured so
// callers can treat "no limits" uniformly.
func TestGetConcurrencyLimitsEmpty(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	if got := GetConcurrencyLimits(); got != nil {
		t.Fatalf("unset: got %v, want nil", got)
	}
}
