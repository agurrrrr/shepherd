package server

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
)

// Config API round-trip for max_upload_mb: GET exposes the effective value,
// PATCH stores a new one after validation, and an unusable number is rejected
// with 400 instead of being saved. An upload limit that silently became 0 or
// "huge" because of a form typo is the failure mode this guards.
//
// configPatch/configGet come from handlers_config_handoff_test.go (same package).

func TestHandleConfigMaxUploadMBRoundTrip(t *testing.T) {
	withShepherdTestEnv(t)

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/config", s.handleGetConfig)
	app.Patch("/api/config", s.handleUpdateConfig)

	// Default is exposed as a number, so the settings field is never blank.
	data := configGet(t, app)
	if v, ok := data["max_upload_mb"]; !ok || toInt(v) != config.DefaultMaxUploadMB {
		t.Fatalf("GET default max_upload_mb = %v (present=%v), want %d",
			v, ok, config.DefaultMaxUploadMB)
	}

	// PATCH stores a valid value, including the string form the CLI and a text
	// input can produce.
	code, payload := configPatch(t, app, map[string]interface{}{"max_upload_mb": "25"})
	if code != http.StatusOK {
		t.Fatalf("valid patch: status = %d payload=%v", code, payload)
	}
	if got := config.GetMaxUploadMB(); got != 25 {
		t.Errorf("stored max_upload_mb = %d, want 25", got)
	}
	if data = configGet(t, app); toInt(data["max_upload_mb"]) != 25 {
		t.Errorf("GET after patch = %v, want 25", data["max_upload_mb"])
	}

	// Unusable values: 400 and the previous value stays.
	for _, bad := range []interface{}{0, -1, "abc", config.MaxUploadMBHardCeil + 1} {
		code, _ = configPatch(t, app, map[string]interface{}{"max_upload_mb": bad})
		if code != http.StatusBadRequest {
			t.Fatalf("max_upload_mb=%v: status = %d, want 400", bad, code)
		}
		if got := config.GetMaxUploadMB(); got != 25 {
			t.Errorf("max_upload_mb=%v changed the stored value to %d", bad, got)
		}
	}
}

// toInt reads a JSON number out of a decoded map (always float64).
func toInt(v interface{}) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return -1
}
