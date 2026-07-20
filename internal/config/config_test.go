package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// TestGetConcurrencyLimitsInProcessSet is a regression test for a bug where a
// concurrency limit saved through the settings UI read back as empty until the
// daemon restarted. viper.Set stores the value in the override layer as a typed
// map[string]int, and viper.GetStringMap (and cast.ToStringMap) return an empty
// map for that shape — so the settings page showed no value and the dispatch
// gate ignored the limit. GetConcurrencyLimits must read it via viper.Get and
// normalize the typed map itself.
func TestGetConcurrencyLimitsInProcessSet(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.SetDefault("concurrency_limits", map[string]interface{}{})

	// Shape written by viper.Set in the running process (override layer).
	viper.Set("concurrency_limits", map[string]int{"claude": 2, "opencode": 1})
	got := GetConcurrencyLimits()
	if got["claude"] != 2 || got["opencode"] != 1 {
		t.Fatalf("in-process Set: got %v, want claude=2 opencode=1", got)
	}
}

// TestGetConcurrencyLimitsFromFileShape covers the shape viper produces after
// parsing the YAML config file on startup (map[string]interface{} with numeric
// values), which is the path that already worked before the fix.
func TestGetConcurrencyLimitsFromFileShape(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.Set("concurrency_limits", map[string]interface{}{
		"claude":   int(2),
		"opencode": float64(1), // YAML/JSON numbers can arrive as float64
		"auto":     "3",        // tolerate string-encoded ints
	})
	got := GetConcurrencyLimits()
	if got["claude"] != 2 || got["opencode"] != 1 || got["auto"] != 3 {
		t.Fatalf("file shape: got %v, want claude=2 opencode=1 auto=3", got)
	}
}

// TestGetConcurrencyLimitsEmpty returns nil when nothing is configured so
// callers can treat "no limits" uniformly.
func TestGetConcurrencyLimitsEmpty(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	if got := GetConcurrencyLimits(); got != nil {
		t.Fatalf("unset: got %v, want nil", got)
	}
}

// TestEmbeddedEndpoint_MaxConcurrent_YAMLRoundTrip verifies that the
// max_concurrent field survives a YAML marshal/unmarshal round-trip.
// EmbeddedEndpoint has mapstructure tags only (no yaml tags), so yaml.v3
// uses the lowercased field name as the key — "maxconcurrent", matching
// the existing pattern of baseurl, maxiterations, contexttokens.
func TestEmbeddedEndpoint_MaxConcurrent_YAMLRoundTrip(t *testing.T) {
	yamlData := []byte("endpoints:\n  - id: test\n    label: Test\n    baseurl: http://localhost:8080\n    model: test-model\n    enabled: true\n    maxconcurrent: 2\n")
	cfg, err := UnmarshalEmbeddedYAML(yamlData)
	if err != nil {
		t.Fatalf("UnmarshalEmbeddedYAML failed: %v", err)
	}
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(cfg.Endpoints))
	}
	if cfg.Endpoints[0].MaxConcurrent != 2 {
		t.Fatalf("expected MaxConcurrent=2, got %d", cfg.Endpoints[0].MaxConcurrent)
	}

	// Round-trip: marshal back and re-parse to ensure the value persists.
	out, err := MarshalEmbeddedYAML(cfg)
	if err != nil {
		t.Fatalf("MarshalEmbeddedYAML failed: %v", err)
	}
	cfg2, err := UnmarshalEmbeddedYAML(out)
	if err != nil {
		t.Fatalf("second UnmarshalEmbeddedYAML failed: %v", err)
	}
	if cfg2.Endpoints[0].MaxConcurrent != 2 {
		t.Fatalf("round-trip: expected MaxConcurrent=2, got %d", cfg2.Endpoints[0].MaxConcurrent)
	}
}

// TestEmbeddedEndpoint_MaxConcurrent_JSONRoundTrip verifies that
// EndpointsFromJSON includes the MaxConcurrent mapping. Without this
// mapping, the API save path would silently drop the field (#7461 C3).
func TestEmbeddedEndpoint_MaxConcurrent_JSONRoundTrip(t *testing.T) {
	jsonEps := []EmbeddedEndpointJSON{
		{ID: "test", MaxConcurrent: 3},
	}
	eps := EndpointsFromJSON(jsonEps)
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].MaxConcurrent != 3 {
		t.Fatalf("expected MaxConcurrent=3, got %d", eps[0].MaxConcurrent)
	}

	// Also verify EndpointsToJSON carries the value forward.
	back := EndpointsToJSON(eps)
	if back[0].MaxConcurrent != 3 {
		t.Fatalf("EndpointsToJSON: expected MaxConcurrent=3, got %d", back[0].MaxConcurrent)
	}
}

// TestFormatEndpointCatalog verifies the agent-facing catalog omits secrets
// and uses exact id fields (spawn_subagents endpoint discovery, #7728–#7730).
func TestFormatEndpointCatalog(t *testing.T) {
	if got := FormatEndpointCatalog(nil); got != "(no enabled endpoints)" {
		t.Fatalf("empty: got %q", got)
	}

	eps := []EmbeddedEndpoint{
		{ID: "gents-a1-4b", Label: "agents-a1-4b", Model: "agents-a1-4b", APIKey: "SECRET", BaseURL: "http://127.0.0.1:8090/v1", MaxConcurrent: 8},
		{ID: "umans", Label: "umans-glm", Model: "umans-glm-5.2", APIKey: "SECRET2", MaxConcurrent: 3},
	}
	got := FormatEndpointCatalog(eps)
	if !strings.Contains(got, `id="gents-a1-4b"`) {
		t.Fatalf("missing id: %s", got)
	}
	if !strings.Contains(got, `label="agents-a1-4b"`) {
		t.Fatalf("missing label: %s", got)
	}
	if strings.Contains(got, "SECRET") || strings.Contains(got, "127.0.0.1") {
		t.Fatalf("catalog must not include API keys or base URLs: %s", got)
	}
}

// TestFormatUnknownEndpointError lists available ids for agent retry.
func TestFormatUnknownEndpointError(t *testing.T) {
	// This hits the live config path; only assert shape when load succeeds.
	msg := FormatUnknownEndpointError("agent-a1-4b")
	if !strings.Contains(msg, `endpoint "agent-a1-4b" not found`) {
		t.Fatalf("unexpected message: %s", msg)
	}
}
