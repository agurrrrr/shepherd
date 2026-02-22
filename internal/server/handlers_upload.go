package server

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/project"
)

// POST /api/upload
func (s *Server) handleUpload(c *fiber.Ctx) error {
	projectName := c.FormValue("project_name")
	if projectName == "" {
		return fail(c, fiber.StatusBadRequest, "project_name is required")
	}

	p, err := project.Get(projectName)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	form, err := c.MultipartForm()
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid multipart form")
	}

	files := form.File["files"]
	if len(files) == 0 {
		return fail(c, fiber.StatusBadRequest, "no files provided")
	}

	// Create upload directory inside project path
	uploadDir := filepath.Join(p.Path, ".shepherd-uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to create upload directory")
	}

	type uploadedFile struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	}

	var result []uploadedFile
	ts := time.Now().Unix()

	for _, fh := range files {
		if fh.Size > 10*1024*1024 {
			return fail(c, fiber.StatusBadRequest, fmt.Sprintf("file %s exceeds 10MB limit", fh.Filename))
		}

		saveName := fmt.Sprintf("%d_%s", ts, fh.Filename)
		savePath := filepath.Join(uploadDir, saveName)

		if err := c.SaveFile(fh, savePath); err != nil {
			return fail(c, fiber.StatusInternalServerError, fmt.Sprintf("failed to save %s", fh.Filename))
		}

		result = append(result, uploadedFile{
			Name: fh.Filename,
			Path: savePath,
			Size: fh.Size,
		})
	}

	return success(c, fiber.Map{"files": result})
}
