package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// withShepherdTestEnv gives the handlers an in-memory DB and keeps every config
// write in viper's override layer instead of touching ~/.shepherd/config.yaml.
func withShepherdTestEnv(t *testing.T) {
	t.Helper()

	withServerTestDB(t)

	viper.Reset()
	// viper.Reset() also drops the provider_enabled_* defaults set by
	// config.Init(), and IsProviderEnabled reads them — restore them so the
	// enabled-provider gate behaves like a running daemon.
	for _, p := range []string{"claude", "opencode", "pi", "grok", "embedded", "magi"} {
		viper.SetDefault("provider_enabled_"+p, true)
	}
	viper.SetDefault("workspace_path", "")
	viper.SetDefault("shepherd_model", "")
	viper.SetDefault("shepherd_instructions", "")
	viper.SetDefault("shepherd_scope_exclude", []string{})
	viper.SetDefault("shepherd_scope_include_hidden", false)

	orig := persistConfig
	persistConfig = func(values map[string]interface{}) error {
		for k, v := range values {
			viper.Set(k, v)
		}
		return nil
	}
	t.Cleanup(func() {
		persistConfig = orig
		viper.Reset()
	})
}

func newShepherdApp(t *testing.T) (*fiber.App, *Server) {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/shepherd/config", s.handleGetShepherdConfig)
	app.Patch("/api/shepherd/config", s.handleUpdateShepherdConfig)
	app.Get("/api/shepherd/status", s.handleGetShepherdStatus)
	// Task API (#594). s.processor stays nil in tests, so POST enqueues only —
	// no dispatch side effects.
	app.Post("/api/shepherd/tasks", s.handleCreateShepherdTask)
	app.Get("/api/shepherd/tasks", s.handleListShepherdTasks)
	return app, s
}

// shepherdApp is the shorthand for tests that do not need the hub.
func shepherdApp(t *testing.T) *fiber.App {
	t.Helper()
	app, _ := newShepherdApp(t)
	return app
}

func patchShepherdConfig(t *testing.T, app *fiber.App, body map[string]interface{}) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/shepherd/config", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return resp.StatusCode, out
}

func getShepherdConfig(t *testing.T, app *fiber.App) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/shepherd/config", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return resp.StatusCode, out
}

func TestHandleGetShepherdConfigReportsUnsetWorkspace(t *testing.T) {
	withShepherdTestEnv(t)

	code, payload := getShepherdConfig(t, shepherdApp(t))
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["workspace_path"] != "" {
		t.Errorf("workspace_path = %v, want empty", data["workspace_path"])
	}
	if data["workspace_valid"] != true {
		t.Errorf("empty workspace is 'not set', not invalid: %v", data["workspace_valid"])
	}
	if data["workspace_unset"] != true {
		t.Errorf("workspace_unset = %v, want true", data["workspace_unset"])
	}
	// The manager row is created on demand so a fresh daemon can save settings.
	if data["provider"] != string(sheep.ProviderClaude) {
		t.Errorf("provider = %v, want claude", data["provider"])
	}
	if data["model_selectable"] != true {
		t.Errorf("claude must offer a model list: %v", data["model_selectable"])
	}
}

func TestHandleUpdateShepherdConfigNormalizesWorkspace(t *testing.T) {
	withShepherdTestEnv(t)
	tmp := t.TempDir()

	code, payload := patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"workspace_path": tmp + "/",
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["workspace_path"] != tmp {
		t.Errorf("stored workspace = %v, want %q (trailing slash stripped)", data["workspace_path"], tmp)
	}
	if viper.GetString("workspace_path") != tmp {
		t.Errorf("config key = %q, want %q", viper.GetString("workspace_path"), tmp)
	}

	// A missing directory must be rejected, and the stored value must survive it.
	code, payload = patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"workspace_path": tmp + "/does-not-exist",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("missing dir: status = %d payload=%v", code, payload)
	}
	if viper.GetString("workspace_path") != tmp {
		t.Errorf("rejected save must not change the stored path: %q", viper.GetString("workspace_path"))
	}
}

