//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
)

// IsRunning checks if the daemon is currently running.
func IsRunning() bool {
	pid, err := ReadPID()
	if err != nil {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		_ = RemovePID()
		return false
	}

	// Signal 0 checks if the process exists without sending a real signal
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		_ = RemovePID()
		return false
	}

	return true
}

// Stop sends SIGTERM to the running daemon.
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

	if err := process.Signal(syscall.SIGTERM); err != nil {
		_ = RemovePID()
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	return nil
}

// IsPIDAlive reports whether a process with the given PID currently exists.
// Used to decide whether a task's owning process is still running before
// recovering it as "interrupted".
func IsPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 probes existence without actually signalling the process.
	return process.Signal(syscall.Signal(0)) == nil
}

// GetStatus returns the daemon's PID and running status.
func GetStatus() (pid int, running bool) {
	pid, err := ReadPID()
	if err != nil {
		return 0, false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		_ = RemovePID()
		return 0, false
	}

	err = process.Signal(syscall.Signal(0))
	if err != nil {
		_ = RemovePID()
		return pid, false
	}

	return pid, true
}
