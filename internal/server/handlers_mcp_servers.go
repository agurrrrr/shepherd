package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/mcpserver"
	"github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/internal/db"
)

// mcpServerReq is the request body for creating/updating an MCP server.
type mcpServerReq struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Description string `json:"description"`
	Transport string `json:"transport"` // stdio, sse, http
	Command   string `json:"command"`
	Args      string `json:"args"`      // JSON array string
	URL       string `json:"url"`
	Env       string `json:"env"`       // JSON object string
	Enabled   *bool  `json:"enabled"`
}

// mcpServerResp is the response body for an MCP server.
type mcpServerResp struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Transport   string `json:"transport"`
	Command     string `json:"command"`
	Args        string `json:"args"`
	URL         string `json:"url"`
	Env         string `json:"env"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func mcpServerToResp(s *ent.MCPServer) mcpServerResp {
	return mcpServerResp{
		ID:          s.ID,
		Name:        s.Name,
		Label:       s.Label,
		Description: s.Description,
		Transport:   string(s.Transport),
		Command:     s.Command,
		Args:        s.Args,
		URL:         s.URL,
		Env:         s.Env,
		Enabled:     s.Enabled,
		CreatedAt:   s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   s.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// GET /api/mcp/servers
func (s *Server) handleListMCPServers(c *fiber.Ctx) error {
	ctx := context.Background()
	servers, err := db.Client().MCPServer.Query().
		Order(ent.Asc(mcpserver.FieldName)).
		All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	var result []mcpServerResp
	for _, srv := range servers {
		result = append(result, mcpServerToResp(srv))
	}

	return success(c, result)
}

// POST /api/mcp/servers
func (s *Server) handleCreateMCPServer(c *fiber.Ctx) error {
	var body mcpServerReq
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if strings.TrimSpace(body.Name) == "" {
		return fail(c, fiber.StatusBadRequest, "name is required")
	}
	body.Name = strings.ToLower(strings.TrimSpace(body.Name))

	if strings.TrimSpace(body.Transport) == "" {
		body.Transport = "stdio"
	}

	ctx := context.Background()
	client := db.Client()

	// Check for duplicate name
	exists, _ := client.MCPServer.Query().
		Where(mcpserver.Name(body.Name)).
		Exist(ctx)
	if exists {
		return fail(c, fiber.StatusConflict, "MCP server '"+body.Name+"' already exists")
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	srv, err := client.MCPServer.Create().
		SetName(body.Name).
		SetLabel(body.Label).
		SetDescription(body.Description).
		SetTransport(mcpserver.Transport(body.Transport)).
		SetCommand(body.Command).
		SetArgs(body.Args).
		SetURL(body.URL).
		SetEnv(body.Env).
		SetEnabled(enabled).
		Save(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	return success(c, mcpServerToResp(srv))
}

// PUT /api/mcp/servers/:id
func (s *Server) handleUpdateMCPServer(c *fiber.Ctx) error {
	idStr := c.Params("id")
	var body mcpServerReq
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	ctx := context.Background()
	client := db.Client()

	srv, err := client.MCPServer.Get(ctx, parseID(idStr))
	if err != nil {
		return fail(c, fiber.StatusNotFound, "MCP server not found")
	}

	update := client.MCPServer.UpdateOneID(srv.ID)
	if body.Label != "" {
		update.SetLabel(body.Label)
	}
	if body.Description != "" {
		update.SetDescription(body.Description)
	}
	if body.Transport != "" {
		update.SetTransport(mcpserver.Transport(body.Transport))
	}
	if body.Command != "" {
		update.SetCommand(body.Command)
	}
	if body.Args != "" {
		update.SetArgs(body.Args)
	}
	if body.URL != "" {
		update.SetURL(body.URL)
	}
	if body.Env != "" {
		update.SetEnv(body.Env)
	}
	if body.Enabled != nil {
		update.SetEnabled(*body.Enabled)
	}

	srv, err = update.Save(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	return success(c, mcpServerToResp(srv))
}

// DELETE /api/mcp/servers/:id
func (s *Server) handleDeleteMCPServer(c *fiber.Ctx) error {
	ctx := context.Background()
	client := db.Client()

	id := parseID(c.Params("id"))
	if err := client.MCPServer.DeleteOneID(id).Exec(ctx); err != nil {
		return fail(c, fiber.StatusNotFound, "MCP server not found")
	}

	// Remove this server from all project mcp_servers JSON
	projects, _ := client.Project.Query().All(ctx)
	for _, p := range projects {
		if p.McpServers != nil {
			delete(p.McpServers, fmt.Sprintf("%d", id))
			client.Project.UpdateOneID(p.ID).SetMcpServers(p.McpServers).SaveX(ctx)
		}
	}

	return success(c, nil)
}

// GET /api/projects/:name/mcp-servers
// Returns the list of all MCP servers with their per-project enabled status.
func (s *Server) handleGetProjectMCPServers(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")

	ctx := context.Background()
	client := db.Client()

	p, err := client.Project.Query().
		Where(project.Name(name)).
		Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	// Get all global MCP servers
	servers, err := client.MCPServer.Query().
		Order(ent.Asc(mcpserver.FieldName)).
		All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Merge with project-level settings
	type projectMCPServer struct {
		mcpServerResp
		ProjectEnabled bool `json:"project_enabled"`
	}

	var result []projectMCPServer
	for _, srv := range servers {
		resp := mcpServerToResp(srv)
		enabled := srv.Enabled // default to global enabled

		// Check project-level override
		if p.McpServers != nil {
			if entry, ok := p.McpServers[srv.Name]; ok {
				if entryMap, ok := entry.(map[string]interface{}); ok {
					if e, ok := entryMap["enabled"]; ok {
						if eBool, ok := e.(bool); ok {
							enabled = eBool
						}
					}
				}
			}
		}

		result = append(result, projectMCPServer{
			mcpServerResp:  resp,
			ProjectEnabled: enabled,
		})
	}

	return success(c, result)
}

// PUT /api/projects/:name/mcp-servers
// Update per-project MCP server enabled settings.
// Body: { "server_name": { "enabled": true/false }, ... }
func (s *Server) handleUpdateProjectMCPServers(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")

	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	ctx := context.Background()
	client := db.Client()

	p, err := client.Project.Query().
		Where(project.Name(name)).
		Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	// Validate that all server names in the body exist
	allServers, _ := client.MCPServer.Query().All(ctx)
	validNames := make(map[string]bool)
	for _, srv := range allServers {
		validNames[srv.Name] = true
	}

	for key := range body {
		if !validNames[key] {
			return fail(c, fiber.StatusBadRequest, "unknown MCP server: "+key)
		}
	}

	// Merge with existing settings
	existing := p.McpServers
	if existing == nil {
		existing = make(map[string]interface{})
	}
	for key, val := range body {
		existing[key] = val
	}

	_, err = client.Project.UpdateOne(p).SetMcpServers(existing).Save(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	return success(c, existing)
}

// parseID parses a string to int64 for entity ID lookup.
func parseID(idStr string) int {
	var id int
	fmt.Sscanf(idStr, "%d", &id)
	return id
}

// getProjectActiveMCPServers returns the list of active MCP servers for a project.
// This is used by the worker to know which MCP tools to inject.
func getProjectActiveMCPServers(projectName string) ([]*ent.MCPServer, error) {
	ctx := context.Background()
	client := db.Client()

	p, err := client.Project.Query().
		Where(project.Name(projectName)).
		Only(ctx)
	if err != nil {
		return nil, err
	}

	servers, err := client.MCPServer.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	var active []*ent.MCPServer
	for _, srv := range servers {
		enabled := srv.Enabled
		if p.McpServers != nil {
			if entry, ok := p.McpServers[srv.Name]; ok {
				if entryMap, ok := entry.(map[string]interface{}); ok {
					if e, ok := entryMap["enabled"]; ok {
						if eBool, ok := e.(bool); ok {
							enabled = eBool
						}
					}
				}
			}
		}
		if enabled {
			active = append(active, srv)
		}
	}

	return active, nil
}

// validateJSON validates that a string is valid JSON (for args/env fields).
func validateJSON(s string) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), &struct{}{})
}
