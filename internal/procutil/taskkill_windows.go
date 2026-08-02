//go:build windows

// Package procutil holds Windows process-tree helpers shared by the embedded
// shell runner and the worker package. It exists to break the duplication
// where embedded killed whole trees with taskkill /T while worker only
// killed the direct child and leaked grandchildren (windows_tool_bugs B3).
package procutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// taskkillTimeout bounds how long we wait on taskkill itself. An unbounded
// wait would block the cancel/timeout path that is trying to free resources.
const taskkillTimeout = 5 * time.Second

// KillTree terminates the process identified by pid and all its descendants.
//
// Process.Kill alone only kills the named process; children (go build, npm,
// gradlew, claude→node, …) survive as orphans after every timeout/cancel.
// taskkill /T /F walks the process tree. Returns true when the tree is gone
// (or was already gone); false when the caller should fall back to
// Process.Kill (taskkill missing, timed out, or failed unexpectedly).
//
// Idempotent: a second call on a dead PID is a no-op.
func KillTree(pid int) bool {
	if pid <= 0 {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), taskkillTimeout)
	defer cancel()

	// Direct exec — never re-enter any shell resolution path just to run taskkill.
	c := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	out, err := c.CombinedOutput()
	if err == nil {
		return true
	}
	if TaskkillAlreadyGone(err, out) {
		return true
	}

	// Unexpected failure only — log and let the caller fall back.
	msg := strings.TrimSpace(string(out))
	switch {
	case ctx.Err() != nil:
		fmt.Fprintf(os.Stderr, "shepherd: taskkill timed out for pid %d: %v\n", pid, err)
	case IsExecNotFound(err):
		// taskkill missing from PATH — silent fallback to Process.Kill.
	default:
		if msg != "" {
			fmt.Fprintf(os.Stderr, "shepherd: taskkill failed for pid %d: %v (%s)\n", pid, err, msg)
		} else {
			fmt.Fprintf(os.Stderr, "shepherd: taskkill failed for pid %d: %v\n", pid, err)
		}
	}
	return false
}
