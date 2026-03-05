//go:build windows

package server

import (
	"os/exec"
	"syscall"
)

const createNewConsole = 0x00000010

// detachProcess configures the command to run in a new console (detached from parent).
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
}
