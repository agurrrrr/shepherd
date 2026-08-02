//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	// detachedProcess starts the child without any console window. Combined
	// with createNewProcessGroup so the daemon is not tied to the parent's
	// console Ctrl+C handling. Replaces the old CREATE_NEW_CONSOLE, which
	// popped a visible black console window on the user's desktop and killed
	// the daemon when that window was closed (windows_tool_bugs B4).
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// shutdownSignals returns the OS signals to listen for graceful shutdown.
// Windows only supports os.Interrupt (CTRL+C).
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

// detachProcess configures the command to run fully detached from the parent:
// no new console window (DETACHED_PROCESS) and its own process group so a
// stray Ctrl+C in the parent console does not propagate.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup,
	}
}
