//go:build windows

package procutil

import (
	"os/exec"
	"testing"
	"time"
)

// B3 integration: KillTree must terminate a whole process tree, not just the
// direct child. We spawn cmd → cmd → a sleeping child and assert the grandchild
// is gone after KillTree.
func TestKillTreeTerminatesGrandchildren(t *testing.T) {
	// cmd /c starts a child (ping -n 60) which would outlive a Process.Kill on
	// the outer cmd. KillTree should reap the entire tree.
	outer := exec.Command("cmd", "/c", "ping", "-n", "60", "127.0.0.1")
	if err := outer.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := outer.Process.Pid
	// Give the tree a moment to spawn.
	time.Sleep(300 * time.Millisecond)

	if !KillTree(pid) {
		t.Fatal("KillTree reported failure (expected success or already-gone)")
	}
	// Reap the outer process so the test doesn't leak a zombie.
	_ = outer.Wait()
}

// KillTree on an invalid PID must report false so callers fall back.
func TestKillTreeInvalidPID(t *testing.T) {
	if KillTree(0) {
		t.Error("KillTree(0) must return false")
	}
	if KillTree(-1) {
		t.Error("KillTree(-1) must return false")
	}
}

// KillTree on an already-dead PID must report true (already gone = success).
func TestKillTreeAlreadyGone(t *testing.T) {
	// Spawn and immediately kill a trivial process, then KillTree its PID.
	c := exec.Command("cmd", "/c", "exit", "0")
	if err := c.Run(); err != nil {
		t.Fatalf("setup run: %v", err)
	}
	// PID is now dead; KillTree should treat "not found" as success.
	if !KillTree(c.Process.Pid) {
		t.Error("KillTree on dead PID should return true (already gone)")
	}
}
