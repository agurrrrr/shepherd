package queue

import (
	"testing"

	"github.com/spf13/viper"
)

func TestGroupKey(t *testing.T) {
	// Isolate from any global per-provider model defaults so the per-task model
	// is the only variable under test. Use viper.Set (in-memory override) rather
	// than config.Set, which would persist to the user's config file.
	viper.Set("model_claude", "")
	viper.Set("model_opencode", "")
	t.Cleanup(func() {
		viper.Set("model_claude", "")
		viper.Set("model_opencode", "")
	})

	tests := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{"opencode with per-task model", "opencode", "local-llm/qwen2.5:14b", "opencode/local-llm/qwen2.5:14b"},
		{"second opencode system is a distinct group", "opencode", "remote-b/devstral", "opencode/remote-b/devstral"},
		{"opencode without model folds to provider key", "opencode", "", "opencode"},
		{"auto provider folds into claude", "auto", "", "claude"},
		{"claude with per-task model", "claude", "opus", "claude/opus"},
		{"whitespace-only model treated as empty", "opencode", "   ", "opencode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := groupKey(tt.provider, tt.model); got != tt.want {
				t.Errorf("groupKey(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
			}
		})
	}

	t.Run("empty per-task model falls back to global opencode default", func(t *testing.T) {
		viper.Set("model_opencode", "local-llm/devstral")
		defer viper.Set("model_opencode", "")
		if got := groupKey("opencode", ""); got != "opencode/local-llm/devstral" {
			t.Errorf("groupKey fallback = %q, want %q", got, "opencode/local-llm/devstral")
		}
	})
}

func TestGroupKey_EmbeddedWithEndpointID(t *testing.T) {
	// embedded provider uses the model field as endpoint ID
	got := groupKey("embedded", "qwen-coder")
	want := "embedded/qwen-coder"
	if got != want {
		t.Fatalf("groupKey(embedded, qwen-coder) = %q, want %q", got, want)
	}
}

func TestGroupKey_EmbeddedWithoutModel(t *testing.T) {
	got := groupKey("embedded", "")
	want := "embedded"
	if got != want {
		t.Fatalf("groupKey(embedded, \"\") = %q, want %q", got, want)
	}
}

func TestGroupKey_EmbeddedDifferentEndpointsSeparateGroups(t *testing.T) {
	got1 := groupKey("embedded", "endpoint-a")
	got2 := groupKey("embedded", "endpoint-b")
	if got1 == got2 {
		t.Fatalf("different endpoints should have different group keys: %q == %q", got1, got2)
	}
}

func TestGroupConcurrencyLimit(t *testing.T) {
	tests := []struct {
		name     string
		limits   map[string]int
		groupKey string
		provider string
		want     int
	}{
		{
			name:     "nil map yields no limit",
			limits:   nil,
			groupKey: "claude",
			provider: "claude",
			want:     0,
		},
		{
			name:     "provider-only key matches",
			limits:   map[string]int{"opencode": 1, "claude": 3},
			groupKey: "opencode",
			provider: "opencode",
			want:     1,
		},
		{
			name:     "exact provider/model key wins over provider fallback",
			limits:   map[string]int{"claude": 3, "claude/opus": 1},
			groupKey: "claude/opus",
			provider: "claude",
			want:     1,
		},
		{
			name:     "provider/model group falls back to provider-only key",
			limits:   map[string]int{"opencode": 2},
			groupKey: "opencode/qwen3.6-27b",
			provider: "opencode",
			want:     2,
		},
		{
			name:     "auto provider falls back to claude key",
			limits:   map[string]int{"claude": 4},
			groupKey: "claude",
			provider: "auto",
			want:     4,
		},
		{
			name:     "no matching key yields no limit",
			limits:   map[string]int{"opencode": 1},
			groupKey: "claude",
			provider: "claude",
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := groupConcurrencyLimit(tt.limits, tt.groupKey, tt.provider)
			if got != tt.want {
				t.Errorf("groupConcurrencyLimit(%v, %q, %q) = %d, want %d",
					tt.limits, tt.groupKey, tt.provider, got, tt.want)
			}
		})
	}
}
