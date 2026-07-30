//go:build !windows

package embedded

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestExecBashTimeoutKillsProcessTree verifies that a cancel/timeout reaps not
// only the shell but children it spawned. Without Setpgid + kill(-pid, SIGKILL)
// the background sleep would outlive the bash tool call as an orphan.
func TestExecBashTimeoutKillsProcessTree(t *testing.T) {
	resetShellCache()
	t.Cleanup(resetShellCache)

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	// Absolute path inside the script so cwd changes cannot misplace it.
	// sleep is backgrounded; wait keeps the shell alive until we kill the group.
	cmd := fmt.Sprintf(`sleep 120 & echo $! > %q; wait`, pidFile)

	tr := &ToolRegistry{projectPath: dir}
	out, err := tr.execBash(t.Context(), map[string]interface{}{
		"command": cmd,
		"timeout": 1,
	})
	if err != nil {
		t.Fatalf("execBash: %v", err)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("expected timeout message, got %q", out)
	}

	// Give the kill a moment to land, then require the child to be gone.
	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("child pid file never written (read err: %v); output was %q", readErr, out)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Poll until the child is reaped (or fail). kill(pid, 0) probes liveness.
	goneDeadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(childPID, 0)
		if err != nil {
			// ESRCH = no such process — the success case.
			if err == syscall.ESRCH {
				return
			}
			// Other errors (EPERM) still mean the PID exists in some form; keep waiting
			// only for a clear "gone". Most hosts return ESRCH once reaped.
		}
		if time.Now().After(goneDeadline) {
			// Final probe with a clearer message.
			if err := syscall.Kill(childPID, 0); err == nil || err != syscall.ESRCH {
				t.Fatalf("child pid %d still alive after process-group kill (probe err: %v); tree kill regressed", childPID, err)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
