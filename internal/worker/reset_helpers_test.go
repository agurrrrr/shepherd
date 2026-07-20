package worker

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsLiveForeignOwned(t *testing.T) {
	alive := func(pid int) bool { return pid == 9999 }
	self := 1000

	tests := []struct {
		name     string
		ownerPid int
		alive    func(int) bool
		want     bool
	}{
		{
			name:     "owner_pid zero is orphan (not foreign live)",
			ownerPid: 0,
			alive:    alive,
			want:     false,
		},
		{
			name:     "self pid is not foreign",
			ownerPid: self,
			alive:    alive,
			want:     false,
		},
		{
			name:     "dead foreign pid is orphan",
			ownerPid: 4242,
			alive:    func(int) bool { return false },
			want:     false,
		},
		{
			name:     "live foreign pid must refuse reset",
			ownerPid: 9999,
			alive:    alive,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLiveForeignOwned(tt.ownerPid, self, tt.alive)
			if got != tt.want {
				t.Errorf("isLiveForeignOwned(%d, %d, …) = %v, want %v",
					tt.ownerPid, self, got, tt.want)
			}
		})
	}
}

func TestIsNonRetryableExecuteError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"timeout with duration", fmt.Errorf("execution timeout (exceeded 10m0s)"), true},
		{"timeout short", errors.New("execution timeout"), true},
		{"task cancelled", errors.New("task was cancelled"), true},
		{"context canceled", errors.New("context canceled"), true},
		{"context cancelled spelling", errors.New("context cancelled"), true},
		{"generic failure retries", errors.New("Claude Code execution failed: exit status 1"), false},
		{"stale session retries once path", errors.New("No conversation found with session ID"), false},
		{"rate limit retries", errors.New("rate limit exceeded"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNonRetryableExecuteError(tt.err)
			if got != tt.want {
				t.Errorf("isNonRetryableExecuteError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
