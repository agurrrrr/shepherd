//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// detachProcess configures the command to run in a new session (detached from parent).
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
