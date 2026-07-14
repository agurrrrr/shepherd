package server

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// GET /api/system/status
func (s *Server) handleSystemStatus(c *fiber.Ctx) error {
	sheepList, err := worker.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	projects, err := project.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	counts, err := queue.CountByStatus()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Count sheep by status
	working := 0
	idle := 0
	errCount := 0
	for _, sh := range sheepList {
		switch sh.Status {
		case "working":
			working++
		case "idle":
			idle++
		case "error":
			errCount++
		}
	}

	return success(c, map[string]interface{}{
		"sheep": map[string]int{
			"total":   len(sheepList),
			"working": working,
			"idle":    idle,
			"error":   errCount,
		},
		"projects": len(projects),
		"tasks": map[string]int{
			"pending":   counts[task.StatusPending],
			"running":   counts[task.StatusRunning],
			"completed": counts[task.StatusCompleted],
			"failed":    counts[task.StatusFailed],
			"stopped":   counts[task.StatusStopped],
		},
		"sse_clients": s.hub.ClientCount(),
	})
}

// GET /api/config
func (s *Server) handleGetConfig(c *fiber.Ctx) error {
	// Return safe config values only (no password hash, jwt secret)
	return success(c, map[string]interface{}{
		"language":                        config.GetString("language"),
		"default_provider":                config.GetString("default_provider"),
		"provider_enabled_claude":         config.GetBool("provider_enabled_claude"),
		"provider_enabled_opencode":       config.GetBool("provider_enabled_opencode"),
		"provider_enabled_pi":             config.GetBool("provider_enabled_pi"),
		"provider_enabled_grok":           config.GetBool("provider_enabled_grok"),
		"provider_enabled_embedded":       config.GetBool("provider_enabled_embedded"),
		"workspace_path":                  config.GetString("workspace_path"),
		"server_port":                     config.GetInt("server_port"),
		"server_host":                     config.GetString("server_host"),
		"max_sheep":                       config.GetInt("max_sheep"),
		"max_concurrent_tasks":            config.GetInt("max_concurrent_tasks"),
		"concurrency_limits":              config.GetConcurrencyLimits(),
		"auto_approve":                    config.GetBool("auto_approve"),
		"session_reuse":                   config.GetBool("session_reuse"),
		"include_task_history":            config.GetBool("include_task_history"),
		"include_mcp_guide":               config.GetBool("include_mcp_guide"),
		"include_sheep_memory":            config.GetBool("include_sheep_memory"),
		"sheep_memory_prompt":             config.GetString("sheep_memory_prompt"),
		"enable_file_browser":             config.GetBool("enable_file_browser"),
		"custom_prompt_claude":            config.GetString("custom_prompt_claude"),
		"custom_prompt_opencode":          config.GetString("custom_prompt_opencode"),
		"custom_prompt_pi":                config.GetString("custom_prompt_pi"),
		"custom_prompt_grok":              config.GetString("custom_prompt_grok"),
		"opencode_compact_prompt":         config.GetBool("opencode_compact_prompt"),
		"opencode_thinking_default":       config.GetBool("opencode_thinking_default"),
		"opencode_thinking_proxy_enabled": config.GetBool("opencode_thinking_proxy_enabled"),
		"opencode_thinking_proxy_port":    config.GetInt("opencode_thinking_proxy_port"),
		"opencode_thinking_proxy_target":  config.GetString("opencode_thinking_proxy_target"),
		"opencode_thinking_model":         config.GetString("opencode_thinking_model"),
		"model_claude":                    config.GetString("model_claude"),
		"model_opencode":                  config.GetString("model_opencode"),
		"model_pi":                        config.GetString("model_pi"),
		"model_grok":                      config.GetString("model_grok"),
		"task_timeout":                    config.GetString("task_timeout"),
		"wiki_enabled":                    config.GetBool("wiki_enabled"),
		"wiki_auto_ingest":                config.GetBool("wiki_auto_ingest"),
		"wiki_max_context_pages":          config.GetInt("wiki_max_context_pages"),
		"wiki_max_page_content_chars":     config.GetInt("wiki_max_page_content_chars"),
		"discord_notifications_enabled":   config.GetBool("discord_notifications_enabled"),
		"discord_webhook_url":             config.GetString("discord_webhook_url"),
		"discord_notify_on_complete":      config.GetBool("discord_notify_on_complete"),
		"discord_notify_on_fail":          config.GetBool("discord_notify_on_fail"),
		"embedded_active_id":              config.GetString("embedded_active_id"),
		"custom_prompt_embedded":          config.GetString("custom_prompt_embedded"),
	})
}

