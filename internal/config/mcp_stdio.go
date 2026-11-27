package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MCPStdioConfig is the `mcp:` section of embedded.yaml. It holds transport
// knobs for external MCP servers that shepherd spawns and talks to over stdio.
type MCPStdioConfig struct {
	// StdioTimeoutSeconds is how long one external MCP server may take to
	// answer a single JSON-RPC request (initialize, tools/list, tools/call).
	// 0 = unset → DefaultMCPStdioTimeoutSeconds (600).
	StdioTimeoutSeconds int `mapstructure:"stdio_timeout_seconds" json:"stdio_timeout_seconds" yaml:"stdio_timeout_seconds,omitempty"`
}

// GetMCPConfig loads the mcp section from embedded.yaml with defaults applied.
// Returns a defaults-only config when the section is absent — unlike magi, a
// missing section here still has meaningful values (the stdio read timeout),
// so callers never need a nil check.
func GetMCPConfig() (*MCPStdioConfig, error) {
	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return nil, err
	}
	m := cfg.MCP
	if m == nil {
		m = &MCPStdioConfig{}
	}
	ApplyMCPStdioDefaults(m)
	return m, nil
}

// SaveMCPConfig writes the mcp section, preserving existing endpoints and magi.
// Uses read-modify-write so a save from the MCP settings form cannot clobber
// the endpoint list (same contract as SaveMagiConfig).
func SaveMCPConfig(m *MCPStdioConfig) error {
	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return fmt.Errorf("load embedded config for mcp save: %w", err)
	}
	ApplyMCPStdioDefaults(m)
	cfg.MCP = m
	return SaveEmbeddedConfig(cfg)
}

// ApplyMCPStdioDefaults fills zero-value fields with defaults. Call after load.
func ApplyMCPStdioDefaults(m *MCPStdioConfig) {
	if m.StdioTimeoutSeconds <= 0 || m.StdioTimeoutSeconds > MCPStdioTimeoutHardCeilSeconds {
		m.StdioTimeoutSeconds = DefaultMCPStdioTimeoutSeconds
	}
}

// GetMCPStdioTimeoutSeconds is the per-request read timeout for external MCP
// stdio servers, in seconds. Unset, zero, negative, or above-ceiling values in
// embedded.yaml fall back to DefaultMCPStdioTimeoutSeconds, so a bad value
// written straight into the file still leaves a working timeout.
func GetMCPStdioTimeoutSeconds() int {
	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return DefaultMCPStdioTimeoutSeconds
	}
	if cfg.MCP == nil {
		return DefaultMCPStdioTimeoutSeconds
	}
	s := cfg.MCP.StdioTimeoutSeconds
	if s <= 0 || s > MCPStdioTimeoutHardCeilSeconds {
		return DefaultMCPStdioTimeoutSeconds
	}
	return s
}

// NormalizeMCPStdioTimeoutSeconds coerces a client-supplied stdio timeout into
// a usable number of seconds. The web form and hand-edits can both deliver a
// string, so "300" is accepted, and a duration form ("5m", "1h") is accepted
// too because every other timeout knob in Shepherd is written that way. Zero,
// negative, unparseable, and above-ceiling values are rejected with an error —
// a typo in a timeout must not silently become "no timeout".
func NormalizeMCPStdioTimeoutSeconds(value interface{}) (int, error) {
	raw := ""
	switch t := value.(type) {
	case float64:
		raw = strconv.Itoa(int(t))
	case int:
		raw = strconv.Itoa(t)
	case int64:
		raw = strconv.Itoa(int(t))
	case string:
		raw = strings.TrimSpace(t)
	default:
		return 0, fmt.Errorf("stdio_timeout_seconds must be a number of seconds or a duration such as \"5m\"")
	}
	if raw == "" {
		return 0, fmt.Errorf("stdio_timeout_seconds must not be empty")
	}
	// Plain integer seconds first: the web form submits a number, so the
	// common path must not depend on duration parsing quirks.
	if n, err := strconv.Atoi(raw); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("stdio_timeout_seconds must be at least 1 second")
		}
		if n > MCPStdioTimeoutHardCeilSeconds {
			return 0, fmt.Errorf("stdio_timeout_seconds cannot exceed %d seconds", MCPStdioTimeoutHardCeilSeconds)
		}
		return n, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("stdio_timeout_seconds must be a number of seconds or a duration such as \"5m\", got %q", raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("stdio_timeout_seconds must be at least 1 second")
	}
	if int(d.Seconds()) > MCPStdioTimeoutHardCeilSeconds {
		return 0, fmt.Errorf("stdio_timeout_seconds cannot exceed %d seconds", MCPStdioTimeoutHardCeilSeconds)
	}
	return int(d.Seconds()), nil
}
