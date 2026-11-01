package mcp

import (
	"os"
	"testing"
)

// TestMain clears SHEPHERD_TASK_ID for the whole package. `go test` run from
// inside a shepherd task inherits the variable, and callerTaskIDOrEnv then
// reports that task as the caller of every handler call: handlers tested
// without a DB panic in the autonomy gate's lookup, and caller-aware checks
// (#597 auto-watch, #722 task_complete guard) see a caller the test never
// set. Tests that need an env caller set it with t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv("SHEPHERD_TASK_ID")
	os.Exit(m.Run())
}
