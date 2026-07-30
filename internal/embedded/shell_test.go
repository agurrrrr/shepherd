package embedded

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// stubLookPath replaces the package-level lookPath for the duration of a test
// and clears the auto-detection cache on both ends, so a stub can never leak
// into another case (or into a real detection result).
func stubLookPath(t *testing.T, found map[string]string) {
	t.Helper()
	orig := lookPath
	resetShellCache()
	lookPath = func(name string) (string, error) {
		if path, ok := found[name]; ok {
			return path, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() {
		lookPath = orig
		resetShellCache()
	})
}

// setShellConfig points the "shell" config key at value for one test. It also
// clears SHEPHERD_SHELL so a developer's own environment cannot decide the
// outcome of a config-level assertion.
func setShellConfig(t *testing.T, value string) {
	t.Helper()
	t.Setenv("SHEPHERD_SHELL", "")
	orig := viper.GetString("shell")
	viper.Set("shell", value)
	t.Cleanup(func() { viper.Set("shell", orig) })
}

func TestShellKindFor(t *testing.T) {
	cases := []struct {
		path string
		want shellKind
	}{
		{"bash", shellKindBash},
		{"/usr/bin/bash", shellKindBash},
		{`C:\Program Files\Git\bin\bash.exe`, shellKindBash},
		{`C:\Program Files\Git\bin\BASH.EXE`, shellKindBash},
		{"/bin/sh", shellKindSh},
		{"/usr/bin/zsh", shellKindSh},
		{"pwsh", shellKindPwsh},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, shellKindPwsh},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.EXE`, shellKindPowerShell},
		{"/usr/bin/fish", shellKindUnknown},
	}
	for _, c := range cases {
		if got := shellKindFor(c.path); got != c.want {
			t.Errorf("shellKindFor(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestShellInvocationPOSIX(t *testing.T) {
	cases := []shellKind{
		shellKindBash,
		shellKindSh,
		// Unrecognized shells fall back to the POSIX convention.
		shellKindUnknown,
	}
	want := []string{"-c", "ls -la"}
	for _, kind := range cases {
		sh := &resolvedShell{kind: kind}
		got, release, err := sh.invocation("ls -la")
		if err != nil {
			t.Fatalf("kind %q invocation: %v", kind, err)
		}
		if release != nil {
			t.Errorf("kind %q allocated a release func; POSIX shells own nothing", kind)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("kind %q args = %q, want %q", kind, got, want)
		}
	}
}

// Both PowerShell dialects route through the encoded-command path.
func TestShellInvocationPowerShell(t *testing.T) {
	for _, kind := range []shellKind{shellKindPwsh, shellKindPowerShell} {
		sh := &resolvedShell{kind: kind}
		got, release, err := sh.invocation("ls -la")
		if err != nil {
			t.Fatalf("kind %q invocation: %v", kind, err)
		}
		if release != nil {
			t.Errorf("kind %q spilled to a temp file for a short command", kind)
		}
		want := []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodeUTF16LEBase64(psScript("ls -la"))}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("kind %q args = %q, want %q", kind, got, want)
		}
	}
}

// The override is the user's escape hatch when auto-detection guesses wrong,
// so it must beat detection even when a perfectly good shell is on PATH.
func TestResolveShellOverrideBeatsDetection(t *testing.T) {
	stubLookPath(t, map[string]string{
		"bash": "/usr/bin/bash",
		"pwsh": "/opt/pwsh",
	})
	setShellConfig(t, "pwsh")

	sh, err := resolveShell()
	if err != nil {
		t.Fatalf("resolveShell: %v", err)
	}
	if sh.path != "/opt/pwsh" || sh.kind != shellKindPwsh {
		t.Fatalf("got %+v, want /opt/pwsh (pwsh)", sh)
	}
}

func TestResolveShellEnvBeatsConfig(t *testing.T) {
	stubLookPath(t, map[string]string{
		"bash": "/usr/bin/bash",
		"pwsh": "/opt/pwsh",
		"sh":   "/bin/sh",
	})
	setShellConfig(t, "pwsh")
	t.Setenv("SHEPHERD_SHELL", "sh")

	sh, err := resolveShell()
	if err != nil {
		t.Fatalf("resolveShell: %v", err)
	}
	if sh.path != "/bin/sh" {
		t.Fatalf("got %q, want /bin/sh (SHEPHERD_SHELL must win over config)", sh.path)
	}
}

// An override holding a path is used verbatim — no PATH lookup, since the
// whole point is reaching a shell that PATH does not expose.
func TestResolveShellOverrideWithPath(t *testing.T) {
	stubLookPath(t, nil) // nothing on PATH at all
	setShellConfig(t, `C:\Program Files\Git\bin\bash.exe`)

	sh, err := resolveShell()
	if err != nil {
		t.Fatalf("resolveShell: %v", err)
	}
	if sh.path != `C:\Program Files\Git\bin\bash.exe` || sh.kind != shellKindBash {
		t.Fatalf("got %+v, want the configured path classified as bash", sh)
	}
}

func TestResolveShellOverrideNotOnPath(t *testing.T) {
	stubLookPath(t, map[string]string{"bash": "/usr/bin/bash"})
	setShellConfig(t, "nosuchshell")

	_, err := resolveShell()
	if err == nil {
		t.Fatal("expected an error for an override that is not on PATH")
	}
	if !strings.Contains(err.Error(), "nosuchshell") {
		t.Errorf("error should name the bad value, got: %v", err)
	}
}

// "pwsh -NoProfile" is the tempting-but-wrong way to configure this: arguments
// belong to the platform generator, so a value with flags is rejected loudly
// rather than exec'd as a filename containing a space.
func TestResolveShellOverrideRejectsArguments(t *testing.T) {
	stubLookPath(t, map[string]string{"pwsh": "/opt/pwsh"})
	setShellConfig(t, "pwsh -NoProfile")

	_, err := resolveShell()
	if err == nil {
		t.Fatal("expected an error for an override containing arguments")
	}
	if !strings.Contains(err.Error(), "single shell executable") {
		t.Errorf("error should explain the expected format, got: %v", err)
	}
}

// A Windows install path contains a space and must not be mistaken for a
// command line — this is the most likely value the setting will ever hold.
func TestResolveShellOverridePathWithSpacesAndQuotes(t *testing.T) {
	stubLookPath(t, nil)
	setShellConfig(t, `"C:\Program Files\Git\bin\bash.exe"`)

	sh, err := resolveShell()
	if err != nil {
		t.Fatalf("resolveShell: %v", err)
	}
	if sh.path != `C:\Program Files\Git\bin\bash.exe` {
		t.Fatalf("got %q, want the unquoted path", sh.path)
	}
}

// Detection is memoized, but an override must not be — the config can change
// while the daemon is running.
func TestResolveShellOverrideIsNotCached(t *testing.T) {
	stubLookPath(t, map[string]string{
		"bash": "/usr/bin/bash",
		"sh":   "/bin/sh",
	})

	setShellConfig(t, "bash")
	first, err := resolveShell()
	if err != nil {
		t.Fatalf("resolveShell: %v", err)
	}

	setShellConfig(t, "sh")
	second, err := resolveShell()
	if err != nil {
		t.Fatalf("resolveShell: %v", err)
	}

	if first.path == second.path {
		t.Fatalf("override change was not picked up: both resolved to %q", first.path)
	}
	if second.path != "/bin/sh" {
		t.Fatalf("got %q, want /bin/sh after the config changed", second.path)
	}
}

func TestNewShellCmdUsesResolvedShell(t *testing.T) {
	stubLookPath(t, map[string]string{"bash": "/usr/bin/bash"})
	setShellConfig(t, "")

	cmd, release, err := newShellCmd(t.Context(), "echo hi", "/tmp")
	if err != nil {
		t.Fatalf("newShellCmd: %v", err)
	}
	if release != nil {
		t.Error("bash invocation should own no resources")
	}
	if cmd.Dir != "/tmp" {
		t.Errorf("Dir = %q, want /tmp", cmd.Dir)
	}
	want := []string{"/usr/bin/bash", "-c", "echo hi"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("Args = %q, want %q", cmd.Args, want)
	}
}

func TestShellToolAliasesRegistered(t *testing.T) {
	tr := NewToolRegistry(t.TempDir(), "test", nil, nil)
	for _, name := range []string{"bash", "shell", "powershell", "pwsh"} {
		if _, ok := tr.nativeTools[name]; !ok {
			t.Errorf("native tool %q not registered", name)
		}
	}
}

func TestOpenAIToolDefsAdvertisesShellOnPowerShell(t *testing.T) {
	setShellConfig(t, "pwsh")
	stubLookPath(t, map[string]string{"pwsh": "/opt/pwsh"})
	if !ShellUsesPowerShell() {
		t.Fatal("expected PowerShell dialect")
	}
	tr := NewToolRegistry(t.TempDir(), "test", nil, nil)
	defs := tr.OpenAIToolDefs()
	var hasBash, hasShell bool
	for _, d := range defs {
		switch d.Function.Name {
		case "bash":
			hasBash = true
			if !strings.Contains(d.Function.Description, "PowerShell") {
				t.Errorf("bash description on PowerShell missing dialect: %q", d.Function.Description)
			}
		case "shell":
			hasShell = true
		}
	}
	if !hasBash {
		t.Error("bash tool must still be advertised")
	}
	if !hasShell {
		t.Error("shell tool must be advertised when PowerShell backs the tool")
	}

	// POSIX host: only bash, not shell.
	setShellConfig(t, "bash")
	stubLookPath(t, map[string]string{"bash": "/usr/bin/bash"})
	// resolveShellOverride caches nothing for overrides, but ShellUsesPowerShell
	// re-resolves each time via config — still clear lookPath.
	tr2 := NewToolRegistry(t.TempDir(), "test", nil, nil)
	for _, d := range tr2.OpenAIToolDefs() {
		if d.Function.Name == "shell" {
			t.Error("shell must not be advertised when backend is bash")
		}
	}
}
