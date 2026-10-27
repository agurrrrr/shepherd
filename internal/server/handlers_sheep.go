package server

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// GET /api/sheep
func (s *Server) handleListSheep(c *fiber.Ctx) error {
	sheepList, err := worker.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	type sheepItem struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Provider string `json:"provider"`
		Project  string `json:"project,omitempty"`
	}

	var result []sheepItem
	for _, s := range sheepList {
		item := sheepItem{
			Name:     s.Name,
			Status:   string(s.Status),
			Provider: string(s.Provider),
		}
		if s.Edges.Project != nil {
			item.Project = s.Edges.Project.Name
		}
		result = append(result, item)
	}

	return success(c, result)
}

// POST /api/sheep
func (s *Server) handleCreateSheep(c *fiber.Ctx) error {
	var body struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	opts := worker.CreateOptions{
		Name:     body.Name,
		Provider: body.Provider,
	}
	if opts.Provider == "" {
		opts.Provider = "claude"
	}

	newSheep, err := worker.CreateWithOptions(opts)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	return success(c, map[string]interface{}{
		"name":     newSheep.Name,
		"status":   string(newSheep.Status),
		"provider": string(newSheep.Provider),
	})
}

// GET /api/sheep/:name
func (s *Server) handleGetSheep(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	sheep, err := worker.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	result := map[string]interface{}{
		"name":     sheep.Name,
		"status":   string(sheep.Status),
		"provider": string(sheep.Provider),
	}
	if sheep.Edges.Project != nil {
		result["project"] = sheep.Edges.Project.Name
	}

	return success(c, result)
}

// DELETE /api/sheep/:name
func (s *Server) handleDeleteSheep(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	if err := worker.Delete(name); err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}
	return success(c, nil)
}

// PATCH /api/sheep/:name/name — rename a sheep (#638 §6).
// Body: {"name": "새이름"}. worker.Rename owns the rules; this handler only
// maps its sentinel errors to HTTP codes:
//   400 validation failure / manager sheep, 404 unknown sheep,
//   409 name taken or sheep currently working.
func (s *Server) handleRenameSheep(c *fiber.Ctx) error {
	oldName := paramDecoded(c, "name")
	var body struct {
		Name string `json:"name"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if names.IsManager(oldName) {
		return fail(c, fiber.StatusBadRequest, "the manager sheep cannot be renamed")
	}
	if err := names.ValidateCustom(body.Name); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	if err := worker.Rename(oldName, body.Name); err != nil {
		switch {
		case errors.Is(err, worker.ErrSheepNotFound):
			return fail(c, fiber.StatusNotFound, err.Error())
		case errors.Is(err, worker.ErrSheepNameExists), errors.Is(err, worker.ErrSheepWorking):
			return fail(c, fiber.StatusConflict, err.Error())
		default:
			return fail(c, fiber.StatusBadRequest, err.Error())
		}
	}

	if strings.TrimSpace(body.Name) == oldName {
		return success(c, map[string]interface{}{"old_name": oldName, "new_name": oldName})
	}

	// Find the project the renamed sheep serves so every store on the web can
	// patch its sheep field from one event (sidebar dots key by project name).
	projectName := ""
	if sh, err := worker.Get(body.Name); err == nil && sh.Edges.Project != nil {
		projectName = sh.Edges.Project.Name
	}

	s.hub.Broadcast(SSEEvent{Type: "sheep_renamed", Data: map[string]interface{}{
		"old_name": oldName,
		"new_name": body.Name,
		"project":  projectName,
	}})

	return success(c, map[string]interface{}{"old_name": oldName, "new_name": body.Name})
}

// PATCH /api/sheep/:name/provider
func (s *Server) handleUpdateSheepProvider(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	var body struct {
		Provider string `json:"provider"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if !config.IsProviderEnabled(body.Provider) {
		return fail(c, fiber.StatusBadRequest, "provider '"+body.Provider+"' is disabled in settings")
	}

	if err := worker.UpdateProvider(name, body.Provider); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	// Notify all SSE clients of provider change
	s.hub.Broadcast(SSEEvent{Type: "provider_change", Data: map[string]interface{}{
		"sheep_name": name, "provider": body.Provider,
	}})

	return success(c, nil)
}