func TestHandleUpdateShepherdConfigRejectsDisabledProvider(t *testing.T) {
	withShepherdTestEnv(t)
	viper.Set("provider_enabled_grok", false)

	code, payload := patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"provider": "grok",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d payload=%v, want 400", code, payload)
	}

	client := db.Client()
	got := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(context.Background())
	if string(got.Provider) != string(sheep.ProviderClaude) {
		t.Errorf("manager provider = %s, must stay claude", got.Provider)
	}
}

// TestHandleUpdateShepherdConfigProviderChangeResetsSessionAndModel pins the
// three side effects of a provider switch: the manager row's provider is
// rewritten, its session ID is cleared (providers keep separate session
// systems), and a model that the new provider does not offer is dropped.
//
// It switches to pi rather than opencode on purpose: ListOpenCodeModels would
// shell out to `opencode models` in the test binary, where the daemon's startup
// cache is never populated. Pi's list comes from a file, so the handler stays
// fast and hermetic.
func TestHandleUpdateShepherdConfigProviderChangeResetsSessionAndModel(t *testing.T) {
	withShepherdTestEnv(t)
	ctx := context.Background()
	client := db.Client()

	// The sheep UI listens for provider_change to refresh its provider chip, so
	// the shepherd tab must emit the same event shape as PATCH /api/sheep/:name.
	app, srv := newShepherdApp(t)
	events := subscribeHub(t, srv)

	client.Sheep.Update().Where(sheep.Name(names.ManagerName)).
		SetSessionID("stale-claude-session").
		SaveX(ctx)
	viper.Set("shepherd_model", "claude-opus-4-8")

	code, payload := patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"provider": "pi",
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}

	got := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)
	if string(got.Provider) != string(sheep.ProviderPi) {
		t.Errorf("manager provider = %s, want pi", got.Provider)
	}
	if got.SessionID != "" {
		t.Errorf("session_id = %q, want cleared", got.SessionID)
	}
	if viper.GetString("shepherd_model") != "" {
		t.Errorf("shepherd_model = %q, want reset (claude model is not a pi model)", viper.GetString("shepherd_model"))
	}

	data := payload["data"].(map[string]any)
	if data["provider"] != "pi" {
		t.Errorf("response provider = %v, want pi", data["provider"])
	}
	if data["model"] != "" {
		t.Errorf("response model = %v, want empty", data["model"])
	}

	if code, _ := patchShepherdConfig(t, app, map[string]interface{}{"provider": "claude"}); code != http.StatusOK {
		t.Fatalf("switch back: status = %d", code)
	}
	var saw bool
	for len(events) > 0 {
		ev := <-events
		if ev.Type != "provider_change" {
			continue
		}
		evData, _ := ev.Data.(map[string]interface{})
		if evData["sheep_name"] == names.ManagerName && evData["provider"] == "claude" {
			saw = true
		}
	}
	if !saw {
		t.Error("provider_change SSE for the manager sheep was not broadcast")
	}
}

// subscribeHub registers a throwaway SSE client so a handler's Broadcast call
// is observable in a test.
func subscribeHub(t *testing.T, s *Server) chan SSEEvent {
	t.Helper()
	ch := s.hub.Subscribe(t.Name())
	t.Cleanup(func() { s.hub.Unsubscribe(t.Name()) })
	return ch
}

// TestHandleUpdateShepherdConfigKeepsModelForSameProvider checks the opposite
// case: saving the same provider again must not wipe a valid model.
func TestHandleUpdateShepherdConfigKeepsModelForSameProvider(t *testing.T) {
	withShepherdTestEnv(t)

	patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"provider": "pi",
	})
	code, payload := patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"provider": "pi",
		"model":    "my-pi-model",
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}

	code, payload = patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"provider": "pi",
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["model"] != "my-pi-model" {
		t.Errorf("model = %v, want it kept across a same-provider save", data["model"])
	}
}

