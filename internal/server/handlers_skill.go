package server

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/skill"
)

// GET /api/skills — list all skills
func (s *Server) handleListAllSkills(c *fiber.Ctx) error {
	skills, err := skill.ListAll()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	var items []fiber.Map
	for _, sk := range skills {
		items = append(items, skillToMap(sk))
	}

	if items == nil {
		items = []fiber.Map{}
	}

	return success(c, items)
}

// GET /api/projects/:name/skills — list skills for a project
func (s *Server) handleListProjectSkills(c *fiber.Ctx) error {
	projectName := paramDecoded(c, "name")

	skills, err := skill.ListByProject(projectName)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	var items []fiber.Map
	for _, sk := range skills {
		items = append(items, skillToMap(sk))
	}

	if items == nil {
		items = []fiber.Map{}
	}

	return success(c, items)
}

// POST /api/skills — create a global skill
func (s *Server) handleCreateGlobalSkill(c *fiber.Ctx) error {
	var body struct {
		Name            string   `json:"name"`
		Description     string   `json:"description"`
		Content         string   `json:"content"`
		Tags            []string `json:"tags"`
		Effort          string   `json:"effort"`
		MaxTurns        int      `json:"max_turns"`
		DisallowedTools []string `json:"disallowed_tools"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Name == "" {
		return fail(c, fiber.StatusBadRequest, "name is required")
	}
	if body.Content == "" {
		return fail(c, fiber.StatusBadRequest, "content is required")
	}

	sk, err := skill.CreateSkill(nil, body.Name, body.Description, body.Content, "global", body.Tags, body.Effort, body.MaxTurns, body.DisallowedTools)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	s.hub.Broadcast(SSEEvent{Type: "skill_created", Data: skillToMap(sk)})

	return success(c, skillToMap(sk))
}

// POST /api/projects/:name/skills — create a project skill
func (s *Server) handleCreateProjectSkill(c *fiber.Ctx) error {
	projectName := paramDecoded(c, "name")

	var body struct {
		Name            string   `json:"name"`
		Description     string   `json:"description"`
		Content         string   `json:"content"`
		Tags            []string `json:"tags"`
		Effort          string   `json:"effort"`
		MaxTurns        int      `json:"max_turns"`
		DisallowedTools []string `json:"disallowed_tools"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Name == "" {
		return fail(c, fiber.StatusBadRequest, "name is required")
	}
	if body.Content == "" {
		return fail(c, fiber.StatusBadRequest, "content is required")
	}

	// Look up project
	ctx := context.Background()
	proj, err := db.Client().Project.Query().
		Where(entProject.Name(projectName)).
		Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	sk, err := skill.CreateSkill(&proj.ID, body.Name, body.Description, body.Content, "project", body.Tags, body.Effort, body.MaxTurns, body.DisallowedTools)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	// Sync to project's .claude/skills/ directory
	if syncErr := skill.SyncSkillToProject(sk, proj.Path); syncErr != nil {
		// Non-fatal: log warning but still return success
		fmt.Printf("Warning: failed to sync skill to .claude/skills/: %v\n", syncErr)
	}

	s.hub.Broadcast(SSEEvent{Type: "skill_created", Data: skillToMap(sk)})

	return success(c, skillToMap(sk))
}

// GET /api/skills/:id — get a skill
func (s *Server) handleGetSkill(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid skill ID")
	}

	sk, err := skill.GetSkill(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	return success(c, skillToMap(sk))
}

// PATCH /api/skills/:id — update a skill
func (s *Server) handleUpdateSkill(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid skill ID")
	}

	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Content     string   `json:"content"`
		Enabled     *bool    `json:"enabled"`
		Tags        []string `json:"tags"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	// If only toggling enabled
	if body.Enabled != nil && body.Name == "" && body.Content == "" {
		sk, err := skill.ToggleEnabled(id, *body.Enabled)
		if err != nil {
			return fail(c, fiber.StatusBadRequest, err.Error())
		}
		s.hub.Broadcast(SSEEvent{Type: "skill_updated", Data: skillToMap(sk)})
		return success(c, skillToMap(sk))
	}

	if body.Name == "" {
		return fail(c, fiber.StatusBadRequest, "name is required")
	}
	if body.Content == "" {
		return fail(c, fiber.StatusBadRequest, "content is required")
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	sk, err := skill.UpdateSkill(id, body.Name, body.Description, body.Content, enabled, body.Tags)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	s.hub.Broadcast(SSEEvent{Type: "skill_updated", Data: skillToMap(sk)})

	return success(c, skillToMap(sk))
}

// DELETE /api/skills/:id — delete a skill
func (s *Server) handleDeleteSkill(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid skill ID")
	}

	// Check if bundled skill
	sk, err := skill.GetSkill(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}
	if sk.Bundled {
		return fail(c, fiber.StatusForbidden, "cannot delete bundled skill")
	}

	if err := skill.DeleteSkill(id); err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	s.hub.Broadcast(SSEEvent{Type: "skill_deleted", Data: fiber.Map{"id": id}})

	return success(c, fiber.Map{"deleted": true})
}

// POST /api/skills/import — import a skill from markdown content
func (s *Server) handleImportSkill(c *fiber.Ctx) error {
	var body struct {
		Content   string `json:"content"`
		ProjectID *int   `json:"project_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Content == "" {
		return fail(c, fiber.StatusBadRequest, "content is required")
	}

	sk, err := skill.ImportSkillFromMarkdown(body.Content, body.ProjectID)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	s.hub.Broadcast(SSEEvent{Type: "skill_created", Data: skillToMap(sk)})

	return success(c, skillToMap(sk))
}

// GET /api/skills/:id/export — export a skill as markdown
func (s *Server) handleExportSkill(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid skill ID")
	}

	sk, err := skill.GetSkill(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	md := skill.ExportSkillToMarkdown(sk)

	c.Set("Content-Type", "text/markdown; charset=utf-8")
	c.Set("Content-Disposition", "attachment; filename=\""+sk.Name+".md\"")
	return c.SendString(md)
}

// skillToMap converts a Skill entity to a response map.
func skillToMap(sk *ent.Skill) fiber.Map {
	m := fiber.Map{
		"id":               sk.ID,
		"name":             sk.Name,
		"description":      sk.Description,
		"content":          sk.Content,
		"scope":            string(sk.Scope),
		"enabled":          sk.Enabled,
		"tags":             sk.Tags,
		"bundled":          sk.Bundled,
		"effort":           sk.Effort,
		"max_turns":        sk.MaxTurns,
		"disallowed_tools": sk.DisallowedTools,
		"created_at":       sk.CreatedAt.Format("2006-01-02 15:04:05"),
		"updated_at":       sk.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
	if sk.Edges.Project != nil {
		m["project"] = sk.Edges.Project.Name
	}
	return m
}
