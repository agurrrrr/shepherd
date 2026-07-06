package config

import "fmt"

// MagiProposer selects one embedded endpoint as a deliberation member.
type MagiProposer struct {
	EndpointID   string `mapstructure:"endpoint_id" yaml:"endpoint_id"`
	Persona      string `mapstructure:"persona" yaml:"persona"` // melchior | balthasar | casper | custom
	CustomPrompt string `mapstructure:"custom_prompt" yaml:"custom_prompt,omitempty"`
}

// MagiAggregator selects the synthesis/judging backend.
type MagiAggregator struct {
	Type       string `mapstructure:"type" yaml:"type"` // claude_cli | endpoint
	EndpointID string `mapstructure:"endpoint_id" yaml:"endpoint_id,omitempty"`
}

// MagiEscalation controls the debate-escalation gate (design §5.3, §5.4).
type MagiEscalation struct {
	ConfidenceThreshold int `mapstructure:"confidence_threshold" yaml:"confidence_threshold"`
	MaxDebateRounds     int `mapstructure:"max_debate_rounds" yaml:"max_debate_rounds"`
}

// MagiConfig is the magi consensus provider settings (design §7).
type MagiConfig struct {
	Enabled                bool           `mapstructure:"enabled" yaml:"enabled"`
	Proposers              []MagiProposer `mapstructure:"proposers" yaml:"proposers"`
	Aggregator             MagiAggregator `mapstructure:"aggregator" yaml:"aggregator"`
	Escalation             MagiEscalation `mapstructure:"escalation" yaml:"escalation"`
	ProposerTimeoutSeconds int            `mapstructure:"proposer_timeout_seconds" yaml:"proposer_timeout_seconds"`
	Mode                   string         `mapstructure:"mode" yaml:"mode"` // advisory (Phase 1); plan/review reserved
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
		m.ProposerTimeoutSeconds = 120
	}
	if m.Mode == "" {
		m.Mode = "advisory"
	}
	if m.Aggregator.Type == "" {
		m.Aggregator.Type = "claude_cli"
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
		ep, exists := endpointByID[p.EndpointID]
		if !exists || !ep.Enabled {
			errs = append(errs, fmt.Sprintf("proposer %d: endpoint %q not found or disabled", i+1, p.EndpointID))
		}

		if !validPersonas[p.Persona] {
			errs = append(errs, fmt.Sprintf("proposer %d: unknown persona %q", i+1, p.Persona))
		}

		if p.Persona == "custom" && p.CustomPrompt == "" {
			errs = append(errs, fmt.Sprintf("proposer %d: custom persona requires custom_prompt", i+1))
		}
	}

	// Aggregator validation.
	switch m.Aggregator.Type {
	case "claude_cli", "":
		// valid — empty defaults to claude_cli after ApplyMagiDefaults,
		// but ValidateMagiConfig is a pure function that may be called
		// before defaults are applied. Treat "" as acceptable.
	case "endpoint":
		if m.Aggregator.EndpointID == "" {
			errs = append(errs, "aggregator: endpoint \"\" not found or disabled")
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
		model   string
		baseURL string
		persona string
	}
	var infos []proposerEndpointInfo
	for _, p := range m.Proposers {
		if ep, ok := endpointByID[p.EndpointID]; ok && ep.Enabled {
			infos = append(infos, proposerEndpointInfo{
				model:   ep.Model,
				baseURL: ep.BaseURL,
				persona: p.Persona,
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

	// Duplicate base_url (design §4).
	baseURLCount := make(map[string]int)
	for _, info := range infos {
		if info.baseURL != "" {
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
