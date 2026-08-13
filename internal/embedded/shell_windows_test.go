//go:build windows

package embedded

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubStatShell makes statShell succeed only for the given path, backed by a
// real file so the IsDir() check sees a genuine FileInfo.
func stubStatShell(t *testing.T, existing string) {
	t.Helper()
	orig := statShell
	real := filepath.Join(t.TempDir(), "bash.exe")
	if err := os.WriteFile(real, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	statShell = func(path string) (os.FileInfo, error) {
		if path == existing {
			return os.Stat(real)
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { statShell = orig })
}

// Git Bash wins over PowerShell even when both exist: the tool is named "bash"
// and the model writes POSIX commands, so a PowerShell-first order would leave
// the tool working and the agent failing.
func TestDetectShellWindowsPrefersBashOverPowerShell(t *testing.T) {
	stubLookPath(t, map[string]string{
		"bash":           `C:\Program Files\Git\bin\bash.exe`,
		"pwsh.exe":       `C:\Program Files\PowerShell\7\pwsh.exe`,
		"powershell.exe": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
	})
	stubStatShell(t, "")

	sh, err := detectShell()
	if err != nil {
		t.Fatalf("detectShell: %v", err)
	}
	if sh.kind != shellKindBash {
		t.Fatalf("got %+v, want Git Bash", sh)
	}
}

// Git's installer commonly leaves bin\ off PATH, so the conventional install
// locations are checked before falling through to PowerShell.
func TestDetectShellWindowsGitBashFallbackPath(t *testing.T) {
	stubLookPath(t, map[string]string{
		"pwsh.exe": `C:\Program Files\PowerShell\7\pwsh.exe`,
	})
	stubStatShell(t, gitBashFallbacks[0])

	sh, err := detectShell()
	if err != nil {
		t.Fatalf("detectShell: %v", err)
	}
	if sh.path != gitBashFallbacks[0] || sh.kind != shellKindBash {
		t.Fatalf("got %+v, want %q", sh, gitBashFallbacks[0])
	}
}

func TestDetectShellWindowsPrefersPwshOverPowerShell(t *testing.T) {
	stubLookPath(t, map[string]string{
		"pwsh.exe":       `C:\Program Files\PowerShell\7\pwsh.exe`,
		"powershell.exe": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
	})
	stubStatShell(t, "")

	sh, err := detectShell()
	if err != nil {
		t.Fatalf("detectShell: %v", err)
	}
	if sh.kind != shellKindPwsh {
		t.Fatalf("got %+v, want pwsh", sh)
	}
}

// cmd.exe is always present on Windows and is deliberately not a candidate:
// silently degrading to a third dialect costs more than a clear failure.
func TestDetectShellWindowsNeverFallsBackToCmd(t *testing.T) {
	stubLookPath(t, map[string]string{
		"cmd":     `C:\Windows\System32\cmd.exe`,
		"cmd.exe": `C:\Windows\System32\cmd.exe`,
	})
	stubStatShell(t, "")

	_, err := detectShell()
	if err == nil {
		t.Fatal("expected an error rather than a silent fallback to cmd.exe")
	}
	msg := err.Error()
	for _, want := range []string{"no supported shell found", "Git for Windows", "pwsh", "SHEPHERD_SHELL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message should mention %q, got: %s", want, msg)
		}
	}
}

// WSL's System32\bash.exe is on PATH whenever WSL is installed. It launches a
// Linux distro that cannot see the Windows working directory, so auto-detect
// must skip it and fall through to Git Bash / PowerShell.
func TestDetectShellWindowsSkipsWslBash(t *testing.T) {
	stubLookPath(t, map[string]string{
		"bash":     `C:\Windows\System32\bash.exe`,
		"pwsh.exe": `C:\Program Files\PowerShell\7\pwsh.exe`,
	})
	stubStatShell(t, "")

	sh, err := detectShell()
	if err != nil {
		t.Fatalf("detectShell: %v", err)
	}
	if sh.kind != shellKindPwsh {
		t.Fatalf("got %+v, want pwsh (WSL bash skipped)", sh)
	}
}

// When only the WSL shim provides "bash", auto-detect should still prefer a
// real Git Bash at its conventional install path over PowerShell.
func TestDetectShellWindowsWslBashThenGitBashFallback(t *testing.T) {
	stubLookPath(t, map[string]string{
		"bash": `C:\Windows\System32\bash.exe`,
	})
	stubStatShell(t, gitBashFallbacks[0])

	sh, err := detectShell()
	if err != nil {
		t.Fatalf("detectShell: %v", err)
	}
	if sh.path != gitBashFallbacks[0] || sh.kind != shellKindBash {
		t.Fatalf("got %+v, want Git Bash %q", sh, gitBashFallbacks[0])
	}
}

// CmdLine must be the literal `cmd.exe /c "<command>"`. If we left quoting
// to Go's EscapeArg, wrapping quotes would become \" and inner double
// quotes would not survive cmd.exe /C rule 2.
func TestNewShellCmdSetsCmdLineForMdShell(t *testing.T) {
	setShellConfig(t, `C:\Users\me\md.exe`)
	stubLookPath(t, nil)

	cmd, _, err := newShellCmd(t.Context(), `echo "hello"`, `C:\tmp`)
	if err != nil {
		t.Fatalf("newShellCmd: %v", err)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CmdLine == "" {
		t.Fatal("expected SysProcAttr.CmdLine so Go EscapeArg does not rewrite quotes")
	}
	want := `cmd.exe /c "echo "hello""`
	if cmd.SysProcAttr.CmdLine != want {
		t.Errorf("CmdLine = %q, want %q", cmd.SysProcAttr.CmdLine, want)
	}
}
