//go:build windows

package server

import (
	"os/exec"
	"syscall"
)

const (
	// detachedProcess starts the replacement daemon without any console
	// window. Must match cmd/shepherd/platform_windows.go: CREATE_NEW_CONSOLE
	// (the old value here) popped a visible console window on every daemon
	// restart, and mixing it with the daemon's own DETACHED_PROCESS start
	// would leave the two entry points inconsistent.
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// detachProcess configures the restart child to run fully detached: no
// console window (DETACHED_PROCESS) and its own process group so a stray
// Ctrl+C elsewhere does not propagate. CREATE_NO_WINDOW must NOT be combined
// with DETACHED_PROCESS — they are mutually exclusive creation flags.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}
