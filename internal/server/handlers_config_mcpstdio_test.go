package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
)

// Config API round-trip for the external MCP stdio read timeout (the `mcp:`
// section of embedded.yaml). GET exposes the effective default (600 seconds),
// PUT stores whole seconds and a duration form ("5m"), and an unusable value is
// rejected with 400 instead of being saved — a timeout that silently became "no
// timeout" because of a typo is the failure mode this guards. A save must also
// leave the endpoints list intact (read-modify-write).

func mcpConfigGet(t *testing.T, app *fiber.App) map[string]any {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/config/mcp", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/config/mcp: status = %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Data struct {
			MCP map[string]any `json:"mcp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out.Data.MCP
}

func mcpConfigPut(t *testing.T, app *fiber.App, body map[string]any) (int, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/config/mcp", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestHandleConfigMCPStdioTimeoutRoundTrip(t *testing.T) {
	withShepherdTestEnv(t)

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/config/mcp", s.handleGetMCPConfig)
	app.Put("/api/config/mcp", s.handleUpdateMCPConfig)

	// Default is exposed as a number, so the settings field is never blank.
	m := mcpConfigGet(t, app)
	if v, ok := m["stdio_timeout_seconds"]; !ok || toInt(v) != config.DefaultMCPStdioTimeoutSeconds {
		t.Fatalf("GET default stdio_timeout_seconds = %v (present=%v), want %d",
			v, ok, config.DefaultMCPStdioTimeoutSeconds)
	}

	// A duration form normalizes to whole seconds on save.
	code, payload := mcpConfigPut(t, app, map[string]any{"stdio_timeout_seconds": "5m"})
	if code != http.StatusOK {
		t.Fatalf("valid put: status = %d payload=%v", code, payload)
	}
	if got := config.GetMCPStdioTimeoutSeconds(); got != 300 {
		t.Errorf("stored stdio timeout = %d, want 300", got)
	}
	if m = mcpConfigGet(t, app); toInt(m["stdio_timeout_seconds"]) != 300 {
		t.Errorf("GET after put = %v, want 300", m["stdio_timeout_seconds"])
	}

	// Unusable values: 400 and the previous value stays.
	for _, bad := range []any{0, -1, "abc", "5x", config.MCPStdioTimeoutHardCeilSeconds + 1} {
		code, _ = mcpConfigPut(t, app, map[string]any{"stdio_timeout_seconds": bad})
		if code != http.StatusBadRequest {
			t.Fatalf("stdio_timeout_seconds=%v: status = %d, want 400", bad, code)
		}
		if got := config.GetMCPStdioTimeoutSeconds(); got != 300 {
			t.Errorf("stdio_timeout_seconds=%v changed the stored value to %d", bad, got)
		}
	}

	// The ceiling itself is accepted.
	code, _ = mcpConfigPut(t, app, map[string]any{"stdio_timeout_seconds": config.MCPStdioTimeoutHardCeilSeconds})
	if code != http.StatusOK {
		t.Fatalf("ceiling put: status = %d", code)
	}
	if got := config.GetMCPStdioTimeoutSeconds(); got != config.MCPStdioTimeoutHardCeilSeconds {
		t.Errorf("stored stdio timeout = %d, want ceiling %d", got, config.MCPStdioTimeoutHardCeilSeconds)
	}
}
