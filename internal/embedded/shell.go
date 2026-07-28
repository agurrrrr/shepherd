package embedded

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/agurrrrr/shepherd/internal/config"
)

// Shell resolution for the "bash" tool.
//
// The tool is named "bash" on every platform and stays that way: loop.go keys
// its build-verification gate and future-intention stall counter off
// `case "bash"`, so an alias would silently break those. What varies per OS is
// only *which* shell binary backs the tool and how a command string is handed
// to it — that lives here plus shell_unix.go / shell_windows.go.
//
// Priority: SHEPHERD_SHELL / config "shell" (see config.GetShell) beats
// auto-detection, which is defined per platform in detectShell().

// shellKind identifies the command-line dialect of a resolved shell, derived
// from the executable's basename. It decides which argv the command string is
// wrapped in.
type shellKind string

const (
	shellKindBash       shellKind = "bash"
	shellKindSh         shellKind = "sh"
	shellKindPwsh       shellKind = "pwsh"
	shellKindPowerShell shellKind = "powershell"
	// shellKindUnknown is used for shells we do not recognize by name. They
	// are driven with the POSIX `-c <command>` convention, which is the only
	// safe guess; resolvedShell.unknown() lets callers surface that guess.
	shellKindUnknown shellKind = ""
)

// Injection points so shell discovery is testable without touching the host's
// real PATH or filesystem.
var (
	lookPath  = exec.LookPath
	statShell = os.Stat
)

// resolvedShell is a shell that discovery settled on.
type resolvedShell struct {
	// path is what gets exec'd — an absolute path when discovery found one,
	// otherwise a bare name resolved against PATH by os/exec.
	path string
	kind shellKind
}

// unknown reports whether the shell dialect could not be identified by name,
// in which case argv falls back to the POSIX `-c` convention.
func (s *resolvedShell) unknown() bool { return s.kind == shellKindUnknown }

// args builds the argv (excluding argv[0]) that hands command to the shell.
func (s *resolvedShell) args(command string) []string {
	switch s.kind {
	case shellKindPwsh, shellKindPowerShell:
		// Placeholder wiring so the Windows path is exercisable end to end.
		// The real PowerShell invocation (-EncodedCommand plus the
		// error-handling preamble) is a separate step; -Command mangles
		// quoting and does not propagate native exit codes reliably.
		return []string{"-NoProfile", "-NonInteractive", "-Command", command}
	default:
		return []string{"-c", command}
	}
}

// shellKindFor classifies a shell executable by basename, ignoring a Windows
// extension and case (PowerShell.EXE, /usr/bin/bash, C:\...\bash.exe → bash).
func shellKindFor(path string) shellKind {
	base := strings.ToLower(filepath.Base(path))
	// filepath.Base only splits on the host separator, so a Windows-style
	// path handed to a Unix build (or a config value typed with backslashes)
	// would keep its directory part. Trim both separators explicitly.
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".cmd")
	base = strings.TrimSuffix(base, ".bat")

	switch base {
	case "bash":
		return shellKindBash
	case "sh", "dash", "zsh", "ash":
		// Not bash, but POSIX enough for `-c` and reported as sh.
		return shellKindSh
	case "pwsh":
		return shellKindPwsh
	case "powershell":
		return shellKindPowerShell
	default:
		return shellKindUnknown
	}
}

// Auto-detection result cache. Detection walks PATH and (on Windows) the
// filesystem, so it is done once per process. An explicit override is never
// cached — config can change while the daemon runs.
var (
	shellCacheMu  sync.Mutex
	shellCache    *resolvedShell
	shellCacheErr error
)

// resetShellCache drops the memoized auto-detection result. Tests use it to
// keep injected lookPath stubs from leaking across cases.
func resetShellCache() {
	shellCacheMu.Lock()
	shellCache, shellCacheErr = nil, nil
	shellCacheMu.Unlock()
}

// resolveShell picks the shell to run bash-tool commands with.
//
// An explicit SHEPHERD_SHELL / config "shell" value wins and is taken at face
// value: it may be a bare name (resolved against PATH) or a full path. Only
// when no override is set does platform auto-detection run.
func resolveShell() (*resolvedShell, error) {
	if override := strings.TrimSpace(config.GetShell()); override != "" {
		return resolveShellOverride(override)
	}

	shellCacheMu.Lock()
	defer shellCacheMu.Unlock()
	if shellCache != nil || shellCacheErr != nil {
		return shellCache, shellCacheErr
	}
	shellCache, shellCacheErr = detectShell()
	return shellCache, shellCacheErr
}

// resolveShellOverride validates a user-supplied shell setting.
//
// The value is one executable — a name or a path, never a command line with
// flags. Flags are the platform generator's job (args), so accepting
// "pwsh -NoProfile" here would just reintroduce quoting bugs.
func resolveShellOverride(override string) (*resolvedShell, error) {
	// Paths pasted from Windows are often quoted; the quotes are not part of
	// the filename.
	override = strings.Trim(override, `"'`)

	// Spaces alone cannot mean "command line" — C:\Program Files\Git\bin\bash.exe
	// is the single most likely value on Windows. A trailing token that looks
	// like a flag is the actual giveaway.
	if fields := strings.Fields(override); len(fields) > 1 {
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "-") {
				return nil, fmt.Errorf(
					"invalid shell setting %q: expected a single shell executable path or name without arguments (they are added automatically for the detected shell); set SHEPHERD_SHELL or the %q config key to e.g. %q",
					override, "shell", fields[0])
			}
		}
	}

	path := override
	// A bare name is resolved now so a typo fails with a clear message here
	// rather than as an opaque exec error at command time. A value with a
	// separator is a path and is used as given.
	if !strings.ContainsAny(override, `/\`) {
		if p, err := lookPath(override); err == nil {
			path = p
		} else {
			return nil, fmt.Errorf("shell %q from SHEPHERD_SHELL/config is not on PATH: %w", override, err)
		}
	}

	return &resolvedShell{path: path, kind: shellKindFor(path)}, nil
}

// shellProc wraps the shell process so that platform-specific cleanup
// (process-group kill on Unix, taskkill/Job Object on Windows) can be swapped
// without changing call sites.
type shellProc struct {
	cmd     *exec.Cmd
	cleanup func() // nil means nothing to do
}

// kill tears down the shell and, where the platform supports it, its children.
func (p *shellProc) kill() {
	if p != nil && p.cleanup != nil {
		p.cleanup()
	}
}

// newShellCmd resolves the shell and builds the *exec.Cmd for command.
// Platform files wrap this in newShellProc to attach their cleanup strategy.
func newShellCmd(ctx context.Context, command, workdir string) (*exec.Cmd, error) {
	sh, err := resolveShell()
	if err != nil {
		return nil, err
	}
	if sh.unknown() {
		// Not fatal: `-c` is the common convention and the user asked for
		// this shell explicitly. Say so, because a shell that disagrees will
		// otherwise fail in confusing ways.
		fmt.Fprintf(os.Stderr, "shepherd: unrecognized shell %q — invoking it with the POSIX \"-c <command>\" convention\n", sh.path)
	}

	cmd := exec.CommandContext(ctx, sh.path, sh.args(command)...)
	cmd.Dir = workdir
	setupProcessGroup(cmd)
	return cmd, nil
}
