package worker

import (
	"os/exec"
	"testing"
)

func TestClaimDispatch_PreventsDoubleClaim(t *testing.T) {
	const name = "test-sheep-claim-dispatch"
	unregisterRunningTask(name, runningTasks[name])
	ReleaseDispatch(name, 1)

	if !ClaimDispatch(name, 42) {
		t.Fatal("first claim should succeed")
	}
	if !IsTaskRunning(name) {
		t.Fatal("claim should mark task running")
	}
	if ClaimDispatch(name, 43) {
		t.Fatal("second claim on same sheep must fail")
	}
	// Real process registration replaces placeholder and preserves task ID.
	token := registerRunningTask(name, nil, exec.Command("true"))
	if token.TaskID != 42 {
		t.Fatalf("TaskID preserved from claim: got %d want 42", token.TaskID)
	}
	// ReleaseDispatch must not remove a registered process entry.
	ReleaseDispatch(name, 42)
	if !IsTaskRunning(name) {
		t.Fatal("ReleaseDispatch must not clear registered process entry")
	}
	unregisterRunningTask(name, token)
	if IsTaskRunning(name) {
		t.Fatal("expected clean after unregister")
	}

	// Placeholder-only release.
	if !ClaimDispatch(name, 99) {
		t.Fatal("claim after clear should succeed")
	}
	ReleaseDispatch(name, 99)
	if IsTaskRunning(name) {
		t.Fatal("placeholder claim should release")
	}
}

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
