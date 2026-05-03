package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

// GET /api/settings/db-backup
//
// Streams a consistent snapshot of the SQLite database to the client.
// Uses `VACUUM INTO` so that WAL pages are checkpointed and the resulting
// file is a single, self-contained .db that can be opened directly.
func (s *Server) handleDownloadDBBackup(c *fiber.Ctx) error {
	rawDB := db.RawDB()
	if rawDB == nil {
		return fail(c, fiber.StatusInternalServerError, "database not initialized")
	}

	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("shepherd-backup-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmpFile)
	// VACUUM INTO target must not already exist.
	_ = os.Remove(tmpFile)

	// VACUUM INTO requires a literal path in SQL — escape single quotes.
	escaped := strings.ReplaceAll(tmpFile, "'", "''")
	if _, err := rawDB.ExecContext(c.Context(), fmt.Sprintf("VACUUM INTO '%s'", escaped)); err != nil {
		return fail(c, fiber.StatusInternalServerError, "backup failed: "+err.Error())
	}

	filename := "shepherd-" + time.Now().UTC().Format("20060102-150405") + ".db"
	c.Set("Content-Type", "application/octet-stream")
	c.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	return c.SendFile(tmpFile)
}

// GET /api/settings/tasks-export?project=<name>
//
// Streams the task table as JSONL. First line is a metadata object,
// subsequent lines are one task each. Time fields are ISO-8601 UTC so the
// dump is human-readable and `grep`-able.
func (s *Server) handleExportTasks(c *fiber.Ctx) error {
	projectName := c.Query("project")
	ctx := context.Background()
	client := db.Client()

	q := client.Task.Query()
	if projectName != "" {
		q = q.Where(entTask.HasProjectWith(entProject.Name(projectName)))
	}
	tasks, err := q.WithProject().WithSheep().Order(ent.Asc(entTask.FieldID)).All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	hostname, _ := os.Hostname()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)

	meta := map[string]any{
		"_type":          "meta",
		"version":        1,
		"exported_at":    time.Now().UTC().Format(time.RFC3339),
		"source_host":    hostname,
		"project_filter": projectName,
		"task_count":     len(tasks),
	}
	if err := enc.Encode(meta); err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	for _, t := range tasks {
		var sheepName, projName string
		if t.Edges.Sheep != nil {
			sheepName = t.Edges.Sheep.Name
		}
		if t.Edges.Project != nil {
			projName = t.Edges.Project.Name
		}
		// Skip records without a project — they cannot be matched on import.
		if projName == "" {
			continue
		}
		rec := map[string]any{
			"_type":          "task",
			"original_id":    t.ID,
			"prompt":         t.Prompt,
			"summary":        t.Summary,
			"status":         string(t.Status),
			"error":          t.Error,
			"files_modified": t.FilesModified,
			"output":         t.Output,
			"cost_usd":       t.CostUsd,
			"created_at":     t.CreatedAt.UTC().Format(time.RFC3339Nano),
			"sheep_name":     sheepName,
			"project_name":   projName,
		}
		if !t.StartedAt.IsZero() {
			rec["started_at"] = t.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if !t.CompletedAt.IsZero() {
			rec["completed_at"] = t.CompletedAt.UTC().Format(time.RFC3339Nano)
		}
		if err := enc.Encode(rec); err != nil {
			return fail(c, fiber.StatusInternalServerError, err.Error())
		}
	}

	filename := "shepherd-tasks"
	if projectName != "" {
		filename += "-" + sanitizeFilename(projectName)
	}
	filename += "-" + time.Now().UTC().Format("20060102-150405") + ".jsonl"

	c.Set("Content-Type", "application/x-ndjson")
	c.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	return c.Send(buf.Bytes())
}

// taskRecord mirrors the JSONL task line. Fields are intentionally string-typed
// for time so we can tolerate both RFC3339 and RFC3339Nano on import.
type taskRecord struct {
	Type          string   `json:"_type"`
	OriginalID    int      `json:"original_id"`
	Prompt        string   `json:"prompt"`
	Summary       string   `json:"summary"`
	Status        string   `json:"status"`
	Error         string   `json:"error"`
	FilesModified []string `json:"files_modified"`
	Output        []string `json:"output"`
	CostUSD       float64  `json:"cost_usd"`
	CreatedAt     string   `json:"created_at"`
	StartedAt     string   `json:"started_at"`
	CompletedAt   string   `json:"completed_at"`
	SheepName     string   `json:"sheep_name"`
	ProjectName   string   `json:"project_name"`
}

func parseImportFile(c *fiber.Ctx) ([]taskRecord, map[string]any, error) {
	fh, err := c.FormFile("file")
	if err != nil {
		return nil, nil, fmt.Errorf("file is required")
	}
	if fh.Size > 50*1024*1024 {
		return nil, nil, fmt.Errorf("file too large (max 50MB)")
	}
	f, err := fh.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open upload: %w", err)
	}
	defer f.Close()

	var meta map[string]any
	var records []taskRecord

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	lineno := 0
	for scanner.Scan() {
		lineno++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type string `json:"_type"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			return nil, nil, fmt.Errorf("invalid JSON at line %d: %v", lineno, err)
		}
		switch probe.Type {
		case "meta":
			if err := json.Unmarshal(line, &meta); err != nil {
				return nil, nil, fmt.Errorf("bad meta at line %d: %v", lineno, err)
			}
		case "task":
			var rec taskRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return nil, nil, fmt.Errorf("bad task at line %d: %v", lineno, err)
			}
			records = append(records, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read error: %w", err)
	}
	return records, meta, nil
}

// POST /api/settings/tasks-import-preview
//
// Parses the uploaded JSONL and reports how many records would land in
// existing projects vs. be skipped. Does not write to the DB.
func (s *Server) handleImportTasksPreview(c *fiber.Ctx) error {
	records, meta, err := parseImportFile(c)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	ctx := context.Background()
	client := db.Client()
	projects, err := client.Project.Query().All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}
	existing := make(map[string]bool, len(projects))
	for _, p := range projects {
		existing[p.Name] = true
	}

	matchedByProject := make(map[string]int)
	skippedByProject := make(map[string]int)
	matched := 0
	skipped := 0

	for _, r := range records {
		if r.ProjectName == "" || !existing[r.ProjectName] {
			skipped++
			key := r.ProjectName
			if key == "" {
				key = "(none)"
			}
			skippedByProject[key]++
			continue
		}
		matched++
		matchedByProject[r.ProjectName]++
	}

	return success(c, map[string]any{
		"meta":               meta,
		"total":              len(records),
		"matched":            matched,
		"skipped":            skipped,
		"matched_by_project": matchedByProject,
		"skipped_by_project": skippedByProject,
	})
}

// POST /api/settings/tasks-import
//
// Same upload contract as the preview, but actually writes the records.
// Matching rule: only records whose project_name corresponds to an existing
// project are imported. Sheep is matched by name when present; if absent the
// record is still imported with no sheep edge. Duplicates are detected by
// (project_id, prompt, created_at) so that re-running with the same dump is
// idempotent.
func (s *Server) handleImportTasks(c *fiber.Ctx) error {
	records, _, err := parseImportFile(c)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	ctx := context.Background()
	client := db.Client()

	projects, err := client.Project.Query().All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}
	projIDByName := make(map[string]int, len(projects))
	for _, p := range projects {
		projIDByName[p.Name] = p.ID
	}

	sheeps, err := client.Sheep.Query().All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}
	sheepIDByName := make(map[string]int, len(sheeps))
	for _, sh := range sheeps {
		sheepIDByName[sh.Name] = sh.ID
	}

	imported := 0
	skipped := 0
	duplicates := 0
	failed := 0

	for _, r := range records {
		projID, ok := projIDByName[r.ProjectName]
		if !ok {
			skipped++
			continue
		}

		createdAt, err := parseFlexibleTime(r.CreatedAt)
		if err != nil {
			failed++
			continue
		}

		exists, err := client.Task.Query().
			Where(
				entTask.HasProjectWith(entProject.ID(projID)),
				entTask.PromptEQ(r.Prompt),
				entTask.CreatedAtEQ(createdAt),
			).
			Exist(ctx)
		if err == nil && exists {
			duplicates++
			continue
		}

		status := entTask.Status(r.Status)
		if !isValidStatus(status) {
			status = entTask.StatusCompleted
		}

		creator := client.Task.Create().
			SetPrompt(r.Prompt).
			SetSummary(r.Summary).
			SetStatus(status).
			SetError(r.Error).
			SetCostUsd(r.CostUSD).
			SetCreatedAt(createdAt).
			SetProjectID(projID)

		if r.FilesModified != nil {
			creator = creator.SetFilesModified(r.FilesModified)
		}
		if r.Output != nil {
			creator = creator.SetOutput(r.Output)
		}
		if r.StartedAt != "" {
			if t, err := parseFlexibleTime(r.StartedAt); err == nil {
				creator = creator.SetStartedAt(t)
			}
		}
		if r.CompletedAt != "" {
			if t, err := parseFlexibleTime(r.CompletedAt); err == nil {
				creator = creator.SetCompletedAt(t)
			}
		}
		if r.SheepName != "" {
			if id, ok := sheepIDByName[r.SheepName]; ok {
				creator = creator.SetSheepID(id)
			}
		}

		if _, err := creator.Save(ctx); err != nil {
			failed++
			continue
		}
		imported++
	}

	return success(c, map[string]any{
		"imported":   imported,
		"skipped":    skipped,
		"duplicates": duplicates,
		"failed":     failed,
		"total":      len(records),
	})
}

func parseFlexibleTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", s)
}

func isValidStatus(s entTask.Status) bool {
	switch s {
	case entTask.StatusPending, entTask.StatusRunning, entTask.StatusCompleted, entTask.StatusFailed, entTask.StatusStopped:
		return true
	}
	return false
}

func sanitizeFilename(s string) string {
	repl := strings.NewReplacer("/", "_", "\\", "_", "..", "_", "\"", "_", " ", "_")
	return repl.Replace(s)
}
