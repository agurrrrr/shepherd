package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/i18n"
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

	// Pin the display language: handlers render stored codes through i18n
	// (#665), so assertions must not depend on test execution order.
	i18n.Init("ko")

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
