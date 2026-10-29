package config

import (
	"testing"

	"github.com/spf13/viper"
)

// Rate-limit handoff settings (#675): two string keys with empty defaults, a
// strict provider whitelist (so a typo cannot silently disable the feature),
// and an "auto" fold that matches ExecuteInteractive's runtime behavior.

func TestLimitHandoffDefaultsRegistered(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	// Mirror the Init() registration for the two keys: both default to empty,
	// which means the feature is OFF until the user picks a target.
	viper.SetDefault("limit_handoff_provider", "")
	viper.SetDefault("limit_handoff_model", "")

	if p := GetString("limit_handoff_provider"); p != "" {
		t.Fatalf("default limit_handoff_provider = %q, want empty (feature off)", p)
	}
	if m := GetString("limit_handoff_model"); m != "" {
		t.Fatalf("default limit_handoff_model = %q, want empty", m)
	}
	if p, m := LimitHandoffTarget(); p != "" || m != "" {
		t.Fatalf("LimitHandoffTarget with defaults = (%q,%q), want off", p, m)
	}
}

func TestNormalizeHandoffProvider(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"empty stays empty (off)", "", "", false},
		{"whitespace only is off", "   ", "", false},
		{"auto folds to claude", "auto", "claude", false},
		{"case and space normalized", "  Grok ", "grok", false},
		{"claude", "claude", "claude", false},
		{"opencode", "opencode", "opencode", false},
		{"pi", "pi", "pi", false},
		{"embedded", "embedded", "embedded", false},
		{"magi", "magi", "magi", false},
		{"unknown rejected", "gpt-cli", "", true},
		{"empty-ish typo rejected", "claudee", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeHandoffProvider(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NormalizeHandoffProvider(%q) err=%v, wantErr=%v", tc.raw, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("NormalizeHandoffProvider(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNormalizeHandoffModel(t *testing.T) {
	if got := NormalizeHandoffModel("  grok-4.5  "); got != "grok-4.5" {
		t.Fatalf("trim = %q", got)
	}
	if got := NormalizeHandoffModel(""); got != "" {
		t.Fatalf("empty stays empty: %q", got)
	}
}

func TestLimitHandoffTargetReadsAndValidates(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.SetDefault("limit_handoff_provider", "")
	viper.SetDefault("limit_handoff_model", "")

	viper.Set("limit_handoff_provider", " Grok ")
	viper.Set("limit_handoff_model", " grok-4.5 ")
	if p, m := LimitHandoffTarget(); p != "grok" || m != "grok-4.5" {
		t.Fatalf("LimitHandoffTarget = (%q,%q), want (grok,grok-4.5)", p, m)
	}

	// A hand-edited config.yaml with an unknown provider must count as OFF —
	// the dispatcher never sees an executable-but-nonsense target.
	viper.Set("limit_handoff_provider", "not-a-provider")
	if p, m := LimitHandoffTarget(); p != "" || m != "" {
		t.Fatalf("invalid stored provider = (%q,%q), want off", p, m)
	}

	viper.Set("limit_handoff_provider", "")
	if p, _ := LimitHandoffTarget(); p != "" {
		t.Fatalf("cleared provider = %q, want off", p)
	}
}
