//go:build !windows

package embedded

import (
	"context"
	"fmt"
)

// unixShellCandidates is the auto-detection order when no shell override is
// set. bash first keeps behavior identical to the previous hard-coded
// `exec.Command("bash", "-c", ...)`. zsh is preferred over sh: systems where
// zsh is the login shell usually ship a richer zsh than a bare-bones sh
// (often dash), and zsh handles POSIX `-c` just as well. sh is the final
// fallback for the rare minimal image that ships neither.
var unixShellCandidates = []string{"bash", "zsh", "sh"}

// detectShell finds a shell on PATH, preferring bash, then zsh, then sh.
func detectShell() (*resolvedShell, error) {
	for _, name := range unixShellCandidates {
		if path, err := lookPath(name); err == nil {
			return &resolvedShell{path: path, kind: shellKindFor(path)}, nil
		}
	}
	return nil, fmt.Errorf(
		"no supported shell found: install bash, zsh, or sh, or set the %q config key / SHEPHERD_SHELL to a shell executable path",
		"shell")
}

// newShellProc starts nothing yet — it prepares the command and the cleanup
// that kills the whole process tree. On Unix the shell runs in its own process
// group (setupProcessGroup), so a single signal to the negative PID reaps
// children the shell spawned.
func newShellProc(ctx context.Context, command, workdir string) (*shellProc, error) {
	cmd, release, err := newShellCmd(ctx, command, workdir)
	if err != nil {
		return nil, err
	}
	return &shellProc{
		cmd:     cmd,
		cleanup: func() { killProcessGroup(cmd) },
		release: release,
	}, nil
}