// GET /api/config/model-options
// Returns selectable models for each provider. Claude options are a curated
// hard-coded list (CLI aliases + pinned versions), OpenCode options come from
// the user's ~/.config/opencode/config.json, and Pi options come from
// ~/.pi/agent/models.json.
func (s *Server) handleGetModelOptions(c *fiber.Ctx) error {
	type option struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}

	claude := []option{
		{ID: "", Label: "CLI default"},
		{ID: "opus", Label: "opus (alias — latest Opus)"},
		{ID: "sonnet", Label: "sonnet (alias — latest Sonnet)"},
		{ID: "haiku", Label: "haiku (alias — latest Haiku)"},
		{ID: "claude-fable-5", Label: "claude-fable-5"},
		{ID: "claude-opus-4-8", Label: "claude-opus-4-8"},
		{ID: "claude-opus-4-7", Label: "claude-opus-4-7"},
		{ID: "claude-sonnet-4-6", Label: "claude-sonnet-4-6"},
		{ID: "claude-haiku-4-5", Label: "claude-haiku-4-5"},
	}

	opencode := []option{{ID: "", Label: "OpenCode config default"}}
	for _, m := range config.ListOpenCodeModels() {
		opencode = append(opencode, option{ID: m.ID, Label: m.Label})
	}

	pi := []option{{ID: "", Label: "Pi config default"}}
	for _, m := range config.ListPiModels() {
		pi = append(pi, option{ID: m.ID, Label: m.Label})
	}

	grok := []option{{ID: "", Label: "grok default (grok-4.5)"}}
	for _, m := range config.ListGrokModels() {
		grok = append(grok, option{ID: m.ID, Label: m.Label})
	}

	// Embedded options are the configured endpoints. The "Default" entry
	// (empty ID) falls back to the globally active endpoint.
	embedded := []option{{ID: "", Label: "Active endpoint"}}
	if cfg, err := config.LoadEmbeddedConfig(); err == nil {
		for _, ep := range cfg.Endpoints {
			if !ep.Enabled {
				continue
			}
			label := ep.Label
			if label == "" {
				label = ep.ID
			}
			if ep.Model != "" {
				label = label + " (" + ep.Model + ")"
			}
			embedded = append(embedded, option{ID: ep.ID, Label: label})
		}
	}

	return success(c, map[string]interface{}{
		"claude":   claude,
		"opencode": opencode,
		"pi":       pi,
		"grok":     grok,
		"embedded": embedded,
	})
}

// GET /api/config/system-prompt-preview?sheep=<name>
// Returns the system prompt that would be injected into task prompts,
// so the user can inspect tool lists, task-history injection, skill summaries,
// and their own custom_prompt all rendered together.
func (s *Server) handleSystemPromptPreview(c *fiber.Ctx) error {
	sheepName := c.Query("sheep", "")
	return success(c, worker.PreviewSystemPrompt(sheepName))
}

