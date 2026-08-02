package procutil

import (
	"errors"
	"os/exec"
	"strings"
)

// TaskkillAlreadyGone reports whether taskkill failed only because the process
// (tree) was already gone. Common cases: exit 128, "not found" in the message.
//
// Lives in a non-tagged file so the classification logic is unit-tested on
// Unix CI as well as Windows; only KillTree is Windows-only.
func TaskkillAlreadyGone(err error, out []byte) bool {
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

// IsExecNotFound reports a missing binary (taskkill not on PATH).
func IsExecNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	// Older Go / wrapped LookPath errors surface this substring.
	return strings.Contains(err.Error(), "executable file not found")
}
