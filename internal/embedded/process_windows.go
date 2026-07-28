//go:build windows

package embedded

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
// walks the process tree. Fall back to Process.Kill when taskkill is missing
// or fails for a reason other than "already gone" — killing the shell alone is
// still better than killing nothing.
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
	if killTreeWithTaskkill(pid) {
		return
	}
	// Fallback: at least the shell dies.
	_ = cmd.Process.Kill()
}

// taskkillTimeout bounds how long we wait on taskkill itself. An unbounded
// wait would block the cancel/timeout path that is trying to free resources.
const taskkillTimeout = 5 * time.Second

// killTreeWithTaskkill runs `taskkill /T /F /PID <pid>` without a shell.
// Returns true when the tree is gone (or was already gone); false when the
// caller should fall back to Process.Kill.
func killTreeWithTaskkill(pid int) bool {
	if pid <= 0 {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), taskkillTimeout)
	defer cancel()

	// Direct exec — never re-enter our shell resolution path just to run taskkill.
	c := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	out, err := c.CombinedOutput()
	if err == nil {
		return true
	}
	if taskkillAlreadyGone(err, out) {
		return true
	}

	// Unexpected failure only — log and let the caller fall back.
	msg := strings.TrimSpace(string(out))
	switch {
	case ctx.Err() != nil:
		fmt.Fprintf(os.Stderr, "shepherd: taskkill timed out for pid %d: %v\n", pid, err)
	case isExecNotFound(err):
		// taskkill missing from PATH — silent fallback to Process.Kill.
	default:
		if msg != "" {
			fmt.Fprintf(os.Stderr, "shepherd: taskkill failed for pid %d: %v (%s)\n", pid, err, msg)
		} else {
			fmt.Fprintf(os.Stderr, "shepherd: taskkill failed for pid %d: %v\n", pid, err)
		}
	}
	return false
}