// TestHandleShepherdConfigNotificationFields (#602): GET reports the documented
// defaults when the keys are unset (viper.Reset in withShepherdTestEnv), and a
// PATCH round-trips all three switches.
func TestHandleShepherdConfigNotificationFields(t *testing.T) {
	withShepherdTestEnv(t)
	app := shepherdApp(t)

	// Defaults: notify on, quiet patrol on, mute delegated off.
	code, payload := getShepherdConfig(t, app)
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["notify"] != true {
		t.Errorf("notify default = %v, want true", data["notify"])
	}
	if data["notify_quiet_patrol"] != true {
		t.Errorf("notify_quiet_patrol default = %v, want true", data["notify_quiet_patrol"])
	}
	if data["mute_delegated_notifications"] != false {
		t.Errorf("mute_delegated_notifications default = %v, want false", data["mute_delegated_notifications"])
	}

	// Save the opposite of every default.
	code, _ = patchShepherdConfig(t, app, map[string]interface{}{
		"notify":                       false,
		"notify_quiet_patrol":          false,
		"mute_delegated_notifications": true,
	})
	if code != http.StatusOK {
		t.Fatalf("patch status = %d payload=%v", code, payload)
	}
	if viper.GetBool("shepherd_notify") {
		t.Error("shepherd_notify should be false after save")
	}
	if viper.GetBool("shepherd_notify_quiet_patrol") {
		t.Error("shepherd_notify_quiet_patrol should be false after save")
	}
	if !viper.GetBool("shepherd_mute_delegated_notifications") {
		t.Error("shepherd_mute_delegated_notifications should be true after save")
	}

	// GET reflects the saved values.
	code, payload = getShepherdConfig(t, app)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	data = payload["data"].(map[string]any)
	if data["notify"] != false {
		t.Errorf("notify = %v, want false", data["notify"])
	}
	if data["notify_quiet_patrol"] != false {
		t.Errorf("notify_quiet_patrol = %v, want false", data["notify_quiet_patrol"])
	}
	if data["mute_delegated_notifications"] != true {
		t.Errorf("mute_delegated_notifications = %v, want true", data["mute_delegated_notifications"])
	}

	// A partial save (no notification keys) must not clobber them.
	code, _ = patchShepherdConfig(t, app, map[string]interface{}{"instructions": "x"})
	if code != http.StatusOK {
		t.Fatalf("partial patch status = %d", code)
	}
	if !viper.GetBool("shepherd_mute_delegated_notifications") {
		t.Error("partial save clobbered shepherd_mute_delegated_notifications")
	}
}

func TestHandleUpdateShepherdConfigSavesInstructionsAndScope(t *testing.T) {
	withShepherdTestEnv(t)

	code, payload := patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"instructions":         "Always run tests before closing an issue.",
		"scope_exclude":        []string{"home"},
		"scope_include_hidden": true,
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	if viper.GetString("shepherd_instructions") != "Always run tests before closing an issue." {
		t.Errorf("shepherd_instructions = %q", viper.GetString("shepherd_instructions"))
	}
	ex := viper.GetStringSlice("shepherd_scope_exclude")
	if len(ex) != 1 || ex[0] != "home" {
		t.Errorf("shepherd_scope_exclude = %v, want [home]", ex)
	}
	if !viper.GetBool("shepherd_scope_include_hidden") {
		t.Error("shepherd_scope_include_hidden must be true")
	}

	// A partial save must not blank the other keys.
	code, _ = patchShepherdConfig(t, shepherdApp(t), map[string]interface{}{
		"instructions": "Shorter version.",
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	ex = viper.GetStringSlice("shepherd_scope_exclude")
	if len(ex) != 1 || ex[0] != "home" {
		t.Errorf("partial save clobbered scope_exclude: %v", ex)
	}

	code, payload = getShepherdConfig(t, shepherdApp(t))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	data := payload["data"].(map[string]any)
	if data["instructions"] != "Shorter version." {
		t.Errorf("instructions = %v", data["instructions"])
	}
	excl, _ := data["scope_exclude"].([]any)
	if len(excl) != 1 || excl[0] != "home" {
		t.Errorf("scope_exclude = %v, want [home]", data["scope_exclude"])
	}
	if data["scope_include_hidden"] != true {
		t.Errorf("scope_include_hidden = %v", data["scope_include_hidden"])
	}
}

// TestHandleUpdateConfigValidatesWorkspacePath covers the shared key: PATCH
// /api/config must run workspace_path through the same validator so both tabs
// store a normalized, existing directory.
func TestHandleUpdateConfigValidatesWorkspacePath(t *testing.T) {
	withShepherdTestEnv(t)
	tmp := t.TempDir()

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Patch("/api/config", s.handleUpdateConfig)

	patch := func(body map[string]interface{}) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		defer resp.Body.Close()
		payload, _ := io.ReadAll(resp.Body)
		var out map[string]any
		json.Unmarshal(payload, &out)
		return resp.StatusCode, out
	}

	code, payload := patch(map[string]interface{}{"workspace_path": tmp + "/nope"})
	if code != http.StatusBadRequest {
		t.Fatalf("missing dir: status = %d payload=%v", code, payload)
	}

	code, _ = patch(map[string]interface{}{"workspace_path": tmp + "/"})
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if viper.GetString("workspace_path") != tmp {
		t.Errorf("workspace_path = %q, want %q", viper.GetString("workspace_path"), tmp)
	}
}

// --- Shepherd task API tests (#594) ---

func postShepherdTask(t *testing.T, app *fiber.App, body map[string]interface{}) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/shepherd/tasks", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return resp.StatusCode, out
}

