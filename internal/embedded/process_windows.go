//go:build windows

package embedded

import (
	"os/exec"

	"github.com/agurrrrr/shepherd/internal/procutil"
)

// setupProcessGroup is a no-op on Windows because Setpgid is not available.
//
// P2 (Job Object, not this step): CREATE_SUSPENDED → AssignProcessToJobObject
// with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE → ResumeThread. That would live here
// (assign on start) and in shellProc.cleanup (close the job handle). The
// shellProc wrapper is already in place so the call site need not change again.
func setupProcessGroup(cmd *exec.Cmd) {
	// Windows does not support Unix process groups.
}

// killProcessGroup terminates the shell and its descendants on Windows.
//
// Process.Kill alone only kills the shell; children (go build, npm, gradlew, …)
// survive as orphans after every timeout/cancel. Prefer taskkill /T /F which
// walks the process tree (procutil.KillTree). Fall back to Process.Kill when
// taskkill is missing or fails for a reason other than "already gone" —
// killing the shell alone is still better than killing nothing.
//
// Wait is intentionally omitted: cmd.Run() already reaped the process.
// A second Wait here always returns an error and is dead code.
//
// Idempotent: safe to call from both cmd.Cancel (context cancel/timeout) and
// the post-Run safety net in execBash. A second call on a dead PID is a no-op.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if procutil.KillTree(pid) {
		return
	}
	// Fallback: at least the shell dies.
	_ = cmd.Process.Kill()
}
