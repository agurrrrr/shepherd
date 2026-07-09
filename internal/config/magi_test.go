package config

import (
	"strings"
	"testing"
)

// helper: build an EmbeddedConfig with 3 distinct endpoints and a valid magi section.
func validMagiConfig() *EmbeddedConfig {
	return &EmbeddedConfig{
		Endpoints: []EmbeddedEndpoint{
			{ID: "gpu0-qwen", BaseURL: "http://192.168.x.a:8080", Model: "qwen3-27b", Enabled: true},
			{ID: "gpu1-llama", BaseURL: "http://192.168.x.b:8080", Model: "llama-3.3-70b", Enabled: true},
			{ID: "mi50-mistral", BaseURL: "http://192.168.x.c:8080", Model: "mistral-small", Enabled: true},
		},
		Magi: &MagiConfig{
			Enabled: true,
			Proposers: []MagiProposer{
				{EndpointID: "gpu0-qwen", Persona: "melchior"},
				{EndpointID: "gpu1-llama", Persona: "balthasar"},
				{EndpointID: "mi50-mistral", Persona: "casper"},
			},
			Aggregator:             MagiAggregator{Type: "claude_cli"},
			Escalation:             MagiEscalation{ConfidenceThreshold: 7, MaxDebateRounds: 1},
			ProposerTimeoutSeconds: 300,
			Mode:                   "advisory",
		},
	}
}

func TestValidateMagiConfig_Valid(t *testing.T) {
	cfg := validMagiConfig()
	errs, warnings := ValidateMagiConfig(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestValidateMagiConfig_TwoProposers(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Proposers = cfg.Magi.Proposers[:2]
	errs, _ := ValidateMagiConfig(cfg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for 2 proposers, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "exactly 3 proposers") {
		t.Fatalf("error should mention proposer count, got: %s", errs[0])
	}
}

func TestValidateMagiConfig_MissingEndpoint(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Proposers[2].EndpointID = "nonexistent"
	errs, _ := ValidateMagiConfig(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "not found or disabled") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected endpoint-not-found error, got: %v", errs)
	}
}

func TestValidateMagiConfig_DisabledEndpoint(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Endpoints[1].Enabled = false
	errs, _ := ValidateMagiConfig(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "not found or disabled") && strings.Contains(e, "gpu1-llama") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected disabled-endpoint error for gpu1-llama, got: %v", errs)
	}
}

func TestValidateMagiConfig_UnknownPersona(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Proposers[0].Persona = "scientist"
	errs, _ := ValidateMagiConfig(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown persona") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown-persona error, got: %v", errs)
	}
}

func TestValidateMagiConfig_CustomPersonaNoPrompt(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Proposers[0].Persona = "custom"
	errs, _ := ValidateMagiConfig(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "custom persona requires custom_prompt") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected custom-prompt-required error, got: %v", errs)
	}
}

func TestValidateMagiConfig_CustomPersonaWithPrompt(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Proposers[0].Persona = "custom"
	cfg.Magi.Proposers[0].CustomPrompt = "You are a meticulous code reviewer."
	errs, _ := ValidateMagiConfig(cfg)
	for _, e := range errs {
		if strings.Contains(e, "custom persona") {
			t.Fatalf("should not error when custom_prompt is set, got: %s", e)
		}
	}
}

func TestValidateMagiConfig_AggregatorUnknownType(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Aggregator.Type = "openai"
	errs, _ := ValidateMagiConfig(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "aggregator") && strings.Contains(e, "unknown type") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected aggregator unknown-type error, got: %v", errs)
	}
}

func TestValidateMagiConfig_AggregatorEndpointMissing(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Aggregator.Type = "endpoint"
	cfg.Magi.Aggregator.EndpointID = "nonexistent"
	errs, _ := ValidateMagiConfig(cfg)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "aggregator") && strings.Contains(e, "not found or disabled") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected aggregator endpoint-not-found error, got: %v", errs)
	}
}

func TestValidateMagiConfig_AggregatorEndpointValid(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Aggregator.Type = "endpoint"
	cfg.Magi.Aggregator.EndpointID = "mi50-mistral"
	errs, _ := ValidateMagiConfig(cfg)
	for _, e := range errs {
		if strings.Contains(e, "aggregator") {
			t.Fatalf("should not have aggregator errors when endpoint is valid, got: %s", e)
		}
	}
}

func TestValidateMagiConfig_AggregatorOpenCodeCLI(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Aggregator.Type = "opencode_cli"
	cfg.Magi.Aggregator.ModelID = "claude-sonnet-4-20250514"
	errs, _ := ValidateMagiConfig(cfg)
	for _, e := range errs {
		if strings.Contains(e, "aggregator") {
			t.Fatalf("opencode_cli aggregator should be valid, got: %s", e)
		}
	}
}

