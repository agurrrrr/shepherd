package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/agurrrrr/shepherd/internal/config"
)

// TestMain points config writes at a throwaway file. Tests here call config.Init()
// and config.Set(), and config.Set rewrites the entire file from viper's state —
// so without this guard a test run overwrites the operator's real
// ~/.shepherd/config.yaml (auth credentials included). Same guard as
// internal/server/testmain_test.go.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "shepherd-worker-config-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testmain: mkdir temp config:", err)
		os.Exit(1)
	}
	config.UseTempConfigFile(filepath.Join(dir, "config.yaml"))

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
