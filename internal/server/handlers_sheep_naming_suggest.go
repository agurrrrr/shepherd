package server

// POST /api/sheep-names/suggest (#638 §7): one LLM call for candidate sheep
// names. Used by the settings preview (form values, before saving) and by the
// project Settings tab's "LLM으로 다시 짓기" button. Works even when the stored
// switch is off, as long as the body carries a provider — that is exactly what
// the preview needs.

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/namegen"
)

func (s *Server) handleSuggestSheepNames(c *fiber.Ctx) error {
	var body struct {
		ProjectName string `json:"project_name"`
		Description string `json:"description"`
		Provider    string `json:"provider"`
		Model       string `json:"model"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if body.ProjectName == "" {
		return fail(c, fiber.StatusBadRequest, "project_name is required")
	}

	// No provider in the body → only allowed when the stored setting supplies one.
	if body.Provider == "" && !config.GetBool("sheep_name_llm_enabled") {
		if config.GetString("sheep_name_llm_provider") == "" {
			return fail(c, fiber.StatusBadRequest, "sheep naming is disabled and no provider was given")
		}
	}

	usedNames, err := usedSheepNames()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to read sheep names: "+err.Error())
	}

	ctx, cancel := context.WithTimeout(c.Context(), namegen.Timeout+5*time.Second) // 35s: namegen's own cap plus handler slack
	defer cancel()

	candidates, err := namegen.Suggest(ctx, namegen.Request{
		ProjectName: body.ProjectName,
		Description: body.Description,
		UsedNames:   usedNames,
		Provider:    body.Provider,
		Model:       body.Model,
	})
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	return success(c, map[string]interface{}{"names": candidates})
}

// usedSheepNames lists every sheep name in the DB so Suggest can drop them.
func usedSheepNames() ([]string, error) {
	sheepList, err := db.Client().Sheep.Query().All(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(sheepList))
	for _, s := range sheepList {
		out = append(out, s.Name)
	}
	return out, nil
}
