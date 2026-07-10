package config

import "fmt"

// MagiProposer selects one LLM backend as a deliberation member.
// Provider determines which backend type to use:
//   - "embedded" (default): uses an embedded endpoint (EndpointID refers to embedded.yaml endpoint)
//   - "claude_cli": uses Claude CLI (ModelID selects a claude model alias like "opus", "sonnet")
//   - "opencode_cli": uses OpenCode CLI (ModelID selects an opencode model)
//   - "grok_cli": uses Grok CLI (ModelID selects a grok model like "grok-4.5")
type MagiProposer struct {
	Provider     string `mapstructure:"provider" json:"provider,omitempty" yaml:"provider,omitempty"` // embedded | claude_cli | opencode_cli | grok_cli (default: embedded)
	EndpointID   string `mapstructure:"endpoint_id" json:"endpoint_id" yaml:"endpoint_id"`           // embedded endpoint ID (when provider == "embedded")
	ModelID      string `mapstructure:"model_id" json:"model_id,omitempty" yaml:"model_id,omitempty"` // model alias for claude_cli/opencode_cli
	Persona      string `mapstructure:"persona" json:"persona" yaml:"persona"`                        // melchior | balthasar | casper | custom
	DisplayName  string `mapstructure:"display_name" json:"display_name,omitempty" yaml:"display_name,omitempty"` // custom display name; overrides MELCHIOR-N when non-empty
	CustomPrompt string `mapstructure:"custom_prompt" json:"custom_prompt,omitempty" yaml:"custom_prompt,omitempty"`
	// TimeoutSeconds overrides the global proposer_timeout_seconds for this
	// slot only. 0 = inherit the global value. Lets a slow local model get a
	// longer budget without holding fast remote slots hostage (task #7205).
	TimeoutSeconds int `mapstructure:"timeout_seconds" json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
}

// MagiAggregator selects the synthesis/judging backend.
type MagiAggregator struct {
	Type       string `mapstructure:"type" json:"type" yaml:"type"`                       // claude_cli | opencode_cli | grok_cli | endpoint
	EndpointID string `mapstructure:"endpoint_id" json:"endpoint_id,omitempty" yaml:"endpoint_id,omitempty"` // embedded endpoint ID (when type == "endpoint")
	ModelID    string `mapstructure:"model_id" json:"model_id,omitempty" yaml:"model_id,omitempty"`        // model alias for claude_cli/opencode_cli
}

// MagiEscalation controls the debate-escalation gate (design §5.3, §5.4).
type MagiEscalation struct {
	ConfidenceThreshold int `mapstructure:"confidence_threshold" json:"confidence_threshold" yaml:"confidence_threshold"`
	MaxDebateRounds     int `mapstructure:"max_debate_rounds" json:"max_debate_rounds" yaml:"max_debate_rounds"`
}

// MagiConfig is the magi consensus provider settings (design §7).
type MagiConfig struct {
	Enabled                bool           `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	Proposers              []MagiProposer `mapstructure:"proposers" json:"proposers" yaml:"proposers"`
	Aggregator             MagiAggregator `mapstructure:"aggregator" json:"aggregator" yaml:"aggregator"`
	Escalation             MagiEscalation `mapstructure:"escalation" json:"escalation" yaml:"escalation"`
	ProposerTimeoutSeconds int            `mapstructure:"proposer_timeout_seconds" json:"proposer_timeout_seconds" yaml:"proposer_timeout_seconds"`
	Mode                   string         `mapstructure:"mode" json:"mode" yaml:"mode"` // advisory (Phase 1); plan/review reserved
}

