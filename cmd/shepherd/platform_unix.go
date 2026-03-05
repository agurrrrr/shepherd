//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// shutdownSignals returns the OS signals to listen for graceful shutdown.
func shutdownSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM}
}

// detachProcess configures the command to run in a new session (detached from parent).
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
