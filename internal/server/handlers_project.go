package server

import (
	"os/exec"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/project"
)

var sshRemoteRe = regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)

// gitRepoURL returns a GitHub/GitLab HTTPS URL from a project path, or "".
func gitRepoURL(projectPath string) string {
	cmd := exec.Command("git", "-C", projectPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return ""
	}
	// SSH format: git@github.com:user/repo.git
	if m := sshRemoteRe.FindStringSubmatch(raw); m != nil {
		return "https://" + m[1] + "/" + m[2]
	}
	// HTTPS format: https://github.com/user/repo.git
	url := strings.TrimSuffix(raw, ".git")
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		return url
	}
	return ""
}

// GET /api/projects
func (s *Server) handleListProjects(c *fiber.Ctx) error {
	projects, err := project.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	type projectItem struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		Description string `json:"description,omitempty"`
		Sheep       string `json:"sheep,omitempty"`
		RepoURL     string `json:"repo_url,omitempty"`
	}

	var result []projectItem
	for _, p := range projects {
		item := projectItem{
			Name:        p.Name,
			Path:        p.Path,
			Description: p.Description,
			RepoURL:     gitRepoURL(p.Path),
		}
		if p.Edges.Sheep != nil {
			item.Sheep = p.Edges.Sheep.Name
		}
		result = append(result, item)
	}

	return success(c, result)
}

// POST /api/projects
func (s *Server) handleCreateProject(c *fiber.Ctx) error {
	var body struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		Description string `json:"description"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Name == "" || body.Path == "" {
		return fail(c, fiber.StatusBadRequest, "name and path are required")
	}

	result := project.AddWithResult(body.Name, body.Path, body.Description)
	if result.Project == nil {
		errMsg := "failed to create project"
		if result.AssignError != nil {
			errMsg = result.AssignError.Error()
		}
		return fail(c, fiber.StatusBadRequest, errMsg)
	}

	resp := map[string]interface{}{
		"name": result.Project.Name,
		"path": result.Project.Path,
	}
	if result.AssignedSheep != nil {
		resp["assigned_sheep"] = result.AssignedSheep.Name
		resp["sheep_created"] = result.SheepCreated
	}

	return success(c, resp)
}

// GET /api/projects/:name
func (s *Server) handleGetProject(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	result := map[string]interface{}{
		"name":        p.Name,
		"path":        p.Path,
		"description": p.Description,
	}
	if repoURL := gitRepoURL(p.Path); repoURL != "" {
		result["repo_url"] = repoURL
	}
	if p.Edges.Sheep != nil {
		result["sheep"] = p.Edges.Sheep.Name
	}

	return success(c, result)
}

// DELETE /api/projects/:name
func (s *Server) handleDeleteProject(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	if err := project.Remove(name); err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}
	return success(c, nil)
}

// POST /api/projects/:name/assign
func (s *Server) handleAssignSheep(c *fiber.Ctx) error {
	projectName := paramDecoded(c, "name")
	var body struct {
		SheepName string `json:"sheep_name"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.SheepName == "" {
		return fail(c, fiber.StatusBadRequest, "sheep_name is required")
	}

	if err := project.AssignSheep(projectName, body.SheepName); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	return success(c, nil)
}