// PATCH /api/config
func (s *Server) handleUpdateConfig(c *fiber.Ctx) error {
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	// Whitelist of updatable config keys
	allowed := map[string]bool{
		"language":                        true,
		"default_provider":                true,
		"provider_enabled_claude":         true,
		"provider_enabled_opencode":       true,
		"provider_enabled_pi":             true,
		"provider_enabled_grok":           true,
		"provider_enabled_embedded":       true,
		"workspace_path":                  true,
		"max_sheep":                       true,
		"max_concurrent_tasks":            true,
		"concurrency_limits":              true,
		"auto_approve":                    true,
		"session_reuse":                   true,
		"include_task_history":            true,
		"include_mcp_guide":               true,
		"include_sheep_memory":            true,
		"sheep_memory_prompt":             true,
		"enable_file_browser":             true,
		"custom_prompt_claude":            true,
		"custom_prompt_opencode":          true,
		"custom_prompt_pi":                true,
		"custom_prompt_grok":              true,
		"opencode_compact_prompt":         true,
		"opencode_thinking_default":       true,
		"opencode_thinking_proxy_enabled": true,
		"opencode_thinking_proxy_port":    true,
		"opencode_thinking_proxy_target":  true,
		"opencode_thinking_model":         true,
		"model_claude":                    true,
		"model_opencode":                  true,
		"model_pi":                        true,
		"model_grok":                      true,
		"task_timeout":                    true,
		"wiki_enabled":                    true,
		"wiki_auto_ingest":                true,
		"wiki_max_context_pages":          true,
		"wiki_max_page_content_chars":     true,
		"discord_notifications_enabled":   true,
		"discord_webhook_url":             true,
		"discord_notify_on_complete":      true,
		"discord_notify_on_fail":          true,
		"embedded_active_id":              true,
		"custom_prompt_embedded":          true,
	}

	for key, value := range body {
		if !allowed[key] {
			continue
		}

		// concurrency_limits is a {group: limit} map. Normalize to a clean
		// map of positive ints so the config file doesn't accumulate zeros,
		// floats, or non-numeric junk from the client.
		if key == "concurrency_limits" {
			value = normalizeConcurrencyLimits(value)
		}

		if err := config.Set(key, value); err != nil {
			return fail(c, fiber.StatusInternalServerError, "failed to save config: "+err.Error())
		}

		// Apply language change immediately
		if key == "language" {
			if lang, ok := value.(string); ok {
				i18n.Init(lang)
			}
		}
	}

	return success(c, nil)
}

// normalizeConcurrencyLimits coerces a client-supplied {group: limit} map into
// a clean map[string]int containing only positive limits. Non-map input, blank
// keys, and zero/negative/non-numeric values are dropped (a missing or zero
// entry means "no group limit"). Returns an empty map so the stored value is
// always a map, never nil.
func normalizeConcurrencyLimits(value interface{}) map[string]int {
	out := map[string]int{}
	m, ok := value.(map[string]interface{})
	if !ok {
		return out
	}
	for k, v := range m {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		n := 0
		switch t := v.(type) {
		case float64:
			n = int(t)
		case int:
			n = t
		case int64:
			n = int(t)
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(t))
			if err != nil {
				continue
			}
			n = parsed
		}
		if n > 0 {
			out[k] = n
		}
	}
	return out
}

// POST /api/system/restart
func (s *Server) handleRestart(c *fiber.Ctx) error {
	// Respond before restarting
	err := success(c, map[string]interface{}{"restarting": true})

	go func() {
		// Wait for response to be sent
		time.Sleep(500 * time.Millisecond)

		exe, exeErr := os.Executable()
		if exeErr != nil {
			return
		}

		// Close all SSE connections first to unblock Shutdown()
		s.hub.CloseAll()

		// Shutdown current server to release the port
		s.Shutdown()

		// Small delay to ensure port is released
		time.Sleep(200 * time.Millisecond)

		// Start new process detached from current session
		child := exec.Command(exe, "serve-foreground")
		child.Stdout = nil
		child.Stderr = nil
		child.Stdin = nil
		detachProcess(child)
		if startErr := child.Start(); startErr != nil {
			// If start fails, exit anyway (user can restart manually)
			os.Exit(1)
		}

		os.Exit(0)
	}()

	return err
}

