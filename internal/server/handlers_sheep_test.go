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

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
)

// PATCH /api/sheep/:name/name (#638 §6): 200 rename, 400 validation / manager,
// 404 unknown sheep, 409 taken name or working sheep.
func sheepRenameApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Patch("/api/sheep/:name/name", s.handleRenameSheep)
	return app
}

func withSheepTestEnv(t *testing.T) *ent.Client {
	t.Helper()
	client := newTestClient(t)
	prevDB := db.ReplaceClient(client)
	prevCfgDir := config.UseTempConfigDir(t.TempDir())
	t.Cleanup(func() {
		db.ReplaceClient(prevDB)
		config.UseTempConfigDir(prevCfgDir)
	})
	return client
}

func TestRenameSheepHandler(t *testing.T) {
	client := withSheepTestEnv(t)
	ctx := context.Background()
	app := sheepRenameApp(t)

	_, err := client.Sheep.Create().SetName("양동이").SetStatus(sheep.StatusIdle).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Sheep.Create().SetName("메에롱").SetStatus(sheep.StatusIdle).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Sheep.Create().SetName("깜순이").SetStatus(sheep.StatusWorking).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		oldName  string
		newName  string
		wantCode int
	}{
		{"success", "양동이", "탕비양", fiber.StatusOK},
		{"validation empty name", "메에롱", "", fiber.StatusBadRequest},
		{"validation reserved", "메에롱", "shepherd", fiber.StatusBadRequest},
		{"validation forbidden char", "메에롱", "a/b", fiber.StatusBadRequest},
		{"manager sheep rejected", "shepherd", "다른이름", fiber.StatusBadRequest},
		{"unknown sheep", "없는양", "새이름", fiber.StatusNotFound},
		{"name conflict", "메에롱", "탕비양", fiber.StatusConflict},
		{"working sheep conflict", "깜순이", "흰둥이", fiber.StatusConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"name": tt.newName})
			req := httptest.NewRequest(http.MethodPatch, "/api/sheep/"+tt.oldName+"/name", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantCode {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("code = %d, want %d (body %s)", resp.StatusCode, tt.wantCode, body)
			}
		})
	}

	// The success case must actually have renamed the row.
	exists, err := client.Sheep.Query().Where(sheep.Name("탕비양")).Exist(ctx)
	if err != nil || !exists {
		t.Errorf("renamed sheep not found in DB (exists=%v err=%v)", exists, err)
	}
}
