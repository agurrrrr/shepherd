package server

// Sheep naming config endpoints (#638 §1). Validation rules make these keys
// unsuitable for the generic PATCH /api/config allowlist, so they get their own
// GET/PUT pair, mirroring the /api/config/magi pattern.

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
)

// sheepNamingProviders are the providers a user may pick for sheep naming.
// magi has no single model (modelOptionsFor returns false) and auto folds into
// claude at exec time, so neither is offered.
var sheepNamingProviders = []string{"claude", "opencode", "pi", "grok", "embedded"}

// buildSheepNamingConfig renders the current sheep-naming settings plus the
// provider/model catalogs the form needs (same model list source as the
// shepherd tab: modelOptionsFor).
func buildSheepNamingConfig() map[string]interface{} {
	provider := strings.TrimSpace(config.GetString("sheep_name_llm_provider"))

	var enabledProviders []string
	for _, p := range sheepNamingProviders {
		if config.IsProviderEnabled(p) {
			enabledProviders = append(enabledProviders, p)
		}
	}

	modelOptions, modelSelectable := modelOptionsFor(provider, "프로바이더 기본값")

	return map[string]interface{}{
		"enabled":          config.GetBool("sheep_name_llm_enabled"),
		"provider":         provider,
		"model":            strings.TrimSpace(config.GetString("sheep_name_llm_model")),
		"providers":        enabledProviders,
		"model_options":    modelOptions,
		"model_selectable": modelSelectable,
	}
}

// GET /api/config/sheep-naming
func (s *Server) handleGetSheepNamingConfig(c *fiber.Ctx) error {
	return success(c, buildSheepNamingConfig())
}

// PUT /api/config/sheep-naming
// Body: {"enabled": bool, "provider": "...", "model": "..."}.
// Turning the switch on requires a provider that is currently enabled; magi and
// auto are rejected. When the provider changes and the saved model is not in
// the new provider's list, the model resets to "" (same rule as the shepherd
// config handler). All keys persist in one write via persistConfig.
func (s *Server) handleUpdateSheepNamingConfig(c *fiber.Ctx) error {
	var body struct {
		Enabled  *bool   `json:"enabled"`
		Provider *string `json:"provider"`
		Model    *string `json:"model"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	oldEnabled := config.GetBool("sheep_name_llm_enabled")
	oldProvider := strings.TrimSpace(config.GetString("sheep_name_llm_provider"))
	model := strings.TrimSpace(config.GetString("sheep_name_llm_model"))

	newEnabled := oldEnabled
	if body.Enabled != nil {
		newEnabled = *body.Enabled
	}
	newProvider := oldProvider
	if body.Provider != nil {
		newProvider = strings.TrimSpace(*body.Provider)
	}

	values := map[string]interface{}{}
	if body.Enabled != nil {
		values["sheep_name_llm_enabled"] = newEnabled
	}
	if body.Provider != nil {
		values["sheep_name_llm_provider"] = newProvider
	}
	if body.Model != nil {
		model = strings.TrimSpace(*body.Model)
		values["sheep_name_llm_model"] = model
	}

	// Provider validation: must be one of the naming providers AND currently
	// enabled in the provider switches.
	if newProvider != "" {
		found := false
		for _, p := range sheepNamingProviders {
			if p == newProvider {
				found = true
				break
			}
		}
		if !found {
			return fail(c, fiber.StatusBadRequest, "provider '"+newProvider+"' is not valid for sheep naming (claude, opencode, pi, grok, embedded)")
		}
		if !config.IsProviderEnabled(newProvider) {
			return fail(c, fiber.StatusBadRequest, "provider '"+newProvider+"' is disabled in settings")
		}
	}
	if newEnabled && newProvider == "" {
		return fail(c, fiber.StatusBadRequest, "provider is required when sheep naming is enabled")
	}

	// Provider change: a model saved for the old provider is meaningless for
	// the new one. Fall back to the provider default when it isn't offered.
	if body.Provider != nil && newProvider != oldProvider {
		opts, selectable := modelOptionsFor(newProvider, "")
		if !selectable || (model != "" && !modelOptionHas(opts, model)) {
			model = ""
			values["sheep_name_llm_model"] = ""
		}
	}

	if len(values) > 0 {
		if err := persistConfig(values); err != nil {
			return fail(c, fiber.StatusInternalServerError, "failed to save config: "+err.Error())
		}
	}

	return success(c, buildSheepNamingConfig())
}