// GET /api/config/embedded
// Returns all embedded endpoints from embedded.yaml.
func (s *Server) handleGetEmbeddedEndpoints(c *fiber.Ctx) error {
	cfg, err := config.LoadEmbeddedConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to load embedded config: "+err.Error())
	}
	activeID := config.GetString("embedded_active_id")

	result := make([]map[string]interface{}, 0, len(cfg.Endpoints))
	for _, ep := range cfg.Endpoints {
		result = append(result, map[string]interface{}{
			"id":             ep.ID,
			"label":          ep.Label,
			"base_url":       ep.BaseURL,
			"api_key":        maskAPIKey(ep.APIKey),
			"model":          ep.Model,
			"enabled":        ep.Enabled,
			"thinking":       ep.Thinking,
			"vision":         ep.Vision,
			"max_iterations": ep.MaxIterations,
			"context_tokens": ep.ContextTokens,
			"max_concurrent": ep.MaxConcurrent,
			"is_active":      ep.ID == activeID,
		})
	}
	return success(c, map[string]interface{}{
		"endpoints":          result,
		"embedded_active_id": activeID,
	})
}

// POST /api/config/embedded
// Creates a new embedded endpoint.
func (s *Server) handleCreateEmbeddedEndpoint(c *fiber.Ctx) error {
	var body config.EmbeddedEndpointJSON
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if body.ID == "" || body.BaseURL == "" || body.Model == "" {
		return fail(c, fiber.StatusBadRequest, "id, base_url, and model are required")
	}

	cfg, err := config.LoadEmbeddedConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to load embedded config: "+err.Error())
	}

	// Check for duplicate ID
	for _, ep := range cfg.Endpoints {
		if ep.ID == body.ID {
			return fail(c, fiber.StatusConflict, "endpoint ID already exists: "+body.ID)
		}
	}

	ep := embeddedEndpointFromJSON(body)
	cfg.Endpoints = append(cfg.Endpoints, ep)

	if err := config.SaveEmbeddedConfig(cfg); err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to save embedded config: "+err.Error())
	}

	// Sync llmslots registry with the new endpoint configuration.
	syncEndpointSemaphores()

	return success(c, map[string]interface{}{"message": "endpoint created"})
}

// PUT /api/config/embedded/:id
// Updates an existing embedded endpoint.
func (s *Server) handleUpdateEmbeddedEndpoint(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return fail(c, fiber.StatusBadRequest, "endpoint ID is required")
	}

	var body config.EmbeddedEndpointJSON
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	cfg, err := config.LoadEmbeddedConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to load embedded config: "+err.Error())
	}

	for i, ep := range cfg.Endpoints {
		if ep.ID == id {
			updated := embeddedEndpointFromJSON(body)
			updated.ID = id
			// Preserve the stored API key when the client sends back the
			// masked placeholder (or an empty value). The edit form is
			// populated from the masked GET response, so without this guard
			// the real key would be overwritten with "Fi2M****243c", causing
			// 401 Invalid API Key at inference time.
			if body.APIKey == "" || body.APIKey == maskAPIKey(ep.APIKey) {
				updated.APIKey = ep.APIKey
			}
			cfg.Endpoints[i] = updated

			if err := config.SaveEmbeddedConfig(cfg); err != nil {
				return fail(c, fiber.StatusInternalServerError, "failed to save embedded config: "+err.Error())
			}

			// Sync llmslots registry with the updated endpoint configuration.
			syncEndpointSemaphores()

			return success(c, map[string]interface{}{"message": "endpoint updated"})
		}
	}

	return fail(c, fiber.StatusNotFound, "endpoint not found: "+id)
}

// DELETE /api/config/embedded/:id
// Deletes an embedded endpoint.
func (s *Server) handleDeleteEmbeddedEndpoint(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return fail(c, fiber.StatusBadRequest, "endpoint ID is required")
	}

	cfg, err := config.LoadEmbeddedConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to load embedded config: "+err.Error())
	}

	activeID := config.GetString("embedded_active_id")
	if activeID == id {
		return fail(c, fiber.StatusBadRequest, "cannot delete the active endpoint")
	}

	for i, ep := range cfg.Endpoints {
		if ep.ID == id {
			cfg.Endpoints = append(cfg.Endpoints[:i], cfg.Endpoints[i+1:]...)

			if err := config.SaveEmbeddedConfig(cfg); err != nil {
				return fail(c, fiber.StatusInternalServerError, "failed to save embedded config: "+err.Error())
			}
			return success(c, map[string]interface{}{"message": "endpoint deleted"})
		}
	}

	return fail(c, fiber.StatusNotFound, "endpoint not found: "+id)
}