// ApplyMagiDefaults fills zero-value fields with defaults. Call after load.
func ApplyMagiDefaults(m *MagiConfig) {
	if m.Escalation.ConfidenceThreshold <= 0 {
		m.Escalation.ConfidenceThreshold = 7
	}
	if m.Escalation.MaxDebateRounds < 0 {
		m.Escalation.MaxDebateRounds = 1
	}
	if m.ProposerTimeoutSeconds <= 0 {
		m.ProposerTimeoutSeconds = 300
	}
	if m.Mode == "" {
		m.Mode = "advisory"
	}
	if m.Aggregator.Type == "" {
		m.Aggregator.Type = "claude_cli"
	}
	// Backfill provider default for proposers (backward compat: empty → "embedded").
	for i := range m.Proposers {
		if m.Proposers[i].Provider == "" {
			m.Proposers[i].Provider = "embedded"
		}
	}
}

// GetMagiConfig loads the magi section from embedded.yaml with defaults applied.
// Returns nil when the section is absent (magi never configured).
func GetMagiConfig() (*MagiConfig, error) {
	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Magi == nil {
		return nil, nil
	}
	ApplyMagiDefaults(cfg.Magi)
	return cfg.Magi, nil
}

// SaveMagiConfig writes the magi section, preserving existing endpoints.
// Uses read-modify-write to avoid clobbering the endpoints list.
func SaveMagiConfig(m *MagiConfig) error {
	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return fmt.Errorf("load embedded config for magi save: %w", err)
	}
	cfg.Magi = m
	return SaveEmbeddedConfig(cfg)
}

