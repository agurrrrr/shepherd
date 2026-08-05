//go:build !windows

package procutil

import "os/exec"

// HideWindow is a no-op on Unix: console windows are a Windows concept.
// It exists so daemon-spawned child commands can call it unconditionally.
func HideWindow(cmd *exec.Cmd) {}
