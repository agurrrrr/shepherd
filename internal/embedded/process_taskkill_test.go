package embedded

import (
	"errors"
	"os/exec"
	"testing"
)

func TestTaskkillAlreadyGone(t *testing.T) {
	// Real taskkill prints "not found" when the PID is already gone.
	if !taskkillAlreadyGone(errors.New("exit status 128"), []byte(`ERROR: The process "1234" not found.`)) {
		t.Fatal("expected not-found message to count as already gone")
	}
	if taskkillAlreadyGone(errors.New("access denied"), []byte(`ERROR: Access is denied.`)) {
		t.Fatal("access denied must not be treated as already gone")
	}
	if taskkillAlreadyGone(errors.New("exit status 1"), nil) {
		t.Fatal("plain exit 1 with empty output must not count as already gone")
	}
}

func TestIsExecNotFound(t *testing.T) {
	if !isExecNotFound(exec.ErrNotFound) {
		t.Fatal("exec.ErrNotFound should match")
	}
	if !isExecNotFound(errors.New(`exec: "taskkill": executable file not found in %PATH%`)) {
		t.Fatal("LookPath-style error should match")
	}
	if isExecNotFound(errors.New("exit status 1")) {
		t.Fatal("ordinary exit must not look like missing binary")
	}
	if isExecNotFound(nil) {
		t.Fatal("nil must not match")
	}
}
