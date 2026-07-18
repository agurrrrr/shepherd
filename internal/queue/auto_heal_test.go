package queue

import (
	"testing"

	"github.com/agurrrrr/shepherd/ent/sheep"
)

func TestShouldAutoHealError(t *testing.T) {
	tests := []struct {
		name        string
		status      sheep.Status
		taskRunning bool
		want        bool
	}{
		{"error with no running task heals", sheep.StatusError, false, true},
		{"error with live task does not heal", sheep.StatusError, true, false},
		{"idle never heals", sheep.StatusIdle, false, false},
		{"working never heals via this path", sheep.StatusWorking, false, false},
		{"working with live task", sheep.StatusWorking, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldAutoHealError(tt.status, tt.taskRunning); got != tt.want {
				t.Errorf("shouldAutoHealError(%v, %v) = %v, want %v",
					tt.status, tt.taskRunning, got, tt.want)
			}
		})
	}
}
