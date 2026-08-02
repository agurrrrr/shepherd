//go:build windows

package worker

import (
	"os/exec"

	"github.com/agurrrrr/shepherd/internal/procutil"
)

// setProcessGroup is a no-op on Windows.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the process and its whole tree on Windows.
//
// Previously this only called cmd.Process.Kill() (TerminateProcess on the
// direct child), so grandchildren a task spawned — claude/opencode launching
// node, python, etc. — were leaked as orphans on every cancel/timeout
// (windows_tool_bugs B3). procutil.KillTree walks the tree with taskkill
// /T /F, matching what the embedded shell runner already did; Process.Kill
// remains as the fallback when taskkill itself is unavailable.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if procutil.KillTree(cmd.Process.Pid) {
		return
	}
	_ = cmd.Process.Kill()
}
