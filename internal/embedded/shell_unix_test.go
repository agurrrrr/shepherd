//go:build !windows

package embedded

import (
	"strings"
	"testing"
)

func TestDetectShellUnixPrefersBash(t *testing.T) {
	stubLookPath(t, map[string]string{
		"bash": "/usr/bin/bash",
		"sh":   "/bin/sh",
	})

	sh, err := detectShell()
	if err != nil {
		t.Fatalf("detectShell: %v", err)
	}
	if sh.path != "/usr/bin/bash" || sh.kind != shellKindBash {
		t.Fatalf("got %+v, want /usr/bin/bash (bash)", sh)
	}
}

func TestDetectShellUnixFallsBackToSh(t *testing.T) {
	stubLookPath(t, map[string]string{"sh": "/bin/sh"})

	sh, err := detectShell()
	if err != nil {
		t.Fatalf("detectShell: %v", err)
	}
	if sh.path != "/bin/sh" || sh.kind != shellKindSh {
		t.Fatalf("got %+v, want /bin/sh (sh)", sh)
	}
}

// The old code produced `exec: "bash": executable file not found in $PATH`,
// which tells the user nothing about what to do. The replacement has to name
// the fix.
func TestDetectShellUnixNoneFound(t *testing.T) {
	stubLookPath(t, nil)

	_, err := detectShell()
	if err == nil {
		t.Fatal("expected an error when no shell exists")
	}
	msg := err.Error()
	for _, want := range []string{"no supported shell found", "bash", "SHEPHERD_SHELL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message should mention %q, got: %s", want, msg)
		}
	}
}

// Parity check for the refactor: execBash must still run the command in the
// project directory, return stdout on success, and format a non-zero exit the
// same way it did when it called exec.Command("bash", "-c", ...) directly.
func TestExecBashUnixBehavior(t *testing.T) {
	resetShellCache()
	t.Cleanup(resetShellCache)

	dir := t.TempDir()
	tr := &ToolRegistry{projectPath: dir}

	out, err := tr.execBash(t.Context(), map[string]interface{}{"command": "echo hello"})
	if err != nil {
		t.Fatalf("execBash: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("stdout = %q, want %q", out, "hello")
	}

	out, err = tr.execBash(t.Context(), map[string]interface{}{"command": "pwd"})
	if err != nil {
		t.Fatalf("execBash: %v", err)
	}
	// macOS resolves TempDir through /private, so compare the tail.
	if !strings.HasSuffix(strings.TrimSpace(out), strings.TrimPrefix(dir, "/private")) {
		t.Errorf("cwd = %q, want the project path %q", strings.TrimSpace(out), dir)
	}

	out, err = tr.execBash(t.Context(), map[string]interface{}{"command": "echo boom >&2; exit 3"})
	if err != nil {
		t.Fatalf("execBash returned an error for a failing command; failures belong in the output: %v", err)
	}
	if !strings.Contains(out, "exit 3") || !strings.Contains(out, "boom") {
		t.Errorf("failure output = %q, want it to carry the exit code and stderr", out)
	}
}
