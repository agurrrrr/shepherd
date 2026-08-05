//go:build windows

package procutil

import (
	"os/exec"
	"syscall"
)

// createNoWindow tells CreateProcess to run a console-subsystem child without
// creating a console window for it. Required when the parent has no console
// (the daemon starts DETACHED_PROCESS): without it Windows auto-creates a new
// console window per child, popping cmd windows that steal focus every time
// the agent runs a shell command, git, taskkill, or a worker CLI
// (windows_tool_bugs regression after 5e6ed3e B4).
//
// 0x08000000 = CREATE_NO_WINDOW. Incompatible with DETACHED_PROCESS and
// CREATE_NEW_CONSOLE — never combine them on the same child.
const createNoWindow = 0x08000000

// HideWindow configures cmd so a console-subsystem child (bash.exe, git.exe,
// taskkill.exe, claude.cmd, …) starts without a visible console window.
//
// HideWindow alone only affects children that inherit the parent's console;
// CREATE_NO_WINDOW additionally suppresses the brand-new console Windows
// would otherwise create for a console child of a console-less parent.
// Both are set so the window is suppressed in every ancestor configuration.
//
// Callers that already set SysProcAttr for other reasons must merge flags
// instead of overwriting (see server/proc_windows.go).
func HideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