func TestValidateMagiConfig_AggregatorOpenCodeCLINoModel(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Aggregator.Type = "opencode_cli"
	cfg.Magi.Aggregator.ModelID = "" // empty is OK — uses CLI default
	errs, _ := ValidateMagiConfig(cfg)
	for _, e := range errs {
		if strings.Contains(e, "aggregator") {
			t.Fatalf("opencode_cli aggregator with empty model_id should be valid, got: %s", e)
		}
	}
}

func TestValidateMagiConfig_SameModelWarning(t *testing.T) {
	cfg := validMagiConfig()
	for i := range cfg.Endpoints {
		cfg.Endpoints[i].Model = "qwen3-27b"
	}
	errs, warnings := ValidateMagiConfig(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "same model") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected same-model warning, got: %v", warnings)
	}
}

func TestValidateMagiConfig_DuplicateBaseURLWarning(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Endpoints[0].BaseURL = "http://192.168.x.a:8080"
	cfg.Endpoints[1].BaseURL = "http://192.168.x.a:8080" // same as gpu0
	errs, warnings := ValidateMagiConfig(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "share base_url") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected base_url duplicate warning, got: %v", warnings)
	}
}

func TestValidateMagiConfig_DuplicatePersonaWarning(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Proposers[0].Persona = "melchior"
	cfg.Magi.Proposers[1].Persona = "melchior" // duplicate
	errs, warnings := ValidateMagiConfig(cfg)
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "duplicate persona") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected duplicate-persona warning, got: %v", warnings)
	}
}

func TestValidateMagiConfig_NilMagi(t *testing.T) {
	cfg := &EmbeddedConfig{
		Endpoints: []EmbeddedEndpoint{},
		Magi:      nil,
	}
	errs, warnings := ValidateMagiConfig(cfg)
	if len(errs) != 0 || len(warnings) != 0 {
		t.Fatalf("nil magi should produce no errors/warnings, got errs=%v warnings=%v", errs, warnings)
	}
}

func TestValidateMagiConfig_DisabledSkipsValidation(t *testing.T) {
	cfg := validMagiConfig()
	cfg.Magi.Enabled = false
	// Even with empty endpoint_ids, disabled magi should produce no errors.
	cfg.Magi.Proposers[0].EndpointID = ""
	cfg.Magi.Proposers[1].EndpointID = ""
	cfg.Magi.Proposers[2].EndpointID = ""
	errs, warnings := ValidateMagiConfig(cfg)
	if len(errs) != 0 {
		t.Fatalf("disabled magi should skip validation, got errors: %v", errs)
	}
	if len(warnings) != 0 {
		t.Fatalf("disabled magi should skip warnings, got: %v", warnings)
	}
}

func TestValidateMagiConfig_EnabledEmptyEndpointsAreWarnings(t *testing.T) {
	cfg := validMagiConfig()
	// All proposers have empty endpoint_id — should be warnings, not errors.
	cfg.Magi.Proposers[0].EndpointID = ""
	cfg.Magi.Proposers[1].EndpointID = ""
	cfg.Magi.Proposers[2].EndpointID = ""
	// Aggregator type endpoint with empty ID — also a warning.
	cfg.Magi.Aggregator.Type = "endpoint"
	cfg.Magi.Aggregator.EndpointID = ""

	errs, warnings := ValidateMagiConfig(cfg)
	if len(errs) != 0 {
		t.Fatalf("empty endpoint_ids should be warnings not errors, got errors: %v", errs)
	}
	// Should have 3 proposer warnings + 1 aggregator warning.
	if len(warnings) < 4 {
		t.Fatalf("expected at least 4 warnings (3 proposers + 1 aggregator), got %d: %v", len(warnings), warnings)
	}
	foundProposer := false
	foundAggregator := false
	for _, w := range warnings {
		if strings.Contains(w, "proposer 1: no endpoint selected") {
			foundProposer = true
		}
		if strings.Contains(w, "aggregator: no endpoint selected") {
			foundAggregator = true
		}
	}
	if !foundProposer {
		t.Fatalf("expected proposer no-endpoint warning, got: %v", warnings)
	}
	if !foundAggregator {
		t.Fatalf("expected aggregator no-endpoint warning, got: %v", warnings)
	}
}

