package worker

import (
	"os/exec"
	"testing"
)

// TestUnregisterRunningTask_SelfGuard verifies the stop+restart race fix:
// a late-finishing task must only ever remove its OWN registry entry, never a
// newer task's that took over the same sheep name after a stop+restart.
func TestUnregisterRunningTask_SelfGuard(t *testing.T) {
	const name = "test-sheep-selfguard"

	// Clean up any leftover state.
	unregisterRunningTask(name, runningTasks[name])

	// Task A registers.
	cmdA := exec.Command("true")
	tokenA := registerRunningTask(name, nil, cmdA)

	// Task A is stopped: StopTask deletes the entry directly (no token).
	unregisterRunningTask(name, tokenA)
	if IsTaskRunning(name) {
		t.Fatal("expected no running task after Task A unregister")
	}

	// Task B (the restart) registers under the same name.
	cmdB := exec.Command("true")
	tokenB := registerRunningTask(name, nil, cmdB)

	// Task A finishes late and runs its deferred unregister with its OWN token.
	// This MUST NOT remove Task B's entry.
	if removed := unregisterRunningTask(name, tokenA); removed {
		t.Fatal("late Task A unregister wrongly removed Task B's entry")
	}
	if !IsTaskRunning(name) {
		t.Fatal("Task B entry was clobbered by late Task A unregister")
	}

	// The surviving entry must be Task B's.
	runningTasksMu.RLock()
	got := runningTasks[name]
	runningTasksMu.RUnlock()
	if got != tokenB {
		t.Fatalf("registry holds wrong entry: got %p, want Task B %p", got, tokenB)
	}

	// Task B finishes normally and removes its own entry.
	if removed := unregisterRunningTask(name, tokenB); !removed {
		t.Fatal("Task B failed to unregister its own entry")
	}
	if IsTaskRunning(name) {
		t.Fatal("expected no running task after Task B unregister")
	}
}
