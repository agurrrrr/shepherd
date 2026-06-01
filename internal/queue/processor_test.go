package queue

import "testing"

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
