package queue

import (
	"testing"

	"github.com/agurrrrr/shepherd/ent/task"
)

func TestStatusToKorean(t *testing.T) {
	tests := []struct {
		status   task.Status
		expected string
	}{
		{task.StatusPending, "pending"},
		{task.StatusRunning, "running"},
		{task.StatusCompleted, "completed"},
		{task.StatusFailed, "failed"},
		{task.Status("unknown"), "unknown"},
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
