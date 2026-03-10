package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/project"
)

const maxFileViewSize = 1 << 20 // 1MB

// Directories always excluded from file listing.
var excludedDirs = map[string]bool{
	"node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".idea": true, ".vscode": true, "dist": true, "build": true,
	".terraform": true, ".gradle": true, ".cache": true,
}

// Extension to highlight.js language name.
var extToLanguage = map[string]string{
	".go": "go", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".jsx": "javascript",
	".py": "python", ".rs": "rust", ".java": "java", ".kt": "kotlin",
	".c": "c", ".cpp": "cpp", ".h": "c", ".hpp": "cpp",
	".cs": "csharp", ".rb": "ruby", ".php": "php", ".swift": "swift",
	".css": "css", ".scss": "scss", ".less": "less",
	".html": "html", ".htm": "html", ".svelte": "html", ".vue": "html",
	".json": "json", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml",
	".xml": "xml", ".sql": "sql", ".graphql": "graphql",
	".md": "markdown", ".sh": "bash", ".bash": "bash", ".zsh": "bash",
	".ps1": "powershell", ".bat": "dos", ".cmd": "dos",
	".dockerfile": "dockerfile", ".tf": "hcl",
	".makefile": "makefile", ".r": "r", ".lua": "lua", ".dart": "dart",
	".proto": "protobuf", ".nginx": "nginx",
}

// GET /api/projects/:name/files?path=<dir>
func (s *Server) handleListFiles(c *fiber.Ctx) error {
	if !config.GetBool("enable_file_browser") {
		return fail(c, fiber.StatusForbidden, "file browser is disabled")
	}

	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	dirPath := c.Query("path", "")
	fullDir := filepath.Clean(filepath.Join(p.Path, dirPath))
	if !strings.HasPrefix(fullDir, filepath.Clean(p.Path)) {
		return fail(c, fiber.StatusBadRequest, "invalid path")
	}

	entries, err := os.ReadDir(fullDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fail(c, fiber.StatusNotFound, "directory not found")
		}
		return fail(c, fiber.StatusInternalServerError, "failed to read directory")
	}

	type fileEntry struct {
		Name       string `json:"name"`
		Path       string `json:"path"`
		IsDir      bool   `json:"is_dir"`
		Size       int64  `json:"size"`
		ModifiedAt string `json:"modified_at"`
	}

	var dirs, files []fileEntry
	for _, entry := range entries {
		entryName := entry.Name()

		// Skip hidden files/dirs
		if strings.HasPrefix(entryName, ".") {
			continue
		}

		// Skip excluded directories
		if entry.IsDir() && excludedDirs[entryName] {
			continue
		}

		// Resolve symlinks — reject if target is outside project
		entryPath := filepath.Join(fullDir, entryName)
		info, err := os.Stat(entryPath) // follows symlinks
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(entryPath)
		if err != nil || !strings.HasPrefix(resolved, filepath.Clean(p.Path)) {
			continue
		}

		rel, _ := filepath.Rel(p.Path, entryPath)
		fe := fileEntry{
			Name:  entryName,
			Path:  rel,
			IsDir: info.IsDir(),
			Size:  info.Size(),
		}
		if !info.IsDir() {
			fe.ModifiedAt = info.ModTime().Format("2006-01-02 15:04:05")
		}

		if fe.IsDir {
			dirs = append(dirs, fe)
		} else {
			files = append(files, fe)
		}
	}

	// Sort: dirs alphabetical, then files alphabetical
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	result := make([]fileEntry, 0, len(dirs)+len(files))
	result = append(result, dirs...)
	result = append(result, files...)

	return success(c, result)
}

// GET /api/projects/:name/files/content/*
func (s *Server) handleGetFileContent(c *fiber.Ctx) error {
	if !config.GetBool("enable_file_browser") {
		return fail(c, fiber.StatusForbidden, "file browser is disabled")
	}

	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	filePath := c.Params("*")
	if filePath == "" {
		return fail(c, fiber.StatusBadRequest, "file path required")
	}

	fullPath := filepath.Clean(filepath.Join(p.Path, filePath))
	if !strings.HasPrefix(fullPath, filepath.Clean(p.Path)) {
		return fail(c, fiber.StatusBadRequest, "invalid path")
	}

	// Reject hidden path segments
	for _, seg := range strings.Split(filePath, string(filepath.Separator)) {
		if strings.HasPrefix(seg, ".") {
			return fail(c, fiber.StatusForbidden, "access denied")
		}
	}
	// Also check forward slash (URL path separator)
	for _, seg := range strings.Split(filePath, "/") {
		if strings.HasPrefix(seg, ".") {
			return fail(c, fiber.StatusForbidden, "access denied")
		}
	}

	// Resolve symlinks
	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "file not found")
	}
	if !strings.HasPrefix(resolved, filepath.Clean(p.Path)) {
		return fail(c, fiber.StatusForbidden, "access denied")
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "file not found")
	}
	if info.IsDir() {
		return fail(c, fiber.StatusBadRequest, "path is a directory")
	}

	baseName := filepath.Base(fullPath)
	ext := strings.ToLower(filepath.Ext(baseName))
	lang := extToLanguage[ext]
	// Special case: Makefile, Dockerfile without extension
	nameLower := strings.ToLower(baseName)
	if lang == "" {
		switch {
		case nameLower == "makefile":
			lang = "makefile"
		case nameLower == "dockerfile" || strings.HasPrefix(nameLower, "dockerfile."):
			lang = "dockerfile"
		}
	}

	// Size check
	if info.Size() > maxFileViewSize {
		return success(c, fiber.Map{
			"name":         baseName,
			"path":         filePath,
			"size":         info.Size(),
			"is_too_large": true,
			"language":     lang,
		})
	}

	// Binary detection: read first 512 bytes
	data, err := os.ReadFile(resolved)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to read file")
	}

	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	mimeType := http.DetectContentType(sample)
	if !strings.HasPrefix(mimeType, "text/") && mimeType != "application/json" && mimeType != "application/xml" {
		return success(c, fiber.Map{
			"name":      baseName,
			"path":      filePath,
			"size":      info.Size(),
			"is_binary": true,
			"mime_type": mimeType,
			"language":  lang,
		})
	}

	return success(c, fiber.Map{
		"name":      baseName,
		"path":      filePath,
		"content":   string(data),
		"size":      info.Size(),
		"language":  lang,
		"is_binary": false,
	})
}

// GET /api/projects/:name/files/download/*
func (s *Server) handleDownloadFile(c *fiber.Ctx) error {
	if !config.GetBool("enable_file_browser") {
		return fail(c, fiber.StatusForbidden, "file browser is disabled")
	}

	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	filePath := c.Params("*")
	if filePath == "" {
		return fail(c, fiber.StatusBadRequest, "file path required")
	}

	fullPath := filepath.Clean(filepath.Join(p.Path, filePath))
	if !strings.HasPrefix(fullPath, filepath.Clean(p.Path)) {
		return fail(c, fiber.StatusBadRequest, "invalid path")
	}

	// Reject hidden path segments
	for _, seg := range strings.Split(filePath, "/") {
		if strings.HasPrefix(seg, ".") {
			return fail(c, fiber.StatusForbidden, "access denied")
		}
	}

	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "file not found")
	}
	if !strings.HasPrefix(resolved, filepath.Clean(p.Path)) {
		return fail(c, fiber.StatusForbidden, "access denied")
	}

	c.Set("Content-Disposition", "attachment; filename=\""+filepath.Base(fullPath)+"\"")
	return c.SendFile(resolved)
}
