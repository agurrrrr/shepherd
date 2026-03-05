//go:build windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

const processQueryLimitedInfo = 0x1000

// IsRunning checks if the daemon is currently running.
func IsRunning() bool {
	pid, err := ReadPID()
	if err != nil {
		return false
	}

	if !isProcessAlive(pid) {
		_ = RemovePID()
		return false
	}

	return true
}

// Stop terminates the running daemon process on Windows.
func Stop() error {
	pid, err := ReadPID()
	if err != nil {
		return fmt.Errorf("daemon is not running (no PID file)")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		_ = RemovePID()
		return fmt.Errorf("daemon process not found")
	}

	// On Windows, os.Process.Kill() calls TerminateProcess
	if err := process.Kill(); err != nil {
		_ = RemovePID()
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	return nil
}

// GetStatus returns the daemon's PID and running status.
func GetStatus() (pid int, running bool) {
	pid, err := ReadPID()
	if err != nil {
		return 0, false
	}

	if !isProcessAlive(pid) {
		_ = RemovePID()
		return pid, false
	}

	return pid, true
}

// isProcessAlive checks if a process with the given PID exists on Windows
// by attempting to open a handle to it.
func isProcessAlive(pid int) bool {
	handle, err := syscall.OpenProcess(processQueryLimitedInfo, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(handle)
	return true
}