func getShepherdTasks(t *testing.T, app *fiber.App, query string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/shepherd/tasks"+query, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return resp.StatusCode, out
}

// POST refuses when the workspace is unset or missing: a shepherd task with no
// usable working directory would sit pending forever, so the operator must
// hear about it at enqueue time. Empty prompt is rejected too.
func TestHandleCreateShepherdTaskRejectsUnsetWorkspace(t *testing.T) {
	withShepherdTestEnv(t)
	app := shepherdApp(t)

	// Unset workspace (the withShepherdTestEnv default).
	code, payload := postShepherdTask(t, app, map[string]interface{}{"prompt": "list the projects"})
	if code != http.StatusBadRequest {
		t.Fatalf("unset workspace: status = %d payload=%v", code, payload)
	}

	// Set but non-existent directory.
	viper.Set("workspace_path", "/nonexistent/shepherd-workspace-xyz")
	code, payload = postShepherdTask(t, app, map[string]interface{}{"prompt": "list the projects"})
	if code != http.StatusBadRequest {
		t.Fatalf("invalid workspace: status = %d payload=%v", code, payload)
	}

	// Workspace is fine now, but the prompt must not be blank.
	viper.Set("workspace_path", t.TempDir())
	code, _ = postShepherdTask(t, app, map[string]interface{}{"prompt": "   "})
	if code != http.StatusBadRequest {
		t.Fatalf("blank prompt: status = %d, want 400", code)
	}

	// Nothing was enqueued by the rejected requests.
	code, payload = getShepherdTasks(t, app, "")
	if code != http.StatusOK {
		t.Fatalf("list: status = %d payload=%v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["count"] != float64(0) {
		t.Errorf("count = %v, want 0 after rejected POSTs", data["count"])
	}
}

// A valid POST creates a kind=command, priority=1 pending task. shepherd_model
// is persisted as the per-task model override so the run pins that model.
func TestHandleCreateShepherdTaskCreatesCommandWithModel(t *testing.T) {
	withShepherdTestEnv(t)
	viper.Set("workspace_path", t.TempDir())
	viper.Set("shepherd_model", "opus-pin")
	app := shepherdApp(t)

	code, payload := postShepherdTask(t, app, map[string]interface{}{"prompt": "summarize the queue"})
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["shepherd_kind"] != "command" {
		t.Errorf("shepherd_kind = %v, want command", data["shepherd_kind"])
	}
	if data["priority"] != float64(1) {
		t.Errorf("priority = %v, want 1", data["priority"])
	}
	if data["status"] != "pending" {
		t.Errorf("status = %v, want pending", data["status"])
	}
	taskID := int(data["task_id"].(float64))

	row := db.Client().Task.GetX(context.Background(), taskID)
	if row.Model != "opus-pin" {
		t.Errorf("persisted model = %q, want opus-pin (shepherd_model override)", row.Model)
	}
	if !taskHasNoProject(t, taskID) {
		t.Errorf("task #%d must have no project edge", taskID)
	}
}

func taskHasNoProject(t *testing.T, taskID int) bool {
	t.Helper()
	row, err := db.Client().Task.Query().WithProject().Where(task.ID(taskID)).Only(context.Background())
	if err != nil {
		t.Fatalf("query task: %v", err)
	}
	return row.Edges.Project == nil
}

// GET filters by kind and status, excludes tasks without shepherd_kind, and
// validates the query parameters.
func TestHandleListShepherdTasksFilters(t *testing.T) {
	withShepherdTestEnv(t)
	app := shepherdApp(t)
	ctx := context.Background()

	mgr, err := worker.GetOrCreateManager()
	if err != nil {
		t.Fatalf("GetOrCreateManager: %v", err)
	}

	command := db.Client().Task.Create().
		SetPrompt("command task").SetStatus(task.StatusPending).
		SetSheepID(mgr.ID).SetShepherdKind("command").SetPriority(1).SaveX(ctx)
	review := db.Client().Task.Create().
		SetPrompt("review task").SetStatus(task.StatusCompleted).
		SetSheepID(mgr.ID).SetShepherdKind("review").SaveX(ctx)
	db.Client().Task.Create().
		SetPrompt("ordinary manager task").SetStatus(task.StatusPending).
		SetSheepID(mgr.ID).SaveX(ctx)

	fetchedIDs := func(payload map[string]any) []int {
		data := payload["data"].(map[string]any)
		items := data["tasks"].([]any)
		if data["count"] != float64(len(items)) {
			t.Errorf("count=%v but %d items", data["count"], len(items))
		}
		ids := make([]int, 0, len(items))
		for _, it := range items {
			ids = append(ids, int(it.(map[string]any)["id"].(float64)))
		}
		return ids
	}

	code, payload := getShepherdTasks(t, app, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	if ids := fetchedIDs(payload); len(ids) != 2 {
		t.Errorf("all: ids=%v, want the 2 shepherd tasks only (kind-less row excluded)", ids)
	}

	code, payload = getShepherdTasks(t, app, "?kind=command")
	if code != http.StatusOK {
		t.Fatalf("kind filter: status = %d", code)
	}
	if ids := fetchedIDs(payload); len(ids) != 1 || ids[0] != command.ID {
		t.Errorf("kind=command: ids=%v, want [%d]", ids, command.ID)
	}

	code, payload = getShepherdTasks(t, app, "?status=completed")
	if code != http.StatusOK {
		t.Fatalf("status filter: status = %d", code)
	}
	if ids := fetchedIDs(payload); len(ids) != 1 || ids[0] != review.ID {
		t.Errorf("status=completed: ids=%v, want [%d]", ids, review.ID)
	}

	for _, bad := range []string{"?kind=bogus", "?status=bogus", "?limit=0", "?limit=abc"} {
		if code, _ = getShepherdTasks(t, app, bad); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", bad, code)
		}
	}
}

// --- Shepherd status API (#598) ---

func getShepherdStatus(t *testing.T, app *fiber.App) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/shepherd/status", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return resp.StatusCode, out
}

