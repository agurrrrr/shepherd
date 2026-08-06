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

// Shell resolution for the shell tool (schema name "bash", plus aliases).
//
// The primary OpenAI tool name stays "bash" because many local models are
// trained to emit that name. On PowerShell hosts the tool *runs* PowerShell;
// loop.go gates (build verification, future-intention stall) use IsShellTool
// so silent aliases (shell/powershell/pwsh) still count as state-changing.
// What varies per OS is only *which* shell binary backs the tool and how a
// command string is handed to it — that lives here plus shell_unix.go /
// shell_windows.go.
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
	// shellKindCmd is used when the configured shell path ends in md.exe — a
	// marker/alias that means "drive the command through cmd.exe" rather than
	// exec'ing md.exe itself. The command is handed to cmd.exe as `/c <command>`.
	shellKindCmd shellKind = "cmd"
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

// isWslBash reports whether path is the Windows WSL shim
// (C:\Windows\System32\bash.exe), which launches a Linux distro and therefore
// sees a completely different filesystem than the Windows project path we pass
// as the working directory. Such a bash cannot run Windows-path commands, so
// auto-detection must skip it and fall through to Git Bash / PowerShell.
//
// It lives in the shared file (not shell_windows.go) purely so the path check
// is unit-testable on any host.
func isWslBash(path string) bool {
	lower := strings.ToLower(filepath.Clean(path))
	return strings.HasSuffix(lower, `system32\bash.exe`) ||
		strings.HasSuffix(lower, `system32/bash.exe`)
}

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
	case shellKindCmd:
		// The configured shell is md.exe (a marker) — the command is driven
		// through cmd.exe with `/c <command>`.
		return []string{"/c", command}, nil, nil
	default:
		return []string{"-c", command}, nil, nil
	}
}

// isPowerShell reports whether commands run through a PowerShell dialect,
// which changes the syntax the model has to write.
func (s *resolvedShell) isPowerShell() bool {
	return s.kind == shellKindPwsh || s.kind == shellKindPowerShell
}

// ShellUsesPowerShell reports whether the bash tool currently runs through a
// PowerShell dialect (pwsh or Windows PowerShell 5.1).
//
// Branch system prompts and recovery hints on this, not runtime.GOOS: Git Bash
// on Windows is still a POSIX dialect, which is why auto-detect prefers it.
// Callers that only have GOOS will mis-prompt Git Bash agents with PowerShell
// syntax (and the reverse when someone forces SHEPHERD_SHELL=pwsh on Unix).
func ShellUsesPowerShell() bool {
	sh, err := resolveShell()
	return err == nil && sh.isPowerShell()
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
	case "md":
		// md.exe is a marker shell: it is not exec'd itself. Commands are
		// routed through cmd.exe with `/c <command>`.
		return shellKindCmd
	case "cmd":
		// A real cmd.exe override. It is exec'd directly (unlike the md.exe
		// marker) and commands are handed to it with `/c <command>`.
		return shellKindCmd
	default:
		return shellKindUnknown
	}
}

// isCmdMarker reports whether path names the md.exe marker shell, which must
// not be exec'd itself — the command is routed through cmd.exe instead. It
// distinguishes the marker from a real cmd.exe override, which is exec'd
// directly.
func isCmdMarker(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".exe") == "md"
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

// shellToolNames are accepted names for the shell execution tool.
//
// "bash" is the schema-primary name (model training + historical). The rest
// are silent dispatch aliases so a local model that invents shell/powershell/
// pwsh after the user says "use PowerShell" still hits the same handler.
// Gates and progress tracking must use IsShellTool, not a bare string match.
var shellToolNames = map[string]bool{
	"bash":       true,
	"shell":      true,
	"powershell": true,
	"pwsh":       true,
}

// IsShellTool reports whether name is the shell execution tool (primary or alias).
func IsShellTool(name string) bool {
	return shellToolNames[name]
}

