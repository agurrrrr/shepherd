//go:build windows

package daemon

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"time"
)

const processQueryLimitedInfo = 0x1000

// stopGracePeriod bounds how long Stop waits for the daemon to exit after a
// graceful shutdown request before falling back to TerminateProcess.
const stopGracePeriod = 10 * time.Second

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

// Stop asks the running daemon to shut itself down gracefully on Windows.
//
// Windows has no portable SIGTERM: os.Process.Kill() is TerminateProcess,
// which kills the daemon instantly and skips every cleanup step in
// runServeForeground (CancelAllRunningTasks, stuck-task recovery, PID/runtime
// file removal, db.Close). Orphaned task children and inconsistent DB state
// were the result (windows_tool_bugs B2).
//
// Instead, Stop POSTs to the daemon's loopback-only /api/_internal/shutdown
// endpoint (authenticated with the shared X-MCP-Token from runtime.json).
// The daemon then runs the exact same graceful path as a console Ctrl+C.
// Only when the request cannot be made — stale PID file, dead HTTP listener,
// corrupted runtime.json — or the daemon does not exit within the grace
// period do we fall back to TerminateProcess.
func Stop() error {
	pid, err := ReadPID()
	if err != nil {
		return fmt.Errorf("daemon is not running (no PID file)")
	}

	if err := requestGracefulShutdown(); err == nil {
		// Wait for the daemon to actually exit so the caller's "stopped"
		// message is truthful and the PID file is gone on return.
		deadline := time.Now().Add(stopGracePeriod)
		for time.Now().Before(deadline) {
			if !isProcessAlive(pid) {
				_ = RemovePID()
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		// Graceful request accepted but process still alive — fall through to
		// Kill rather than reporting success falsely.
	}

	// Fallback: no reachable daemon HTTP endpoint — force-kill.
	process, err := os.FindProcess(pid)
	if err != nil {
		_ = RemovePID()
		return fmt.Errorf("daemon process not found")
	}
	if err := process.Kill(); err != nil {
		_ = RemovePID()
		return fmt.Errorf("failed to stop daemon: %w", err)
	}
	return nil
}

// requestGracefulShutdown POSTs to the daemon's internal shutdown endpoint.
// Returns nil only when the daemon accepted the request (HTTP 2xx).
func requestGracefulShutdown() error {
	info, err := ReadRuntime()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, info.Addr+"/api/_internal/shutdown", bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("X-MCP-Token", info.MCPToken)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("shutdown request rejected: HTTP %d", resp.StatusCode)
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

// IsPIDAlive reports whether a process with the given PID currently exists.
// Used to decide whether a task's owning process is still running before
// recovering it as "interrupted".
func IsPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return isProcessAlive(pid)
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
