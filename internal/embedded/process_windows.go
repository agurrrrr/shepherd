//go:build windows

package embedded

import (
	"os/exec"
)

// setupProcessGroup is a no-op on Windows because Setpgid is not available.
func setupProcessGroup(cmd *exec.Cmd) {
	// Windows does not support Unix process groups.
}

// killProcessGroup kills the process using Process.Kill() which is the
// Windows-compatible way to terminate a subprocess.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Process.Wait() // avoid zombie
	}
}