func TestHandleGetShepherdStatusReportsWorkspaceAndInjectSupport(t *testing.T) {
	withShepherdTestEnv(t)
	app := shepherdApp(t)
	// viper.Reset() in withShepherdTestEnv drops config.Init()'s defaults;
	// auto_approve=true is the daemon default and enables claude injection.
	viper.SetDefault("auto_approve", true)

	// Unset workspace → the console banner condition.
	code, payload := getShepherdStatus(t, app)
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["workspace_valid"] != false {
		t.Errorf("workspace_valid = %v, want false when unset", data["workspace_valid"])
	}
	if data["provider"] != string(sheep.ProviderClaude) {
		t.Errorf("provider = %v, want claude", data["provider"])
	}
	// Default provider is claude; auto_approve defaults true → injection works.
	if data["inject_supported"] != true {
		t.Errorf("inject_supported = %v, want true for claude+auto_approve", data["inject_supported"])
	}
	if _, has := data["running_task_id"]; has {
		t.Errorf("running_task_id present while nothing runs: %v", data)
	}
	if data["status"] != "idle" {
		t.Errorf("status = %v, want idle", data["status"])
	}

	// Valid workspace flips the flag.
	viper.Set("workspace_path", t.TempDir())
	code, payload = getShepherdStatus(t, app)
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data = payload["data"].(map[string]any)
	if data["workspace_valid"] != true {
		t.Errorf("workspace_valid = %v, want true", data["workspace_valid"])
	}

	// opencode has no inject channel at all.
	if _, err := worker.GetOrCreateManager(); err != nil {
		t.Fatalf("GetOrCreateManager: %v", err)
	}
	if err := worker.UpdateProvider(names.ManagerName, "opencode"); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	_, payload = getShepherdStatus(t, app)
	data = payload["data"].(map[string]any)
	if data["inject_supported"] != false {
		t.Errorf("inject_supported = %v, want false for opencode", data["inject_supported"])
	}
}

