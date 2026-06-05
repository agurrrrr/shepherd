package envutil

import (
	"os"
	"os/exec"
	"strings"
)

// CleanEnv returns os.Environ() with CLAUDECODE and PWD removed.
//
// CLAUDECODE is stripped to prevent "nested session" errors when spawning the
// claude CLI from within a Claude Code session.
//
// PWD is stripped because Go's exec.Cmd.Dir changes the child's working
// directory via chdir but does NOT update the inherited PWD env var — it keeps
// pointing at the daemon's launch directory. Tools that resolve their project
// root from $PWD (e.g. opencode) would otherwise operate on the wrong
// directory. Callers should re-add PWD matching cmd.Dir; SetCleanEnv does this.
func CleanEnv() []string {
	env := os.Environ()
	cleaned := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDECODE=") || strings.HasPrefix(e, "PWD=") {
			continue
		}
		cleaned = append(cleaned, e)
	}
	return cleaned
}

// SetCleanEnv sets cmd.Env to CleanEnv() and pins PWD to the command's working
// directory so subprocesses that trust $PWD see the same path as the real
// chdir. cmd.Dir must be set before calling this; when empty, the current
// working directory is used.
func SetCleanEnv(cmd *exec.Cmd) {
	env := CleanEnv()
	dir := cmd.Dir
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	if dir != "" {
		env = append(env, "PWD="+dir)
	}
	cmd.Env = env
}
