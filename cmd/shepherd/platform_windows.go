//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

const createNewConsole = 0x00000010

// shutdownSignals returns the OS signals to listen for graceful shutdown.
// Windows only supports os.Interrupt (CTRL+C).
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

// detachProcess configures the command to run in a new console (detached from parent).
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
}