func TestApplyMagiDefaults(t *testing.T) {
	m := &MagiConfig{
		Escalation:             MagiEscalation{ConfidenceThreshold: 0, MaxDebateRounds: 0},
		ProposerTimeoutSeconds: 0,
		Mode:                   "",
		Aggregator:              MagiAggregator{Type: ""},
	}
	ApplyMagiDefaults(m)

	if m.Escalation.ConfidenceThreshold != 7 {
		t.Errorf("threshold: got %d, want 7", m.Escalation.ConfidenceThreshold)
	}
	if m.ProposerTimeoutSeconds != 300 {
		t.Errorf("timeout: got %d, want 300", m.ProposerTimeoutSeconds)
	}
	if m.Mode != "advisory" {
		t.Errorf("mode: got %q, want advisory", m.Mode)
	}
	if m.Aggregator.Type != "claude_cli" {
		t.Errorf("aggregator type: got %q, want claude_cli", m.Aggregator.Type)
	}
	// MaxDebateRounds == 0 is a valid value ("no debate"), must be preserved.
	if m.Escalation.MaxDebateRounds != 0 {
		t.Errorf("max_debate_rounds: got %d, want 0 (valid value must be preserved)", m.Escalation.MaxDebateRounds)
	}
}

func TestApplyMagiDefaults_NegativeDebateRounds(t *testing.T) {
	m := &MagiConfig{
		Escalation: MagiEscalation{MaxDebateRounds: -1},
	}
	ApplyMagiDefaults(m)
	if m.Escalation.MaxDebateRounds != 1 {
		t.Errorf("negative max_debate_rounds: got %d, want 1", m.Escalation.MaxDebateRounds)
	}
}

func TestApplyMagiDefaults_PreservesExplicitValues(t *testing.T) {
	m := &MagiConfig{
		Escalation:             MagiEscalation{ConfidenceThreshold: 9, MaxDebateRounds: 2},
		ProposerTimeoutSeconds: 60,
		Mode:                   "plan",
		Aggregator:              MagiAggregator{Type: "endpoint"},
	}
	ApplyMagiDefaults(m)

	if m.Escalation.ConfidenceThreshold != 9 {
		t.Errorf("threshold should be preserved: got %d, want 9", m.Escalation.ConfidenceThreshold)
	}
	if m.Escalation.MaxDebateRounds != 2 {
		t.Errorf("max_debate_rounds should be preserved: got %d, want 2", m.Escalation.MaxDebateRounds)
	}
	if m.ProposerTimeoutSeconds != 60 {
		t.Errorf("timeout should be preserved: got %d, want 60", m.ProposerTimeoutSeconds)
	}
	if m.Mode != "plan" {
		t.Errorf("mode should be preserved: got %q, want plan", m.Mode)
	}
	if m.Aggregator.Type != "endpoint" {
		t.Errorf("aggregator type should be preserved: got %q, want endpoint", m.Aggregator.Type)
	}
}

