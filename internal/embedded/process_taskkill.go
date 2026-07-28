package embedded

import (
	"errors"
	"os/exec"
	"strings"
)

// taskkillAlreadyGone reports whether taskkill failed only because the process
// (tree) was already gone. Common cases: exit 128, "not found" in the message.
//
// Lives in a non-tagged file so the classification logic is unit-tested on
// Unix CI as well as Windows; only killTreeWithTaskkill is Windows-only.
func taskkillAlreadyGone(err error, out []byte) bool {
	msg := strings.ToLower(string(out))
	if strings.Contains(msg, "not found") {
		return true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 128 {
		return true
	}
	return false
}

// isExecNotFound reports a missing binary (taskkill not on PATH).
func isExecNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	// Older Go / wrapped LookPath errors surface this substring.
	return strings.Contains(err.Error(), "executable file not found")
}
