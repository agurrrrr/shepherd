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

// invocation builds the argv (excluding argv[0]) that hands command to the
// shell, plus a release func for anything the invocation had to allocate
// (nil when there is nothing to free).
//
// The release func must run on every exit path, not just the error one — see
// shellProc.close.
func (s *resolvedShell) invocation(command string) (args []string, release func(), err error) {
	switch s.kind {
	case shellKindPwsh, shellKindPowerShell:
		// -EncodedCommand plus an error/exit preamble; see shell_powershell.go
		// for why -Command is not usable here.
		return psInvocation(command)
	default:
		return []string{"-c", command}, nil, nil
	}
}

// isPowerShell reports whether commands run through a PowerShell dialect,
// which changes the syntax the model has to write.
func (s *resolvedShell) isPowerShell() bool {
	return s.kind == shellKindPwsh || s.kind == shellKindPowerShell
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

// bashToolDescription is the description advertised for the bash tool.
//
// The tool name stays "bash" everywhere (loop.go gates on `case "bash"`), so
// when the resolved shell is PowerShell the only way the model learns it is
// not writing POSIX is this line. A fuller Windows-aware prompt is a separate
// step; without at least this much, the PowerShell path works while every
// command the agent writes still fails.
func bashToolDescription() string {
	const base = "Execute a shell command in the project directory. Output is capped at 64KB."
	if sh, err := resolveShell(); err == nil && sh.isPowerShell() {
		return base + " Shell is PowerShell: use ';' instead of '&&' (Windows PowerShell 5.1 has no '&&'), and Windows-style paths."
	}
	return base
}

// shellProc wraps the shell process so that platform-specific cleanup
// (process-group kill on Unix, taskkill/Job Object on Windows) can be swapped
// without changing call sites.
//
// The two teardown paths are deliberately distinct. cleanup kills the process
// tree and only makes sense when the command did *not* finish on its own;
// release frees resources the invocation allocated (the temp .ps1 that the
// PowerShell path spills long commands into) and must run whatever happened.
type shellProc struct {
	cmd     *exec.Cmd
	cleanup func() // nil means nothing to do
	release func() // nil means nothing to free
	closed  sync.Once
}

// kill tears down the shell and, where the platform supports it, its children.
func (p *shellProc) kill() {
	if p != nil && p.cleanup != nil {
		p.cleanup()
	}
}

// close frees the invocation's resources. It is idempotent and belongs in a
// defer at the call site so success, failure and timeout all reach it — a
// leaked temp script is invisible until the temp directory fills up.
func (p *shellProc) close() {
	if p == nil || p.release == nil {
		return
	}
	p.closed.Do(p.release)
}

// newShellCmd resolves the shell and builds the *exec.Cmd for command, along
// with the invocation's release func (see shellProc.close). Platform files
// wrap this in newShellProc to attach their cleanup strategy.
func newShellCmd(ctx context.Context, command, workdir string) (*exec.Cmd, func(), error) {
	sh, err := resolveShell()
	if err != nil {
		return nil, nil, err
	}
	if sh.unknown() {
		// Not fatal: `-c` is the common convention and the user asked for
		// this shell explicitly. Say so, because a shell that disagrees will
		// otherwise fail in confusing ways.
		fmt.Fprintf(os.Stderr, "shepherd: unrecognized shell %q — invoking it with the POSIX \"-c <command>\" convention\n", sh.path)
	}

	args, release, err := sh.invocation(command)
	if err != nil {
		return nil, nil, err
	}

	cmd := exec.CommandContext(ctx, sh.path, args...)
	cmd.Dir = workdir
	setupProcessGroup(cmd)
	return cmd, release, nil
}
