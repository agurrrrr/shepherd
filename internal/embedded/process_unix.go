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

// killProcessGroup kills the entire process group. On Unix this sends SIGKILL
// to the negative PID (process group). Returns true if there was a process to kill.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Process.Wait() // avoid zombie
	}
}
