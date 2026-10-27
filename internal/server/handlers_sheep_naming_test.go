package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
)

// withSheepNamingTestEnv keeps config writes in viper's override layer and
// restores the provider_enabled_* defaults, mirroring withShepherdTestEnv.
func withSheepNamingTestEnv(t *testing.T) {
	t.Helper()

	viper.Reset()
	for _, p := range []string{"claude", "opencode", "pi", "grok", "embedded", "magi"} {
		viper.SetDefault("provider_enabled_"+p, true)
	}
	viper.SetDefault("sheep_name_llm_enabled", false)
	viper.SetDefault("sheep_name_llm_provider", "")
	viper.SetDefault("sheep_name_llm_model", "")

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

func sheepNamingApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/config/sheep-naming", s.handleGetSheepNamingConfig)
	app.Put("/api/config/sheep-naming", s.handleUpdateSheepNamingConfig)
	return app
}

func putSheepNaming(t *testing.T, app *fiber.App, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/config/sheep-naming", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func TestUpdateSheepNamingConfig(t *testing.T) {
	withSheepNamingTestEnv(t)
	app := sheepNamingApp(t)

	// Enable with a valid enabled provider.
	code, out := putSheepNaming(t, app, map[string]any{"enabled": true, "provider": "claude", "model": "haiku"})
	if code != fiber.StatusOK {
		t.Fatalf("enable: code=%d out=%v", code, out)
	}
	data := out["data"].(map[string]any)
	if data["enabled"] != true || data["provider"] != "claude" || data["model"] != "haiku" {
		t.Fatalf("payload = %v", data)
	}

	// Enabled without a provider → 400.
	viper.Set("sheep_name_llm_provider", "")
	code, _ = putSheepNaming(t, app, map[string]any{"enabled": true})
	if code != fiber.StatusBadRequest {
		t.Fatalf("missing provider: code=%d", code)
	}

	// magi / auto rejected.
	for _, p := range []string{"magi", "auto"} {
		code, _ = putSheepNaming(t, app, map[string]any{"provider": p})
		if code != fiber.StatusBadRequest {
			t.Fatalf("provider %s: code=%d", p, code)
		}
	}

	// Disabled provider rejected.
	viper.Set("provider_enabled_grok", false)
	code, _ = putSheepNaming(t, app, map[string]any{"provider": "grok"})
	if code != fiber.StatusBadRequest {
		t.Fatalf("disabled provider: code=%d", code)
	}
	viper.Set("provider_enabled_grok", true)

	// Provider change resets a model that the new provider does not offer.
	viper.Set("sheep_name_llm_provider", "claude")
	viper.Set("sheep_name_llm_model", "haiku")
	code, out = putSheepNaming(t, app, map[string]any{"provider": "embedded"})
	if code != fiber.StatusOK {
		t.Fatalf("switch provider: code=%d out=%v", code, out)
	}
	if got := viper.GetString("sheep_name_llm_model"); got != "" {
		t.Fatalf("model should reset on provider change, got %q", got)
	}

	// Provider change keeps a model the new provider DOES offer.
	viper.Set("sheep_name_llm_provider", "claude")
	viper.Set("sheep_name_llm_model", "")
	code, _ = putSheepNaming(t, app, map[string]any{"provider": "claude", "model": "sonnet"})
	if code != fiber.StatusOK {
		t.Fatalf("set claude model: code=%d", code)
	}
	if got := viper.GetString("sheep_name_llm_model"); got != "sonnet" {
		t.Fatalf("model = %q, want sonnet", got)
	}
}

func TestGetSheepNamingConfig(t *testing.T) {
	withSheepNamingTestEnv(t)
	app := sheepNamingApp(t)

	viper.Set("sheep_name_llm_enabled", true)
	viper.Set("sheep_name_llm_provider", "claude")

	req := httptest.NewRequest(http.MethodGet, "/api/config/sheep-naming", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("code = %d", resp.StatusCode)
	}
	var out map[string]any
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	data := out["data"].(map[string]any)
	if data["enabled"] != true || data["provider"] != "claude" {
		t.Fatalf("payload = %v", data)
	}
	providers, _ := data["providers"].([]any)
	if len(providers) != 5 {
		t.Fatalf("providers should exclude magi, got %v", providers)
	}
	for _, p := range providers {
		if p == "magi" || p == "auto" {
			t.Fatalf("providers should not contain %v", p)
		}
	}
	opts, _ := data["model_options"].([]any)
	if len(opts) == 0 {
		t.Fatal("model_options should be populated for claude")
	}
	if data["model_selectable"] != true {
		t.Fatalf("model_selectable = %v", data["model_selectable"])
	}
}
