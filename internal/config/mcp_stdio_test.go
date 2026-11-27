package config

import (
	"os"
	"path/filepath"
	"testing"
)

// useEmbeddedYaml points the embedded config file at a temp dir for one test and
// restores the previous dir, so a case cannot leak into the next one.
func useEmbeddedYaml(t *testing.T, body string) {
	t.Helper()
	prev := UseTempConfigDir(t.TempDir())
	t.Cleanup(func() { UseTempConfigDir(prev) })
	if body != "" {
		if err := os.WriteFile(filepath.Join(EmbeddedConfigFile()), []byte(body), 0o600); err != nil {
			t.Fatalf("write embedded.yaml: %v", err)
		}
	}
}

// TestGetMCPStdioTimeoutSecondsDefault pins the new default at 600 seconds and
// confirms a missing/empty mcp section falls back to it (an absent section must
// still yield a working timeout, not zero).
func TestGetMCPStdioTimeoutSecondsDefault(t *testing.T) {
	useEmbeddedYaml(t, "endpoints: []\n")
	if got := GetMCPStdioTimeoutSeconds(); got != 600 {
		t.Fatalf("default stdio timeout = %d, want 600", got)
	}

	// No mcp section at all still defaults.
	useEmbeddedYaml(t, "")
	if got := GetMCPStdioTimeoutSeconds(); got != 600 {
		t.Fatalf("no-section stdio timeout = %d, want 600", got)
	}
}

// TestGetMCPStdioTimeoutSecondsReadsAndGuards verifies a configured value is
// read verbatim, while zero/negative/above-ceiling values in embedded.yaml fall
// back to the default rather than producing an unusable timeout.
func TestGetMCPStdioTimeoutSecondsReadsAndGuards(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"configured 120", "mcp:\n  stdio_timeout_seconds: 120\n", 120},
		{"zero falls back", "mcp:\n  stdio_timeout_seconds: 0\n", 600},
		{"negative falls back", "mcp:\n  stdio_timeout_seconds: -5\n", 600},
		{"above ceil falls back", "mcp:\n  stdio_timeout_seconds: 99999\n", 600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useEmbeddedYaml(t, tc.body)
			if got := GetMCPStdioTimeoutSeconds(); got != tc.want {
				t.Fatalf("stdio timeout = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSaveMCPConfigPreservesEndpoints checks that saving the mcp section from
// the settings form does not clobber the endpoints list (read-modify-write, same
// contract as SaveMagiConfig).
func TestSaveMCPConfigPreservesEndpoints(t *testing.T) {
	useEmbeddedYaml(t, ""+
		"endpoints:\n"+
		"  - id: keep-me\n"+
		"    model: m\n"+
		"    enabled: true\n")

	if err := SaveMCPConfig(&MCPStdioConfig{StdioTimeoutSeconds: 300}); err != nil {
		t.Fatalf("SaveMCPConfig: %v", err)
	}

	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(cfg.Endpoints) != 1 || cfg.Endpoints[0].ID != "keep-me" {
		t.Fatalf("endpoints were clobbered: %+v", cfg.Endpoints)
	}
	if cfg.MCP == nil || cfg.MCP.StdioTimeoutSeconds != 300 {
		t.Fatalf("mcp section not saved: %+v", cfg.MCP)
	}
	if got := GetMCPStdioTimeoutSeconds(); got != 300 {
		t.Fatalf("GetMCPStdioTimeoutSeconds after save = %d, want 300", got)
	}
}

// TestNormalizeMCPStdioTimeoutSeconds pins the accepted/rejected forms: integer
// seconds, duration strings, and rejection of empty/zero/negative/unparseable/
// above-ceiling input so a typo cannot silently become "no timeout".
func TestNormalizeMCPStdioTimeoutSeconds(t *testing.T) {
	ok := []struct {
		in   interface{}
		want int
	}{
		{300, 300},
		{"300", 300},
		{"5m", 300},
		{"1h", 3600},
	}
	for _, c := range ok {
		got, err := NormalizeMCPStdioTimeoutSeconds(c.in)
		if err != nil {
			t.Errorf("NormalizeMCPStdioTimeoutSeconds(%v) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeMCPStdioTimeoutSeconds(%v) = %d, want %d", c.in, got, c.want)
		}
	}

	bad := []interface{}{"", "  ", 0, -1, "0", "-5", "abc", "5x", 99999, "2h"}
	for _, in := range bad {
		if got, err := NormalizeMCPStdioTimeoutSeconds(in); err == nil {
			t.Errorf("NormalizeMCPStdioTimeoutSeconds(%v) = %d, want an error", in, got)
		}
	}
}

// TestToolDispatchWatchdogFollowsMCPStdioTimeout checks the embedded loop's
// per-tool watchdog is never shorter than the MCP stdio read timeout plus a
// margin, so a long MCP call reports its own readable timeout instead of dying
// at the generic loop watchdog. With stdio at 600 and bash at the default 600,
// the watchdog must be at least 630s.
func TestToolDispatchWatchdogFollowsMCPStdioTimeout(t *testing.T) {
	useEmbeddedYaml(t, "mcp:\n  stdio_timeout_seconds: 600\n")
	td := GetToolDispatchTimeout()
	if td.Seconds() < 630 {
		t.Fatalf("tool dispatch watchdog = %s, want >= 630s", td)
	}
}
