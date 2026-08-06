//go:build windows

package embedded

import (
	"context"
	"fmt"
)

// Windows shell auto-detection.
//
// Git Bash comes first on purpose. The tool is named "bash" and the system
// prompt describes a POSIX world, so the model keeps emitting `ls`, `sed`,
// `grep`, `&&` and /tmp-style paths. Putting PowerShell ahead of bash would
// leave the tool technically working while every command the agent writes
// fails — the worst of both worlds.
//
// cmd.exe is deliberately absent: its quoting, piping and exit-code semantics
// differ enough from both bash and PowerShell that falling back to it silently
// turns one clear failure into a long debugging session. When nothing is
// found, we say so and point at the fix.

// gitBashFallbacks are the install locations Git for Windows uses when its bin
// directory is not on PATH (the default "Git from the command line only"
// installer option leaves cmd/ on PATH but not bin/).
var gitBashFallbacks = []string{
	`C:\Program Files\Git\bin\bash.exe`,
	`C:\Program Files (x86)\Git\bin\bash.exe`,
}

// windowsShellCandidates are the PowerShell executables tried after bash.
var windowsShellCandidates = []string{"pwsh.exe", "powershell.exe"}

// detectShell resolves in the order: Git Bash (PATH, then conventional install
// paths) → pwsh → powershell.
//
// Caveat worth knowing when diagnosing a report: on a machine with WSL
// enabled, PATH may hold C:\Windows\System32\bash.exe, which launches the
// Linux distro and sees a completely different filesystem than the project
// path we pass as the working directory. That WSL shim is skipped by
// isWslBash so it cannot shadow Git Bash / PowerShell — otherwise every bash
// tool call would "find bash" and then fail on the Windows working directory.
// SHEPHERD_SHELL remains the escape hatch when nothing on PATH suits.
func detectShell() (*resolvedShell, error) {
	if path, err := lookPath("bash"); err == nil && !isWslBash(path) {
		return &resolvedShell{path: path, kind: shellKindFor(path)}, nil
	}
	for _, path := range gitBashFallbacks {
		if info, err := statShell(path); err == nil && !info.IsDir() {
			return &resolvedShell{path: path, kind: shellKindFor(path)}, nil
		}
	}
	for _, name := range windowsShellCandidates {
		if path, err := lookPath(name); err == nil {
			return &resolvedShell{path: path, kind: shellKindFor(path)}, nil
		}
	}
	return nil, fmt.Errorf(
		"no supported shell found: install Git for Windows (bash) or PowerShell 7 (pwsh), or set the %q config key / SHEPHERD_SHELL to a shell executable path",
		"shell")
}

// newShellProc prepares the shell command plus its cleanup.
//
// Windows has no process groups. killProcessGroup uses taskkill /T /F (with
// Process.Kill fallback) so children do not survive cancel/timeout as orphans.
// cmd.Cancel is wired in newShellCmd to the same cleanup so CommandContext
// cancel and the post-Run safety net share one path.
//
// P2: Job Object (KILL_ON_JOB_CLOSE) would replace the taskkill body inside
// killProcessGroup / setupProcessGroup; this shellProc shape stays put.
func newShellProc(ctx context.Context, command, workdir string) (*shellProc, error) {
	cmd, release, err := newShellCmd(ctx, command, workdir)
	if err != nil {
		return nil, err
	}
	return &shellProc{
		cmd:     cmd,
		cleanup: func() { killProcessGroup(cmd) },
		// release deletes the temp .ps1 when the PowerShell path had to spill
		// a long command to disk; shellProc.close runs it on every exit path.
		release: release,
	}, nil
}
