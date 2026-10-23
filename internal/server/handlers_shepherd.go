package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// Shepherd settings API (#593).
//
// GET   /api/shepherd/config  — current workspace / provider / model /
//                               instructions / management scope
// PATCH /api/shepherd/config  — validate and save the same fields
//
// The provider is NOT a config key: it is the manager sheep's `provider`
// column, because ExecuteInteractive selects the provider from that row
// (internal/worker/interactive.go `switch s.Provider`). Everything else lives
// in config.yaml under shepherd_* (workspace_path is the existing shared key).

// shepherdScopeItem is one project row in the management-scope checklist.
type shepherdScopeItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	InWorkspace bool   `json:"in_workspace"`
	Hidden      bool   `json:"hidden"`
	Excluded    bool   `json:"excluded"`
}

// buildShepherdConfig renders the payload both handlers share. The project
// list covers every registered project — hidden ones and excluded ones too —
// with flags, so the checklist can re-check a project the user unchecked in a
// previous session instead of losing it from the UI. Scope itself is computed
// by manager.ScopeProjects, which later stages (prompts, issue board, patrol)
// also use.
func buildShepherdConfig() (map[string]interface{}, error) {
	mgr, err := worker.GetOrCreateManager()
	if err != nil {
		return nil, err
	}

	workspace := config.GetString("workspace_path")
	normalized, workspaceErr := config.ValidateWorkspacePath(workspace)
	workspaceValid := workspaceErr == nil

	excluded := make(map[string]bool)
	for _, name := range config.GetSheepScopeExclude() {
		excluded[name] = true
	}

	allProjects, err := project.List()
	if err != nil {
		return nil, err
	}
	scopeItems := make([]shepherdScopeItem, 0, len(allProjects))
	for _, p := range allProjects {
		scopeItems = append(scopeItems, shepherdScopeItem{
			Name:        p.Name,
			Path:        p.Path,
			InWorkspace: manager.InWorkspace(normalized, p.Path),
			Hidden:      p.Hidden,
			Excluded:    excluded[p.Name],
		})
	}

	inScope, err := manager.ScopeProjects()
	if err != nil {
		return nil, err
	}
	inScopeNames := make([]string, 0, len(inScope))
	for _, p := range inScope {
		inScopeNames = append(inScopeNames, p.Name)
	}

	provider := string(mgr.Provider)
	modelOptions, modelSelectable := modelOptionsFor(provider, "프로바이더 기본값")

	return map[string]interface{}{
		"workspace_path":        normalized,
		"workspace_valid":       workspaceValid,
		"workspace_error":       workspaceErrorText(workspaceErr),
		"workspace_unset":       normalized == "",
		"provider":              provider,
		"model":                 config.GetString("shepherd_model"),
		"model_options":         modelOptions,
		"model_selectable":      modelSelectable,
		"instructions":          config.GetString("shepherd_instructions"),
		"scope_exclude":         config.GetSheepScopeExclude(),
		"scope_include_hidden":  config.GetBool("shepherd_scope_include_hidden"),
		"scope_projects":        scopeItems,
		"scope_active_projects": inScopeNames,
		// Wake caps (#597). The raw config value; wake.go treats <=0 as its
		// built-in default (3 / 30), and the UI shows the same fallbacks.
		"max_review_rounds":   config.GetInt("shepherd_max_review_rounds"),
		"max_wakeups_per_day": config.GetInt("shepherd_max_wakeups_per_day"),
	}, nil
}

// workspaceErrorText turns a validation error into a UI string ("" when ok).
func workspaceErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// GET /api/shepherd/config
func (s *Server) handleGetShepherdConfig(c *fiber.Ctx) error {
	payload, err := buildShepherdConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}
	return success(c, payload)
}