// shellToolAliases lists non-primary names registered on the dispatch map.
// They are not all advertised in the OpenAI tool list (that would bloat the
// schema); "shell" is additionally advertised when PowerShell is active so
// models that refuse a tool literally named "bash" still have a callable entry.
var shellToolAliases = []string{"shell", "powershell", "pwsh"}

// bashToolDescription is the description advertised for the primary "bash" tool.
//
// When the resolved shell is PowerShell the name "bash" is misleading and local
// models often refuse the tool ("instructions say PowerShell only, but the only
// shell tool is bash"). The description must resolve that contradiction: call
// this tool, write PowerShell syntax, do not invent a separate powershell tool.
func bashToolDescription() string {
	const base = "Execute a shell command in the project directory. Output is capped at 64KB."
	if sh, err := resolveShell(); err == nil && sh.isPowerShell() {
		return base +
			" IMPORTANT: despite the historical tool name \"bash\", this tool runs PowerShell " +
			"(pwsh or Windows PowerShell). You MUST call this tool (or \"shell\") for any shell " +
			"work — do not refuse it because the name says bash, and do not invent a separate " +
			"pwsh/powershell tool. Write PowerShell syntax in the command argument " +
			"(Get-ChildItem, Select-String, Get-Content; use ';' not '&&' on Windows PowerShell 5.1). " +
			"For file search prefer the native grep/glob tools instead of shell find/rg."
	}
	return base + " Prefer the native grep/glob tools for searching files instead of shell find/grep."
}

// shellToolDescription is the description for the extra "shell" tool entry
// advertised only when PowerShell backs the tool. Same handler as "bash".
func shellToolDescription() string {
	return "Execute a PowerShell command in the project directory (same backend as the bash tool). " +
		"Output is capped at 64KB. Write PowerShell syntax: Get-ChildItem, Select-String, " +
		"Get-Content; use ';' instead of '&&' on Windows PowerShell 5.1. " +
		"For file search prefer native grep/glob tools."
}

// shellCommandFromArgs pulls the command string from tool args, accepting
// common aliases local models invent (cmd/script) when they forget "command".
func shellCommandFromArgs(args map[string]interface{}) string {
	for _, key := range []string{"command", "cmd", "script", "code"} {
		if s, ok := args[key].(string); ok {
			if t := strings.TrimSpace(s); t != "" {
				return t
			}
		}
	}
	return ""
}

// powerShellDialectHint is appended to failed shell results when the backend
// is PowerShell, so a POSIX-habit command failure points the model at the
// right next step instead of retrying the same Unix command.
func powerShellDialectHint() string {
	return "\n\n[hint] This tool runs PowerShell, not Unix bash. " +
		"Use PowerShell cmdlets (Get-ChildItem, Select-String, Get-Content) and ';' for chaining. " +
		"For search/list files prefer the native grep and glob tools. " +
		"Call the tool named bash or shell — both execute PowerShell here."
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

	// For the md.exe marker the configured path must not be exec'd itself —
	// the command runs through cmd.exe instead. A real cmd.exe override is
	// exec'd directly.
	execPath := sh.path
	if sh.kind == shellKindCmd && isCmdMarker(sh.path) {
		execPath = "cmd.exe"
	}

	cmd := exec.CommandContext(ctx, execPath, args...)
	cmd.Dir = workdir
	setupProcessGroup(cmd)

	// CommandContext's default Cancel is Process.Kill, which only terminates
	// the shell. On cancel/timeout we need the whole tree (Unix process group,
	// Windows taskkill /T). Override Cancel so the primary kill path is ours;
	// execBash still calls proc.kill() after Run as an idempotent safety net
	// for the non-cancel error path. Dual kill is harmless — both platforms'
	// killProcessGroup tolerate an already-dead PID.
	//
	// P2 (Windows Job Object): when setupProcessGroup assigns the shell to a
	// job with KILL_ON_JOB_CLOSE, cleanup can close the job handle instead of
	// shelling out to taskkill; Cancel would keep calling the same cleanup.
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}
	return cmd, release, nil
}
