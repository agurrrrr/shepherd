package envutil

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	val, ok := "", false
	// Last occurrence wins, matching getenv semantics.
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			val, ok = strings.TrimPrefix(e, prefix), true
		}
	}
	return val, ok
}

func TestCleanEnvStripsClaudeCodeAndPWD(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("PWD", "/some/stale/dir")

	env := CleanEnv()
	if _, ok := envValue(env, "CLAUDECODE"); ok {
		t.Errorf("CLAUDECODE should be stripped")
	}
	if _, ok := envValue(env, "PWD"); ok {
		t.Errorf("PWD should be stripped")
	}
}

func TestSetCleanEnvPinsPWDToCmdDir(t *testing.T) {
	// Simulate the daemon's stale PWD pointing at its launch directory.
	t.Setenv("PWD", "/home/daemon/launchdir")

	cmd := exec.Command("true")
	cmd.Dir = "/home/agurrrrr/code/drop-the-codes"
	SetCleanEnv(cmd)

	pwd, ok := envValue(cmd.Env, "PWD")
	if !ok {
		t.Fatalf("PWD should be set")
	}
	if pwd != cmd.Dir {
		t.Errorf("PWD = %q, want %q (the real working dir, not the stale daemon PWD)", pwd, cmd.Dir)
	}
}

func TestSetCleanEnvFallsBackToWd(t *testing.T) {
	cmd := exec.Command("true") // no Dir set
	SetCleanEnv(cmd)

	wd, _ := os.Getwd()
	pwd, ok := envValue(cmd.Env, "PWD")
	if !ok {
		t.Fatalf("PWD should be set")
	}
	if pwd != wd {
		t.Errorf("PWD = %q, want current working dir %q", pwd, wd)
	}
}
