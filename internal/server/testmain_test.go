package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/agurrrrr/shepherd/internal/config"
)

// TestMain redirects every config write to a throwaway file. Handlers here save
// settings through viper, and viper.WriteConfigAs rewrites the whole file — so a
// handler test that resets viper would truncate the operator's real
// ~/.shepherd/config.yaml (auth credentials included). This happened once; the
// guard lives here so new handler tests are safe by default instead of having to
// remember to stub persistConfig individually.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "shepherd-config-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testmain: mkdir temp config:", err)
		os.Exit(1)
	}
	config.UseTempConfigFile(filepath.Join(dir, "config.yaml"))

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
