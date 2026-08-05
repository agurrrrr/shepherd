//go:build windows

package worker

import (
	"os/exec"

	"github.com/agurrrrr/shepherd/internal/procutil"
)

// setProcessGroup suppresses the console window Windows would auto-create
// for each CLI child (claude, opencode, pi, grok) of the console-less
// daemon. Without it every worker task pops a visible cmd window that
// steals focus (regression after 5e6ed3e B4 detached the daemon).
func setProcessGroup(cmd *exec.Cmd) {
	procutil.HideWindow(cmd)
}

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
