package server

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/project"
)

// GET /api/projects/:name/docs
func (s *Server) handleListDocs(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	var docs []map[string]interface{}
	err = filepath.WalkDir(p.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		// Skip hidden directories and node_modules
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			rel, _ := filepath.Rel(p.Path, path)
			info, infoErr := d.Info()
			modTime := ""
			if infoErr == nil {
				modTime = info.ModTime().Format("2006-01-02 15:04:05")
			}
			docs = append(docs, map[string]interface{}{
				"name":        d.Name(),
				"path":        rel,
				"modified_at": modTime,
				"size":        func() int64 { if infoErr == nil { return info.Size() }; return 0 }(),
			})
		}
		return nil
	})
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to scan docs")
	}

	if docs == nil {
		docs = []map[string]interface{}{}
	}
	return success(c, docs)
}

// GET /api/projects/:name/docs/*
func (s *Server) handleGetDoc(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	// Get the wildcard part (file path)
	docPath := c.Params("*")
	if docPath == "" {
		return fail(c, fiber.StatusBadRequest, "doc path required")
	}

	// Prevent path traversal
	fullPath := filepath.Join(p.Path, docPath)
	fullPath = filepath.Clean(fullPath)
	if !strings.HasPrefix(fullPath, filepath.Clean(p.Path)) {
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
		"path":    docPath,
		"content": string(content),
	})
}