// PATCH /api/shepherd/config
func (s *Server) handleUpdateShepherdConfig(c *fiber.Ctx) error {
	// Pointer fields mean "absent = leave alone", so a partial save from the UI
	// can't silently blank out the other shepherd_* keys.
	var body struct {
		WorkspacePath      *string   `json:"workspace_path"`
		Provider           *string   `json:"provider"`
		Model              *string   `json:"model"`
		Instructions       *string   `json:"instructions"`
		ScopeExclude       *[]string `json:"scope_exclude"`
		ScopeIncludeHidden *bool     `json:"scope_include_hidden"`
		// Wake caps (#597). Absent = leave alone.
		MaxReviewRounds  *int `json:"max_review_rounds"`
		MaxWakeupsPerDay *int `json:"max_wakeups_per_day"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	// The manager row is the provider store; create it on first use so a fresh
	// daemon can save settings before anything has run.
	mgr, err := worker.GetOrCreateManager()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	values := map[string]interface{}{}

	if body.WorkspacePath != nil {
		normalized, err := config.ValidateWorkspacePath(*body.WorkspacePath)
		if err != nil {
			return fail(c, fiber.StatusBadRequest, err.Error())
		}
		values["workspace_path"] = normalized
	}

	if body.Instructions != nil {
		values["shepherd_instructions"] = *body.Instructions
	}
	if body.ScopeExclude != nil {
		values["shepherd_scope_exclude"] = *body.ScopeExclude
	}
	if body.ScopeIncludeHidden != nil {
		values["shepherd_scope_include_hidden"] = *body.ScopeIncludeHidden
	}

	// Wake caps (#597). 0 means "use wake.go's built-in default"; negatives are
	// rejected so a typo cannot silently disable or inflate the guard.
	if body.MaxReviewRounds != nil {
		if *body.MaxReviewRounds < 0 {
			return fail(c, fiber.StatusBadRequest, "max_review_rounds must be >= 0 (0 = default 3)")
		}
		values["shepherd_max_review_rounds"] = *body.MaxReviewRounds
	}
	if body.MaxWakeupsPerDay != nil {
		if *body.MaxWakeupsPerDay < 0 {
			return fail(c, fiber.StatusBadRequest, "max_wakeups_per_day must be >= 0 (0 = default 30)")
		}
		values["shepherd_max_wakeups_per_day"] = *body.MaxWakeupsPerDay
	}

	// Provider change: validate against the enabled-provider switches, then
	// write the manager row through worker.UpdateProvider — that call also
	// clears the session ID, since providers keep separate session systems.
	newProvider := string(mgr.Provider)
	if body.Provider != nil {
		provider := strings.TrimSpace(*body.Provider)
		if provider == "" {
			return fail(c, fiber.StatusBadRequest, "provider is required")
		}
		if !config.IsProviderEnabled(provider) {
			return fail(c, fiber.StatusBadRequest, "provider '"+provider+"' is disabled in settings")
		}
		if provider != newProvider {
			if err := worker.UpdateProvider(names.ManagerName, provider); err != nil {
				return fail(c, fiber.StatusBadRequest, err.Error())
			}
			newProvider = provider
		}
	}

	model := config.GetString("shepherd_model")
	if body.Model != nil {
		model = strings.TrimSpace(*body.Model)
		values["shepherd_model"] = model
	}

	// A model saved for the old provider is meaningless for the new one. When
	// the provider changed and the current value isn't offered by the new
	// provider's list (magi offers none), fall back to the provider default.
	if body.Provider != nil && newProvider != string(mgr.Provider) {
		opts, selectable := modelOptionsFor(newProvider, "")
		if !selectable {
			model = ""
			values["shepherd_model"] = ""
		} else if model != "" && !modelOptionHas(opts, model) {
			model = ""
			values["shepherd_model"] = ""
		}
	}

	if len(values) > 0 {
		if err := persistConfig(values); err != nil {
			return fail(c, fiber.StatusInternalServerError, "failed to save config: "+err.Error())
		}
	}

	if body.Provider != nil && newProvider != string(mgr.Provider) {
		// Same event shape as PATCH /api/sheep/:name/provider so the sheep UI
		// updates the manager chip too.
		s.hub.Broadcast(SSEEvent{Type: "provider_change", Data: map[string]interface{}{
			"sheep_name": names.ManagerName, "provider": newProvider,
		}})
	}

	payload, err := buildShepherdConfig()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}
	return success(c, payload)
}

// modelOptionHas reports whether a model ID appears in a provider's list.
func modelOptionHas(opts []modelOption, id string) bool {
	for _, o := range opts {
		if o.ID == id {
			return true
		}
	}
	return false
}

// --- Shepherd task API (#594) ---
//
// POST /api/shepherd/tasks {prompt} — enqueue a `command` shepherd task
// GET  /api/shepherd/tasks?kind=&status=&limit= — recent shepherd tasks
//
// POST refuses when workspace_path is unset or invalid: a shepherd task with
// no usable working directory would sit pending forever, so the operator should
// hear about the missing workspace now instead of later.

// shepherdWorkspaceForTasks returns the validated workspace or a UI/API error.
func shepherdWorkspaceForTasks() (string, error) {
	normalized, err := config.ValidateWorkspacePath(config.GetString("workspace_path"))
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return "", fmt.Errorf("workspace_path is not set: choose the shepherd workspace in Settings → Shepherd first")
	}
	return normalized, nil
}

// POST /api/shepherd/tasks
func (s *Server) handleCreateShepherdTask(c *fiber.Ctx) error {
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if strings.TrimSpace(body.Prompt) == "" {
		return fail(c, fiber.StatusBadRequest, "prompt is required")
	}

	if _, err := shepherdWorkspaceForTasks(); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	// User instructions run at priority 1 so a queued command is picked up
	// ahead of the automatic review/patrol tasks (priority 0).
	t, err := manager.EnqueueShepherdTask(manager.KindCommand, body.Prompt, 1)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, map[string]interface{}{
		"task_id":       t.ID,
		"shepherd_kind": t.ShepherdKind,
		"status":        string(t.Status),
		"priority":      t.Priority,
	})
}

// GET /api/shepherd/tasks
func (s *Server) handleListShepherdTasks(c *fiber.Ctx) error {
	kind := strings.TrimSpace(c.Query("kind"))
	if kind != "" && !manager.ValidKind(kind) {
		return fail(c, fiber.StatusBadRequest, "kind must be one of command, review, patrol")
	}

	status := strings.TrimSpace(c.Query("status"))
	if status != "" {
		switch status {
		case "pending", "running", "completed", "failed", "stopped":
		default:
			return fail(c, fiber.StatusBadRequest, "invalid status: "+status)
		}
	}

	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return fail(c, fiber.StatusBadRequest, "limit must be a positive integer")
		}
		limit = n
	}

	tasks, err := queue.ListShepherdTasks(kind, status, limit)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	items := make([]map[string]interface{}, 0, len(tasks))
	for _, t := range tasks {
		item := map[string]interface{}{
			"id":            t.ID,
			"shepherd_kind": t.ShepherdKind,
			"status":        string(t.Status),
			"prompt":        t.Prompt,
			"summary":       t.Summary,
			"error":         t.Error,
			"model":         t.Model,
			"cost_usd":      t.CostUsd,
			"created_at":    t.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if !t.StartedAt.IsZero() {
			item["started_at"] = t.StartedAt.Format("2006-01-02 15:04:05")
		}
		if !t.CompletedAt.IsZero() {
			item["completed_at"] = t.CompletedAt.Format("2006-01-02 15:04:05")
		}
		items = append(items, item)
	}

	return success(c, map[string]interface{}{
		"tasks": items,
		"count": len(items),
	})
}
