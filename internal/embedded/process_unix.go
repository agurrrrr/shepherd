//go:build !windows

package embedded

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup creates a new process group so that on cancel/timeout we
// can kill the entire process tree (bash + all children).
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// applyCmdCCommandLine is a no-op on Unix: cmd.exe is a Windows path and
// argv from cmdCArgs is enough for tests that inspect *exec.Cmd.Args.
func applyCmdCCommandLine(*exec.Cmd, string, string) {}

// killProcessGroup kills the entire process group. On Unix this sends SIGKILL
// to the negative PID (process group).
//
// Wait is intentionally omitted: cmd.Run() already reaped the process.
// A second Wait here always returns an error and is dead code.
//
// Idempotent: safe to call from both cmd.Cancel (context cancel/timeout) and
// the post-Run safety net in execBash. SIGKILL on an already-dead group is a
// no-op at the kernel level.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		// Negative PID = process group. Do not change this signal path —
		// regressions here leave orphaned children after every timeout.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
