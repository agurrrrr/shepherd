package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// TestPromptPreviewRendersManagerContext wires the exact daemon hookup from
// cmd/shepherd/main.go — worker.SetShepherdContextProvider(manager.BuildShepherdContext)
// — and checks that GET /api/config/system-prompt-preview?sheep=shepherd returns
// the orchestrator block with the live scope table (#595 goal: the settings >
// 목자 tab previews the manager prompt). An ordinary sheep's preview must stay
// free of it.
func TestPromptPreviewRendersManagerContext(t *testing.T) {
	withShepherdTestEnv(t)

	// Real end-to-end wiring: server already imports manager, so this is the
	// same provider main.go installs.
	worker.SetShepherdContextProvider(manager.BuildShepherdContext)
	t.Cleanup(func() { worker.SetShepherdContextProvider(nil) })

	ctx := context.Background()
	client := db.Client()
	client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/config/system-prompt-preview", s.handleSystemPromptPreview)

	req := httptest.NewRequest(http.MethodGet, "/api/config/system-prompt-preview?sheep="+names.ManagerName, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var payload struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}

	for mode, out := range payload.Data {
		if !strings.Contains(out, "[Shepherd Orchestrator") {
			t.Errorf("preview[%s]: missing orchestrator block", mode)
		}
	}
	if !strings.Contains(payload.Data["streaming"], "bori | /ws/bori") {
		t.Errorf("preview must carry the live scope table:\n%s", payload.Data["streaming"])
	}

	// Ordinary sheep: no manager block.
	req2 := httptest.NewRequest(http.MethodGet, "/api/config/system-prompt-preview?sheep=%EC%96%89%EC%88%9C%EC%9D%B4", nil)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if strings.Contains(string(body2), "[Shepherd Orchestrator") {
		t.Errorf("ordinary sheep preview must not contain the manager block:\n%s", body2)
	}
}
