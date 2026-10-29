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

// Config API round-trip for the rate-limit handoff keys (#675): GET exposes the
// current values, PATCH stores them after normalization, and an unknown
// provider is rejected with 400 instead of being saved.

func configPatch(t *testing.T, app *fiber.App, body map[string]interface{}) (int, map[string]any) {
	t.Helper()
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

func configGet(t *testing.T, app *fiber.App) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return out.Data
}

func TestHandleConfigLimitHandoffRoundTrip(t *testing.T) {
	withShepherdTestEnv(t)

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/config", s.handleGetConfig)
	app.Patch("/api/config", s.handleUpdateConfig)

	// Defaults: both keys exposed and empty (feature off).
	data := configGet(t, app)
	if v, ok := data["limit_handoff_provider"]; !ok || v != "" {
		t.Fatalf("GET default limit_handoff_provider = %v (present=%v), want empty string", v, ok)
	}
	if v, ok := data["limit_handoff_model"]; !ok || v != "" {
		t.Fatalf("GET default limit_handoff_model = %v (present=%v), want empty string", v, ok)
	}

	// PATCH stores normalized values (trim + lowercase).
	code, payload := configPatch(t, app, map[string]interface{}{
		"limit_handoff_provider": " Grok ",
		"limit_handoff_model":    " grok-4.5 ",
	})
	if code != http.StatusOK {
		t.Fatalf("valid patch: status = %d payload=%v", code, payload)
	}
	if got := viper.GetString("limit_handoff_provider"); got != "grok" {
		t.Errorf("stored provider = %q, want grok", got)
	}
	if got := viper.GetString("limit_handoff_model"); got != "grok-4.5" {
		t.Errorf("stored model = %q, want grok-4.5", got)
	}

	// GET reflects the stored values.
	data = configGet(t, app)
	if data["limit_handoff_provider"] != "grok" || data["limit_handoff_model"] != "grok-4.5" {
		t.Errorf("GET after patch = (%v,%v), want (grok,grok-4.5)",
			data["limit_handoff_provider"], data["limit_handoff_model"])
	}

	// Unknown provider: 400 and nothing stored.
	code, _ = configPatch(t, app, map[string]interface{}{"limit_handoff_provider": "not-a-provider"})
	if code != http.StatusBadRequest {
		t.Fatalf("unknown provider: status = %d, want 400", code)
	}
	if got := viper.GetString("limit_handoff_provider"); got != "grok" {
		t.Errorf("provider changed after rejected patch: %q", got)
	}

	// "auto" folds to claude on save (matches the runtime fold).
	code, _ = configPatch(t, app, map[string]interface{}{"limit_handoff_provider": "auto"})
	if code != http.StatusOK {
		t.Fatalf("auto patch: status = %d", code)
	}
	if got := viper.GetString("limit_handoff_provider"); got != "claude" {
		t.Errorf("auto stored as %q, want claude", got)
	}

	// Empty clears the target (feature off).
	code, _ = configPatch(t, app, map[string]interface{}{"limit_handoff_provider": ""})
	if code != http.StatusOK {
		t.Fatalf("clear patch: status = %d", code)
	}
	if got := viper.GetString("limit_handoff_provider"); got != "" {
		t.Errorf("cleared provider = %q, want empty", got)
	}
}
