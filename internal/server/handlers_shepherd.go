package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/i18n"
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
	modelOptions, modelSelectable := modelOptionsFor(provider, i18n.T().Server.ProviderDefaultModel)

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
		// Attention thresholds (#599). attention.go treats <=0 as its built-in
		// default (120 / 60), and the settings UI shows the same fallbacks.
		"stuck_minutes":         config.GetInt("shepherd_stuck_minutes"),
		"pending_stale_minutes": config.GetInt("shepherd_pending_stale_minutes"),
		// Patrol (#600). The raw interval string ("30m"; "0" = off, nonzero
		// clamped to a 5-minute floor by manager.PatrolInterval) and the wake
		// switch (absent = true, matching patrol.go).
		"patrol_interval": patrolIntervalSetting(),
		"patrol_wake":     patrolWakeSetting(),
		// Autonomy (#601). Effective values, not raw keys: an unset or unknown
		// shepherd_autonomy resolves to "execute", and a non-positive cap falls
		// back inside manager — the form must show what is actually enforced.
		"autonomy":               manager.AutonomyTier(),
		"max_active_delegations": manager.MaxActiveDelegations(),
		"daily_cost_limit_usd":   manager.DailyCostLimitUSD(),
		// Report notifications (#602). shepherd_notify and quiet_patrol default
		// to true, mute_delegated to false; the two true-defaults are read the
		// way patrol.go reads patrol_wake (absent = the documented default),
		// because tests reset viper.
		"notify":                       boolSettingDefaultTrue("shepherd_notify"),
		"notify_quiet_patrol":          boolSettingDefaultTrue("shepherd_notify_quiet_patrol"),
		"mute_delegated_notifications": config.GetBool("shepherd_mute_delegated_notifications"),
	}, nil
}

// boolSettingDefaultTrue reads a bool key whose absent value means true, so a
// test that resets viper still sees the documented default (#602).
func boolSettingDefaultTrue(key string) bool {
	if v := config.Get(key); v == nil {
		return true
	}
	return config.GetBool(key)
}

// patrolIntervalSetting returns the stored raw string, or the documented
// default when unset — the settings form needs a visible value.
func patrolIntervalSetting() string {
	if v := config.Get("shepherd_patrol_interval"); v != nil {
		return strings.TrimSpace(config.GetString("shepherd_patrol_interval"))
	}
	return "30m"
}