// ValidateMagiConfig returns hard errors (config unusable) and soft warnings
// (config works but is inadvisable). Pure function for testability.
func ValidateMagiConfig(cfg *EmbeddedConfig) (errs []string, warnings []string) {
	if cfg.Magi == nil {
		return errs, warnings
	}
	m := cfg.Magi

	// When MAGI is disabled, skip hard validation — user can save
	// a partially configured (or empty) magi section without errors.
	if !m.Enabled {
		return errs, warnings
	}

	// --- Hard errors ---

	// Proposer count must be exactly 3.
	if len(m.Proposers) != 3 {
		errs = append(errs, fmt.Sprintf("magi requires exactly 3 proposers, got %d", len(m.Proposers)))
	}

	// Build a lookup map for endpoints by ID (only enabled ones are visible
	// via GetEmbeddedEndpointByID, but validation should report disabled
	// endpoints distinctly — we check existence + enabled here).
	endpointByID := make(map[string]*EmbeddedEndpoint, len(cfg.Endpoints))
	for i := range cfg.Endpoints {
		ep := &cfg.Endpoints[i]
		endpointByID[ep.ID] = ep
	}

	validPersonas := map[string]bool{
		"melchior":  true,
		"balthasar": true,
		"casper":    true,
		"custom":    true,
	}

	for i, p := range m.Proposers {
		provider := p.Provider
		if provider == "" {
			provider = "embedded"
		}

		switch provider {
		case "embedded":
			// Empty endpoint_id is a warning (not yet configured), not a hard error.
			// A non-empty ID that doesn't exist or is disabled is a hard error.
			if p.EndpointID == "" {
				warnings = append(warnings, fmt.Sprintf("proposer %d: no endpoint selected", i+1))
			} else {
				ep, exists := endpointByID[p.EndpointID]
				if !exists || !ep.Enabled {
					errs = append(errs, fmt.Sprintf("proposer %d: endpoint %q not found or disabled", i+1, p.EndpointID))
				}
			}
		case "claude_cli":
			// ModelID is optional — empty means CLI default.
			// No hard validation needed; the CLI will resolve the model.
		case "opencode_cli":
			// ModelID is optional — empty means OpenCode config default.
		case "grok_cli":
			// ModelID is optional — empty means grok default (grok-4.5).
		default:
			errs = append(errs, fmt.Sprintf("proposer %d: unknown provider %q", i+1, provider))
		}

		if !validPersonas[p.Persona] {
			errs = append(errs, fmt.Sprintf("proposer %d: unknown persona %q", i+1, p.Persona))
		}

		if p.Persona == "custom" && p.CustomPrompt == "" {
			errs = append(errs, fmt.Sprintf("proposer %d: custom persona requires custom_prompt", i+1))
		}

		if p.TimeoutSeconds < 0 {
			errs = append(errs, fmt.Sprintf("proposer %d: timeout_seconds must be >= 0", i+1))
		} else if p.TimeoutSeconds > 0 && p.TimeoutSeconds < 30 {
			warnings = append(warnings, fmt.Sprintf("proposer %d: timeout_seconds %d is very short (<30s)", i+1, p.TimeoutSeconds))
		}
	}

	// Aggregator validation.
	switch m.Aggregator.Type {
	case "claude_cli", "":
		// valid — empty defaults to claude_cli after ApplyMagiDefaults,
		// but ValidateMagiConfig is a pure function that may be called
		// before defaults are applied. Treat "" as acceptable.
	case "opencode_cli":
		// valid — ModelID is optional (empty means CLI default).
	case "grok_cli":
		// valid — ModelID is optional (empty means grok default).
	case "endpoint":
		if m.Aggregator.EndpointID == "" {
			warnings = append(warnings, "aggregator: no endpoint selected")
		} else {
			ep, exists := endpointByID[m.Aggregator.EndpointID]
			if !exists || !ep.Enabled {
				errs = append(errs, fmt.Sprintf("aggregator: endpoint %q not found or disabled", m.Aggregator.EndpointID))
			}
		}
	default:
		errs = append(errs, fmt.Sprintf("aggregator: unknown type %q", m.Aggregator.Type))
	}

	// --- Soft warnings (design §2.2, §4) ---

	if len(m.Proposers) == 0 {
		return errs, warnings
	}

	// Collect model and base_url info for proposers whose endpoints exist.
	type proposerEndpointInfo struct {
		model    string
		baseURL  string
		persona  string
		provider string
	}
	var infos []proposerEndpointInfo
	for _, p := range m.Proposers {
		provider := p.Provider
		if provider == "" {
			provider = "embedded"
		}
		if provider == "embedded" {
			if ep, ok := endpointByID[p.EndpointID]; ok && ep.Enabled {
				infos = append(infos, proposerEndpointInfo{
					model:    ep.Model,
					baseURL:  ep.BaseURL,
					persona:  p.Persona,
					provider: provider,
				})
			}
		} else {
			// claude_cli / opencode_cli — model is ModelID (or default).
			model := p.ModelID
			if model == "" {
				model = provider + ":default"
			}
			infos = append(infos, proposerEndpointInfo{
				model:    model,
				baseURL:  provider, // use provider as baseURL-equivalent for duplicate detection
				persona:  p.Persona,
				provider: provider,
			})
		}
	}

	// Same model across all proposers (design §2.2).
	if len(infos) >= 3 {
		allSame := true
		first := infos[0].model
		for _, info := range infos[1:] {
			if info.model != first {
				allSame = false
				break
			}
		}
		if allSame && first != "" {
			warnings = append(warnings, fmt.Sprintf(
				"all proposers use the same model %q — consensus value degrades sharply with correlated errors (use different model families)",
				first))
		}
	}

	// Duplicate base_url (design §4) — only meaningful for embedded proposers.
	baseURLCount := make(map[string]int)
	for _, info := range infos {
		if info.provider == "embedded" && info.baseURL != "" {
			baseURLCount[info.baseURL]++
		}
	}
	for url, count := range baseURLCount {
		if count > 1 {
			warnings = append(warnings, fmt.Sprintf(
				"proposers share base_url %q — requests will serialize on the same server",
				url))
		}
	}

	// Duplicate persona.
	personaCount := make(map[string]int)
	for _, info := range infos {
		if info.persona != "" {
			personaCount[info.persona]++
		}
	}
	for persona, count := range personaCount {
		if count > 1 {
			warnings = append(warnings, fmt.Sprintf(
				"duplicate persona %q across proposers",
				persona))
		}
	}

	return errs, warnings
}