func TestHandleGetShepherdStatusTodayStatsAndBreaker(t *testing.T) {
	withShepherdTestEnv(t)
	app := shepherdApp(t)
	ctx := context.Background()

	mgr, err := worker.GetOrCreateManager()
	if err != nil {
		t.Fatalf("GetOrCreateManager: %v", err)
	}

	db.Client().Task.Create().SetPrompt("today command").SetStatus(task.StatusCompleted).
		SetSheepID(mgr.ID).SetShepherdKind("command").SetCostUsd(1.25).SaveX(ctx)
	db.Client().Task.Create().SetPrompt("today review").SetStatus(task.StatusCompleted).
		SetSheepID(mgr.ID).SetShepherdKind("review").SetCostUsd(0.5).SaveX(ctx)
	// Not counted: ordinary (kind-less) task and a yesterday shepherd task.
	db.Client().Task.Create().SetPrompt("ordinary").SetStatus(task.StatusCompleted).
		SetSheepID(mgr.ID).SetCostUsd(9).SaveX(ctx)
	db.Client().Task.Create().SetPrompt("yesterday command").SetStatus(task.StatusCompleted).
		SetSheepID(mgr.ID).SetShepherdKind("command").SetCostUsd(5).
		SetCreatedAt(time.Now().AddDate(0, 0, -1)).SaveX(ctx)

	code, payload := getShepherdStatus(t, app)
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["today_task_count"] != float64(2) {
		t.Errorf("today_task_count = %v, want 2", data["today_task_count"])
	}
	if cost, _ := data["today_cost_usd"].(float64); cost < 1.74 || cost > 1.76 {
		t.Errorf("today_cost_usd = %v, want ~1.75", data["today_cost_usd"])
	}
	if data["circuit_tripped"] != false {
		t.Errorf("circuit_tripped = %v, want false", data["circuit_tripped"])
	}

	// Trip the breaker on the manager row.
	db.Client().Sheep.UpdateOneID(mgr.ID).SetConsecutiveFailures(5).ExecX(ctx)
	_, payload = getShepherdStatus(t, app)
	data = payload["data"].(map[string]any)
	if data["circuit_tripped"] != true {
		t.Errorf("circuit_tripped = %v, want true after 5 failures", data["circuit_tripped"])
	}

	// Stale "working" with no live in-memory task reports idle.
	if err := worker.UpdateStatus(names.ManagerName, sheep.StatusWorking); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	_, payload = getShepherdStatus(t, app)
	data = payload["data"].(map[string]any)
	if data["status"] != "idle" {
		t.Errorf("stale working status = %v, want idle", data["status"])
	}
}
