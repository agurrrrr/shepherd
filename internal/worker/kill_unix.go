//go:build !windows

package worker

import (
	"os/exec"
	"syscall"
)

// setProcessGroup configures the command to run in a new process group.
// This allows killing all child processes when the parent is killed.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup kills the entire process group (parent + all children).
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Negative PID kills the process group
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
