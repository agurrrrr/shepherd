package server

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/spec"
)

// GET /api/projects/:name/specs
func (s *Server) handleListSpecs(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	specDir := filepath.Join(p.Path, "spec")

	// If spec dir doesn't exist, return empty list
	if _, err := os.Stat(specDir); os.IsNotExist(err) {
		return success(c, []map[string]interface{}{})
	}

	var specs []map[string]interface{}
	err = filepath.WalkDir(specDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			rel, _ := filepath.Rel(specDir, path)
			info, infoErr := d.Info()
			modTime := ""
			if infoErr == nil {
				modTime = info.ModTime().Format("2006-01-02 15:04:05")
			}
			specs = append(specs, map[string]interface{}{
				"name":        d.Name(),
				"path":        rel,
				"modified_at": modTime,
				"size":        func() int64 { if infoErr == nil { return info.Size() }; return 0 }(),
			})
		}
		return nil
	})
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to scan specs")
	}

	if specs == nil {
		specs = []map[string]interface{}{}
	}
	return success(c, specs)
}

// GET /api/projects/:name/specs/*
func (s *Server) handleGetSpec(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	specPath := c.Params("*")
	if specPath == "" {
		return fail(c, fiber.StatusBadRequest, "spec path required")
	}

	specDir := filepath.Join(p.Path, "spec")
	fullPath := filepath.Join(specDir, specPath)
	fullPath = filepath.Clean(fullPath)
	if !strings.HasPrefix(fullPath, filepath.Clean(specDir)) {
		return fail(c, fiber.StatusBadRequest, "invalid path")
	}

	if !strings.HasSuffix(strings.ToLower(fullPath), ".md") {
		return fail(c, fiber.StatusBadRequest, "only .md files allowed")
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "file not found")
	}

	return success(c, map[string]string{
		"name":    filepath.Base(fullPath),
		"path":    specPath,
		"content": string(content),
	})
}

// GET /api/projects/:name/specs-download/*
func (s *Server) handleDownloadSpec(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	specPath := c.Params("*")
	if specPath == "" {
		return fail(c, fiber.StatusBadRequest, "spec path required")
	}

	specDir := filepath.Join(p.Path, "spec")
	fullPath := filepath.Join(specDir, specPath)
	fullPath = filepath.Clean(fullPath)
	if !strings.HasPrefix(fullPath, filepath.Clean(specDir)) {
		return fail(c, fiber.StatusBadRequest, "invalid path")
	}

	if !strings.HasSuffix(strings.ToLower(fullPath), ".md") {
		return fail(c, fiber.StatusBadRequest, "only .md files allowed")
	}

	c.Set("Content-Disposition", "attachment; filename=\""+filepath.Base(fullPath)+"\"")
	return c.SendFile(fullPath)
}

// GET /api/spec-types
func (s *Server) handleListSpecTypes(c *fiber.Ctx) error {
	return success(c, spec.SpecTypes)
}