// POST /api/config/embedded/:id/set-active
// Sets the active embedded endpoint.
func (s *Server) handleSetActiveEndpoint(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return fail(c, fiber.StatusBadRequest, "endpoint ID is required")
	}

	cfg, err := config.LoadEmbeddedConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to load embedded config: "+err.Error())
	}
	found := false
	for _, ep := range cfg.Endpoints {
		if ep.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fail(c, fiber.StatusNotFound, "endpoint not found: "+id)
	}

	if err := config.Set("embedded_active_id", id); err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to save config: "+err.Error())
	}

	return success(c, map[string]interface{}{"message": "active endpoint set", "id": id})
}

// POST /api/config/embedded/test
// Tests the connection to an embedded endpoint.
func (s *Server) handleTestEmbeddedEndpoint(c *fiber.Ctx) error {
	var body struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Model   string `json:"model"`
	}
	if err := c.BodyParser(&body); err != nil || body.BaseURL == "" {
		return fail(c, fiber.StatusBadRequest, "base_url is required")
	}
	if body.Model == "" {
		return fail(c, fiber.StatusBadRequest, "model is required")
	}

	ep := &embedded.Endpoint{
		ID:      "test",
		BaseURL: body.BaseURL,
		APIKey:  body.APIKey,
		Model:   body.Model,
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	if err := embedded.TestConnection(ctx, ep); err != nil {
		return fail(c, fiber.StatusBadGateway, "connection test failed: "+err.Error())
	}

	return success(c, map[string]interface{}{
		"message":  "connection successful",
		"base_url": body.BaseURL,
	})
}

// maskAPIKey masks an API key for display.
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// embeddedEndpointFromJSON converts JSON-friendly input to config.EmbeddedEndpoint.
func embeddedEndpointFromJSON(body config.EmbeddedEndpointJSON) config.EmbeddedEndpoint {
	return config.EmbeddedEndpoint{
		ID:            body.ID,
		Label:         body.Label,
		BaseURL:       body.BaseURL,
		APIKey:        body.APIKey,
		Model:         body.Model,
		Enabled:       body.Enabled,
		Thinking:      body.Thinking,
		Vision:        body.Vision,
		MaxIterations: body.MaxIterations,
		ContextTokens: body.ContextTokens,
		MaxConcurrent: body.MaxConcurrent,
	}
}

// GET /api/config/magi
// Returns the magi consensus config from embedded.yaml, plus validation
// errors and warnings (design §7, §2.2).
func (s *Server) handleGetMagiConfig(c *fiber.Ctx) error {
	cfg, err := config.LoadEmbeddedConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to load embedded config: "+err.Error())
	}

	errs, warnings := config.ValidateMagiConfig(cfg)

	if cfg.Magi == nil {
		return success(c, fiber.Map{
			"magi":     nil,
			"errors":   errs,
			"warnings": warnings,
		})
	}

	config.ApplyMagiDefaults(cfg.Magi)
	return success(c, fiber.Map{
		"magi":     cfg.Magi,
		"errors":   errs,
		"warnings": warnings,
	})
}

// PUT /api/config/magi
// Updates the magi section of embedded.yaml. Hard validation errors block
// the save (400); warnings are advisory and do not block (design §2.2).
func (s *Server) handleUpdateMagiConfig(c *fiber.Ctx) error {
	var magi config.MagiConfig
	if err := c.BodyParser(&magi); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	config.ApplyMagiDefaults(&magi)

	// Load full embedded config to validate magi in context (endpoints must exist).
	cfg, err := config.LoadEmbeddedConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to load embedded config: "+err.Error())
	}
	cfg.Magi = &magi

	errs, warnings := config.ValidateMagiConfig(cfg)
	if len(errs) > 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"message": "magi config validation failed",
			"errors":  errs,
		})
	}

	if err := config.SaveMagiConfig(&magi); err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to save magi config: "+err.Error())
	}

	return success(c, fiber.Map{
		"ok":       true,
		"warnings": warnings,
	})
}
