package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/agurrrrr/shepherd/internal/config"
)

func pidFilePath() string {
	return filepath.Join(config.GetConfigDir(), "shepherd.pid")
}

// WritePID writes the current process PID to the PID file.
func WritePID() error {
	pid := os.Getpid()
	return os.WriteFile(pidFilePath(), []byte(strconv.Itoa(pid)), 0644)
}

// RemovePID removes the PID file.
func RemovePID() error {
	return os.Remove(pidFilePath())
}

// ReadPID reads the PID from the PID file.
func ReadPID() (int, error) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID file: %w", err)
	}
	return pid, nil
}

// IsRunning checks if the daemon is currently running.
// It reads the PID file, verifies the process exists, and cleans up stale PID files.
func IsRunning() bool {
	pid, err := ReadPID()
	if err != nil {
		return false
	}

	// Check if the process is actually alive
	process, err := os.FindProcess(pid)
	if err != nil {
		_ = RemovePID()
		return false
	}

	// Signal 0 checks if the process exists without sending a real signal
	err = process.Signal(syscall.Signal(0))
	if err != nil {
		// Process doesn't exist — stale PID file
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
