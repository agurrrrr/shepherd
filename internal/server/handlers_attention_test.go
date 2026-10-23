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

	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/queue"
)

// POST /api/sheep/:name/reset (#599) — the panel's circuit-breaker reset.
// withShepherdTestEnv gives an in-memory DB + viper-scoped config; the
// processor stays nil so the endpoint only enqueues nothing and mutates rows.

func newResetApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Post("/api/sheep/:name/reset", s.handleResetSheep)
	return app
}

func postJSON(t *testing.T, app *fiber.App, url string, body map[string]any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(http.MethodPost, url, reader)
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

func TestResetSheepUntripsCircuit(t *testing.T) {
	withShepherdTestEnv(t)
	app := newResetApp(t)
	ctx := context.Background()
	client := db.Client()

	tripped := client.Sheep.Create().SetName("깜순이").SetStatus(sheep.StatusError).
		SetConsecutiveFailures(queue.CircuitBreakerThreshold).SaveX(ctx)

	status, payload := postJSON(t, app, "/api/sheep/깜순이/reset", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", status, payload)
	}

	got := client.Sheep.GetX(ctx, tripped.ID)
	if got.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures = %d, want 0", got.ConsecutiveFailures)
	}
	if got.Status != sheep.StatusIdle {
		t.Errorf("status = %s, want idle", got.Status)
	}
	if data, _ := payload["data"].(map[string]any); data == nil || data["reset_circuit"] != true {
		t.Errorf("response should report reset_circuit=true: %v", payload)
	}
}

func TestResetSheepManagerByName(t *testing.T) {
	withShepherdTestEnv(t)
	app := newResetApp(t)
	ctx := context.Background()

	// The manager row is created on demand by GetOrCreateManager flows; seed
	// it directly here.
	mgr := db.Client().Sheep.Create().SetName(names.ManagerName).SetStatus(sheep.StatusError).
		SetConsecutiveFailures(queue.CircuitBreakerThreshold + 2).SaveX(ctx)

	status, payload := postJSON(t, app, "/api/sheep/shepherd/reset", map[string]any{"clear_session": true})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", status, payload)
	}
	got := db.Client().Sheep.GetX(ctx, mgr.ID)
	if got.ConsecutiveFailures != 0 || got.Status != sheep.StatusIdle {
		t.Errorf("manager not reset: failures=%d status=%s", got.ConsecutiveFailures, got.Status)
	}
}

func TestResetSheepUnknownName(t *testing.T) {
	withShepherdTestEnv(t)
	app := newResetApp(t)

	status, payload := postJSON(t, app, "/api/sheep/nope/reset", nil)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %v", status, payload)
	}
}
