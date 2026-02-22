package envutil

import (
	"os"
	"os/exec"
	"strings"
)

// CleanEnv returns os.Environ() with CLAUDECODE removed.
// This prevents "nested session" errors when spawning claude CLI subprocesses
// from within a Claude Code session.
func CleanEnv() []string {
	env := os.Environ()
	cleaned := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			cleaned = append(cleaned, e)
		}
	}
	return cleaned
}

// SetCleanEnv sets cmd.Env to os.Environ() with CLAUDECODE removed.
func SetCleanEnv(cmd *exec.Cmd) {
	cmd.Env = CleanEnv()
}