func TestUnmarshalEmbeddedYAML_MagiRoundTrip(t *testing.T) {
	yamlIn := `
endpoints:
  - id: gpu0-qwen
    base_url: http://192.168.x.a:8080
    model: qwen3-27b
    enabled: true
  - id: gpu1-llama
    base_url: http://192.168.x.b:8080
    model: llama-3.3-70b
    enabled: true
  - id: mi50-mistral
    base_url: http://192.168.x.c:8080
    model: mistral-small
    enabled: true

magi:
  enabled: true
  proposers:
    - endpoint_id: gpu0-qwen
      persona: melchior
    - endpoint_id: gpu1-llama
      persona: balthasar
    - endpoint_id: mi50-mistral
      persona: casper
      custom_prompt: "You are a meticulous reviewer."
  aggregator:
    type: claude_cli
  escalation:
    confidence_threshold: 7
    max_debate_rounds: 1
  proposer_timeout_seconds: 300
  mode: advisory
`

	cfg, err := UnmarshalEmbeddedYAML([]byte(yamlIn))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if cfg.Magi == nil {
		t.Fatal("magi section should not be nil")
	}

	if len(cfg.Magi.Proposers) != 3 {
		t.Fatalf("expected 3 proposers, got %d", len(cfg.Magi.Proposers))
	}

	if cfg.Magi.Proposers[0].EndpointID != "gpu0-qwen" {
		t.Errorf("proposer 0 endpoint_id: got %q, want gpu0-qwen", cfg.Magi.Proposers[0].EndpointID)
	}

	if cfg.Magi.Proposers[2].CustomPrompt != "You are a meticulous reviewer." {
		t.Errorf("proposer 2 custom_prompt: got %q", cfg.Magi.Proposers[2].CustomPrompt)
	}

	if cfg.Magi.Aggregator.Type != "claude_cli" {
		t.Errorf("aggregator type: got %q, want claude_cli", cfg.Magi.Aggregator.Type)
	}

	if cfg.Magi.Mode != "advisory" {
		t.Errorf("mode: got %q, want advisory", cfg.Magi.Mode)
	}

	if cfg.Magi.Escalation.ConfidenceThreshold != 7 {
		t.Errorf("threshold: got %d, want 7", cfg.Magi.Escalation.ConfidenceThreshold)
	}

	// Marshal back and re-parse to verify round-trip fidelity.
	data, err := MarshalEmbeddedYAML(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	cfg2, err := UnmarshalEmbeddedYAML(data)
	if err != nil {
		t.Fatalf("re-unmarshal failed: %v", err)
	}

	if cfg2.Magi == nil {
		t.Fatal("magi section should not be nil after round-trip")
	}

	if len(cfg2.Magi.Proposers) != 3 {
		t.Fatalf("expected 3 proposers after round-trip, got %d", len(cfg2.Magi.Proposers))
	}

	if cfg2.Magi.Proposers[0].EndpointID != cfg.Magi.Proposers[0].EndpointID {
		t.Errorf("round-trip proposer 0 endpoint_id mismatch")
	}

	if cfg2.Magi.Aggregator.Type != cfg.Magi.Aggregator.Type {
		t.Errorf("round-trip aggregator type mismatch")
	}

	if cfg2.Magi.Mode != cfg.Magi.Mode {
		t.Errorf("round-trip mode mismatch")
	}

	if cfg2.Magi.Escalation.ConfidenceThreshold != cfg.Magi.Escalation.ConfidenceThreshold {
		t.Errorf("round-trip threshold mismatch")
	}
}

func TestUnmarshalEmbeddedYAML_NoMagiSection(t *testing.T) {
	yamlIn := `
endpoints:
  - id: gpu0-qwen
    base_url: http://192.168.x.a:8080
    model: qwen3-27b
    enabled: true
`

	cfg, err := UnmarshalEmbeddedYAML([]byte(yamlIn))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if cfg.Magi != nil {
		t.Fatalf("magi should be nil when section is absent, got %+v", cfg.Magi)
	}

	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(cfg.Endpoints))
	}
}

func TestUnmarshalEmbeddedYAML_MagiAggregatorOpenCodeCLI(t *testing.T) {
	yamlIn := `
endpoints:
  - id: gpu0-qwen
    base_url: http://192.168.x.a:8080
    model: qwen3-27b
    enabled: true
  - id: gpu1-llama
    base_url: http://192.168.x.b:8080
    model: llama-3.3-70b
    enabled: true
  - id: mi50-mistral
    base_url: http://192.168.x.c:8080
    model: mistral-small
    enabled: true

magi:
  enabled: true
  proposers:
    - endpoint_id: gpu0-qwen
      persona: melchior
    - endpoint_id: gpu1-llama
      persona: balthasar
    - endpoint_id: mi50-mistral
      persona: casper
  aggregator:
    type: opencode_cli
    model_id: claude-sonnet-4-20250514
  escalation:
    confidence_threshold: 7
    max_debate_rounds: 1
  proposer_timeout_seconds: 300
  mode: advisory
`

	cfg, err := UnmarshalEmbeddedYAML([]byte(yamlIn))
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if cfg.Magi == nil {
		t.Fatal("magi section should not be nil")
	}

	if cfg.Magi.Aggregator.Type != "opencode_cli" {
		t.Errorf("aggregator type: got %q, want opencode_cli", cfg.Magi.Aggregator.Type)
	}

	if cfg.Magi.Aggregator.ModelID != "claude-sonnet-4-20250514" {
		t.Errorf("aggregator model_id: got %q, want claude-sonnet-4-20250514", cfg.Magi.Aggregator.ModelID)
	}

	// Marshal back and re-parse to verify round-trip fidelity.
	data, err := MarshalEmbeddedYAML(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	cfg2, err := UnmarshalEmbeddedYAML(data)
	if err != nil {
		t.Fatalf("re-unmarshal failed: %v", err)
	}

	if cfg2.Magi.Aggregator.Type != "opencode_cli" {
		t.Errorf("round-trip aggregator type mismatch: got %q, want opencode_cli", cfg2.Magi.Aggregator.Type)
	}

	if cfg2.Magi.Aggregator.ModelID != "claude-sonnet-4-20250514" {
		t.Errorf("round-trip aggregator model_id mismatch: got %q, want claude-sonnet-4-20250514", cfg2.Magi.Aggregator.ModelID)
	}
}
