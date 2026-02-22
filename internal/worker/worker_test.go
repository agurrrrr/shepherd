package worker

import (
	"testing"

	"github.com/agurrrrr/shepherd/ent/sheep"
)

func TestStatusToKorean(t *testing.T) {
	tests := []struct {
		status   sheep.Status
		expected string
	}{
		{sheep.StatusIdle, "idle"},
		{sheep.StatusWorking, "working"},
		{sheep.StatusError, "error"},
		{sheep.Status("unknown"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := StatusToKorean(tt.status)
			if result != tt.expected {
				t.Errorf("StatusToKorean(%q) = %q, want %q", tt.status, result, tt.expected)
			}
		})
	}
}

func TestDefaultExecuteOptions(t *testing.T) {
	opts := DefaultExecuteOptions()

	if opts.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", opts.Timeout, DefaultTimeout)
	}

	if opts.MaxRetries != MaxRetries {
		t.Errorf("MaxRetries = %d, want %d", opts.MaxRetries, MaxRetries)
	}
}

func TestProviderToKorean(t *testing.T) {
	tests := []struct {
		provider sheep.Provider
		expected string
	}{
		{sheep.ProviderClaude, "Claude"},
		{sheep.ProviderOpencode, GetOpenCodeDisplayName()},
		{sheep.ProviderAuto, "auto"},
		{sheep.Provider("unknown"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			result := ProviderToKorean(tt.provider)
			if result != tt.expected {
				t.Errorf("ProviderToKorean(%q) = %q, want %q", tt.provider, result, tt.expected)
			}
		})
	}
}
