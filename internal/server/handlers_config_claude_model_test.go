package server

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
)

// The Claude model settings feed `claude --model` verbatim. The selector used to
// offer "claude-opus-5.5", a dotted id that no Claude CLI accepts: the CLI
// refused it, and shepherd still recorded the task as completed with the refusal
// sentence as its summary (task #12073). A save must reject an impossible shape
// so the bad value never reaches config.yaml.

func TestHandleConfigRejectsDottedClaudeModelID(t *testing.T) {
	withShepherdTestEnv(t)

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/config", s.handleGetConfig)
	app.Patch("/api/config", s.handleUpdateConfig)

	code, payload := configPatch(t, app, map[string]interface{}{"model_claude": "claude-opus-5.5"})
	if code != http.StatusBadRequest {
		t.Fatalf("dotted id: status = %d payload=%v, want 400", code, payload)
	}
	if got := viper.GetString("model_claude"); got != "" {
		t.Errorf("stored model_claude = %q, must stay unset after a rejected save", got)
	}

	// A real id and an empty id (let the CLI decide) both save.
	for _, want := range []string{"claude-opus-5-5", "opus", ""} {
		code, payload = configPatch(t, app, map[string]interface{}{"model_claude": want})
		if code != http.StatusOK {
			t.Fatalf("patch %q: status = %d payload=%v, want 200", want, code, payload)
		}
		if got := viper.GetString("model_claude"); got != want {
			t.Errorf("stored model_claude = %q, want %q", got, want)
		}
	}

	// Trimming still applies to a valid id.
	code, payload = configPatch(t, app, map[string]interface{}{"model_claude": "  sonnet  "})
	if code != http.StatusOK {
		t.Fatalf("patch padded id: status = %d payload=%v, want 200", code, payload)
	}
	if got := viper.GetString("model_claude"); got != "sonnet" {
		t.Errorf("stored model_claude = %q, want sonnet (trimmed)", got)
	}
}

// The 목자 tab stores shepherd_model, which the dispatcher hands to the provider
// as a per-task override — the same path that ran claude-opus-5.5. It must be
// validated too, and only for providers that take a Claude id.
func TestHandleUpdateShepherdConfigRejectsDottedClaudeModel(t *testing.T) {
	withShepherdTestEnv(t)

	app := shepherdApp(t)

	code, payload := patchShepherdConfig(t, app, map[string]interface{}{
		"provider": "claude",
		"model":    "claude-sonnet-5.5",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("dotted id: status = %d payload=%v, want 400", code, payload)
	}
	if got := viper.GetString("shepherd_model"); got != "" {
		t.Errorf("stored shepherd_model = %q, must stay unset after a rejected save", got)
	}

	// The same id shape is fine for embedded, where the value is an endpoint id.
	code, payload = patchShepherdConfig(t, app, map[string]interface{}{
		"provider": "embedded",
		"model":    "some.endpoint-v1.2",
	})
	if code != http.StatusOK {
		t.Fatalf("embedded endpoint id: status = %d payload=%v, want 200", code, payload)
	}
}

// modelOptionsFor is the single source for every Claude model selector (the
// provider tabs and the 목자 tab). Its ids must be shapes the CLI can accept, or
// the list itself keeps re-introducing the bug it was meant to avoid.
func TestModelOptionsForClaudeHasNoDottedIDs(t *testing.T) {
	opts, selectable := modelOptionsFor("claude", "CLI default")
	if !selectable {
		t.Fatalf("claude must be selectable")
	}
	if len(opts) < 2 {
		t.Fatalf("claude options = %d, want a populated list", len(opts))
	}
	for _, o := range opts {
		for _, r := range o.ID {
			if r == '.' || r == ' ' {
				t.Errorf("claude model option id %q contains %q; Claude ids use hyphens", o.ID, string(r))
			}
		}
	}
}
