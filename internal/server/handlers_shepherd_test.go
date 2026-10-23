package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
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
