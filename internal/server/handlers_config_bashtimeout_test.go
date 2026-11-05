package server

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
)

// Config API round-trip for bash_timeout: GET exposes the effective
// default (10 minutes), PATCH stores whole seconds, a duration form ("5m"), and
// the string form the CLI can produce, and an unusable value is rejected with 400
// instead of being saved. A timeout that silently became "no timeout" because of
// a typo is the failure mode this guards.
//
// configPatch/configGet come from handlers_config_handoff_test.go (same package).

func TestHandleConfigBashTimeoutRoundTrip(t *testing.T) {
	withShepherdTestEnv(t)

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/config", s.handleGetConfig)
	app.Patch("/api/config", s.handleUpdateConfig)

	// Default is exposed as a number, so the settings field is never blank.
	data := configGet(t, app)
	if v, ok := data["bash_timeout"]; !ok || toInt(v) != config.DefaultBashTimeoutSeconds {
		t.Fatalf("GET default bash_timeout = %v (present=%v), want %d",
			v, ok, config.DefaultBashTimeoutSeconds)
	}

	// A duration form normalizes to whole seconds on save, so the config file and
	// the form round-trip through one number format.
	code, payload := configPatch(t, app, map[string]interface{}{"bash_timeout": "5m"})
	if code != http.StatusOK {
		t.Fatalf("valid patch: status = %d payload=%v", code, payload)
	}
	if got := config.GetBashTimeoutSeconds(); got != 300 {
		t.Errorf("stored bash_timeout = %d, want 300", got)
	}
	if data = configGet(t, app); toInt(data["bash_timeout"]) != 300 {
		t.Errorf("GET after patch = %v, want 300", data["bash_timeout"])
	}

	// Unusable values: 400 and the previous value stays. The ceiling case matters
	// most here — an unbounded shell timeout would park an agent turn for hours.
	for _, bad := range []interface{}{0, -1, "abc", "1Om", config.BashTimeoutHardCeilSeconds + 1} {
		code, _ = configPatch(t, app, map[string]interface{}{"bash_timeout": bad})
		if code != http.StatusBadRequest {
			t.Fatalf("bash_timeout=%v: status = %d, want 400", bad, code)
		}
		if got := config.GetBashTimeoutSeconds(); got != 300 {
			t.Errorf("bash_timeout=%v changed the stored value to %d", bad, got)
		}
	}

	// The ceiling itself is accepted.
	code, _ = configPatch(t, app, map[string]interface{}{"bash_timeout": config.BashTimeoutHardCeilSeconds})
	if code != http.StatusOK {
		t.Fatalf("ceiling patch: status = %d", code)
	}
	if got := config.GetBashTimeoutSeconds(); got != config.BashTimeoutHardCeilSeconds {
		t.Errorf("stored bash_timeout = %d, want ceiling %d", got, config.BashTimeoutHardCeilSeconds)
	}
}