// patrolWakeSetting mirrors manager.patrolWakeEnabled's unset-means-true rule.
func patrolWakeSetting() bool {
	if v := config.Get("shepherd_patrol_wake"); v == nil {
		return true
	}
	return config.GetBool("shepherd_patrol_wake")
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
		// Attention thresholds (#599). Absent = leave alone.
		StuckMinutes        *int `json:"stuck_minutes"`
		PendingStaleMinutes *int `json:"pending_stale_minutes"`
		// Patrol (#600). patrol_interval is the raw duration string ("30m",
		// "0" = off); patrol_wake is the wake switch. Absent = leave alone.
		PatrolInterval *string `json:"patrol_interval"`
		PatrolWake     *bool   `json:"patrol_wake"`
		// Autonomy (#601). autonomy is one of report/plan/execute/full; the two
		// caps are 0 = "use the built-in default / no limit". Absent = alone.
		Autonomy             *string  `json:"autonomy"`
		MaxActiveDelegations *int     `json:"max_active_delegations"`
		DailyCostLimitUSD    *float64 `json:"daily_cost_limit_usd"`
		// Report notifications (#602). Absent = leave alone.
		Notify                     *bool `json:"notify"`
		NotifyQuietPatrol          *bool `json:"notify_quiet_patrol"`
		MuteDelegatedNotifications *bool `json:"mute_delegated_notifications"`
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

	// Attention thresholds (#599). Same 0-means-default rule as the wake caps.
	if body.StuckMinutes != nil {
		if *body.StuckMinutes < 0 {
			return fail(c, fiber.StatusBadRequest, "stuck_minutes must be >= 0 (0 = default 120)")
		}
		values["shepherd_stuck_minutes"] = *body.StuckMinutes
	}
	if body.PendingStaleMinutes != nil {
		if *body.PendingStaleMinutes < 0 {
			return fail(c, fiber.StatusBadRequest, "pending_stale_minutes must be >= 0 (0 = default 60)")
		}
		values["shepherd_pending_stale_minutes"] = *body.PendingStaleMinutes
	}

	// Patrol (#600). The interval is stored as the raw Go duration string so
	// GET shows exactly what was typed; "0" turns patrolling off, and any
	// other value must parse — manager.PatrolInterval raises it to the 5m
	// floor at read time, so no clamping here (the form keeps its value).
	if body.PatrolInterval != nil {
		raw := strings.TrimSpace(*body.PatrolInterval)
		if raw == "" {
			return fail(c, fiber.StatusBadRequest, "patrol_interval must be a duration like \"30m\" (\"0\" = off)")
		}
		if d, err := time.ParseDuration(raw); err != nil || d < 0 {
			return fail(c, fiber.StatusBadRequest, "patrol_interval must be a duration like \"30m\" (\"0\" = off)")
		}
		values["shepherd_patrol_interval"] = raw
	}
	if body.PatrolWake != nil {
		values["shepherd_patrol_wake"] = *body.PatrolWake
	}

	// Autonomy (#601). Only the four named tiers are accepted — manager.
	// AutonomyTier would silently fall an unknown value back to "execute", so
	// the API rejects typos instead of storing a value that means something
	// else at read time. The caps follow the same 0-means-default convention
	// as the wake caps: negatives are rejected, 0 on the cost key means
	// "no limit" (its documented default), 0 on delegations falls back to 3.
	if body.Autonomy != nil {
		tier := strings.ToLower(strings.TrimSpace(*body.Autonomy))
		switch tier {
		case manager.AutonomyReport, manager.AutonomyPlan, manager.AutonomyExecute, manager.AutonomyFull:
			values["shepherd_autonomy"] = tier
		default:
			return fail(c, fiber.StatusBadRequest, "autonomy must be one of report, plan, execute, full")
		}
	}
	if body.MaxActiveDelegations != nil {
		if *body.MaxActiveDelegations < 0 {
			return fail(c, fiber.StatusBadRequest, "max_active_delegations must be >= 0 (0 = default 3)")
		}
		values["shepherd_max_active_delegations"] = *body.MaxActiveDelegations
	}
	if body.DailyCostLimitUSD != nil {
		if *body.DailyCostLimitUSD < 0 {
			return fail(c, fiber.StatusBadRequest, "daily_cost_limit_usd must be >= 0 (0 = no limit)")
		}
		values["shepherd_daily_cost_limit_usd"] = *body.DailyCostLimitUSD
	}

	// Report notifications (#602). Plain booleans; absent = leave alone.
	if body.Notify != nil {
		values["shepherd_notify"] = *body.Notify
	}
	if body.NotifyQuietPatrol != nil {
		values["shepherd_notify_quiet_patrol"] = *body.NotifyQuietPatrol
	}
	if body.MuteDelegatedNotifications != nil {
		values["shepherd_mute_delegated_notifications"] = *body.MuteDelegatedNotifications
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

// --- Shepherd status API (#598) ---
//
// GET /api/shepherd/status — the payload the /shepherd console header renders:
// manager provider + status, workspace state, today's shepherd task count and
// cost, circuit-breaker state, and the running-task info (id + whether the
// provider supports mid-run injection) that drives the Stop/Inject buttons.
// It deliberately overlaps GET /api/shepherd/config: the console should need
// one cheap call per refresh, not the full settings payload with scope lists.

// shepherdInjectSupported reports whether a provider's runs accept mid-run
// prompt injection today. embedded always wires an InjectCh; claude does only
// in the auto_approve streaming path (executeWithStreaming, stream-json stdin).
// opencode/pi/grok spawn one-shot CLIs with no stdin channel.
func shepherdInjectSupported(provider string) bool {
	switch provider {
	case "embedded":
		return true
	case "claude":
		return config.GetBool("auto_approve")
	default:
		return false
	}
}

// GET /api/shepherd/status
func (s *Server) handleGetShepherdStatus(c *fiber.Ctx) error {
	mgr, err := worker.GetOrCreateManager()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	workspace := config.GetString("workspace_path")
	normalized, wsErr := config.ValidateWorkspacePath(workspace)

	todayCount, todayCost, err := queue.ShepherdTodayStats()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	runningID, injectable, running := worker.GetRunningTaskID(names.ManagerName)

	// The sheep row is the durable circuit-breaker state (same threshold the
	// dispatcher gates on in processor.dispatchShepherd).
	tripped := mgr.ConsecutiveFailures >= queue.CircuitBreakerThreshold

	// A working manager with no live in-memory task is a stale status (daemon
	// restart mid-run); report it as idle so the console does not show a
	// phantom "working" with no Stop target — same auto-heal the dispatcher
	// applies before dispatching.
	status := string(mgr.Status)
	if status == "working" && !running {
		status = "idle"
	}

	payload := map[string]interface{}{
		"provider":             string(mgr.Provider),
		"model":                config.GetString("shepherd_model"),
		"status":               status,
		"workspace_path":       normalized,
		"workspace_valid":      wsErr == nil && normalized != "",
		"workspace_error":      workspaceErrorText(wsErr),
		"today_task_count":     todayCount,
		"today_cost_usd":       todayCost,
		"circuit_tripped":      tripped,
		"consecutive_failures": mgr.ConsecutiveFailures,
		"inject_supported":     shepherdInjectSupported(string(mgr.Provider)),
	}
	if running {
		payload["running_task_id"] = runningID
		payload["inject_available"] = injectable
	}

	// Patrol card (#600): when the next automatic pass is due. The baseline is
	// the newest patrol record (a manual "지금 순찰" reschedules the auto pass
	// too), and an off interval reports patrol_enabled=false with no time.
	if next, ok := manager.NextPatrolAt(time.Now()); ok {
		payload["patrol_enabled"] = true
		payload["next_patrol_at"] = next.Format("2006-01-02 15:04:05")
		payload["patrol_interval"] = manager.PatrolInterval().String()
	} else {
		payload["patrol_enabled"] = false
	}

	// Autonomy card (#601): the tier in force, both caps, and the live numbers
	// they gate on. today_cost is the *combined* window the cost cap bills
	// against (shepherd tasks + watched-issue tasks since local midnight) — it
	// differs from today_cost_usd above, which counts shepherd tasks only.
	// A failed query degrades to zeros with exceeded=false rather than failing
	// the whole status call: the console header must keep rendering.
	payload["autonomy_tier"] = manager.AutonomyTier()
	payload["max_active_delegations"] = manager.MaxActiveDelegations()
	if active, err := manager.ActiveDelegations(); err == nil {
		payload["active_delegations"] = active
	} else {
		payload["active_delegations"] = 0
	}
	if exceeded, spent, limit, err := manager.CostLimitStatus(time.Now()); err == nil {
		payload["today_cost"] = spent
		payload["daily_cost_limit_usd"] = limit
		payload["cost_limit_exceeded"] = exceeded
	} else {
		payload["today_cost"] = 0.0
		payload["daily_cost_limit_usd"] = manager.DailyCostLimitUSD()
		payload["cost_limit_exceeded"] = false
	}

	return success(c, payload)
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
//
// Filters: kind, status, q (prompt/summary substring — same fields as GET
// /api/tasks), plus pagination page (1-based) and limit (#623). The data
// object carries total/total_pages/page next to the tasks array; callers that
// only send `limit` (the console's "recent task" link sends limit=1) keep
// working unchanged.
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
	if limit > 100 {
		limit = 100 // the screen lists a page, not the whole table
	}

	// page is strict (#623): 0, negatives, and non-numbers are a client error.
	page := 1
	if raw := strings.TrimSpace(c.Query("page")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return fail(c, fiber.StatusBadRequest, "page must be a positive integer")
		}
		page = n
	}

	q := strings.TrimSpace(c.Query("q"))

	filter := queue.ShepherdTaskFilter{Kind: kind, Status: status, Query: q}

	total, err := queue.CountShepherdTasks(filter)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	filter.Offset = (page - 1) * limit
	filter.Limit = limit

	tasks, err := queue.ListShepherdTasksFiltered(filter)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	totalPages := total / limit
	if total%limit > 0 {
		totalPages++
	}
	if totalPages < 1 {
		totalPages = 1
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
		"tasks":       items,
		"count":       len(items),
		"total":       total,
		"total_pages": totalPages,
		"page":        page,
	})
}
