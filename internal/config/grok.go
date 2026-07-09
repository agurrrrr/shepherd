package config

// grok (Grok Build TUI / xAI) exposes a small, fixed set of models rather than a
// user-editable config file, so — unlike ListOpenCodeModels / ListPiModels which
// read the CLI's own config — the selectable models are a curated list kept here.
// IDs are passed straight to `grok -m <id>`. Update this list when xAI ships new
// models. `grok models` prints the live set for the logged-in account.

// GrokModelOption describes a single selectable grok model.
type GrokModelOption struct {
	ID    string `json:"id"`    // passed to `grok -m`
	Label string `json:"label"` // human-readable display name
}

// ListGrokModels returns the curated set of selectable grok models.
func ListGrokModels() []GrokModelOption {
	return []GrokModelOption{
		{ID: "grok-4.5", Label: "grok-4.5 (default)"},
		{ID: "grok-composer-2.5-fast", Label: "grok-composer-2.5-fast"},
	}
}
