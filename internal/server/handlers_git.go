package server

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/project"
)

// validateGitRef validates a git ref name (branch, tag).
// Allowed: alphanumeric, -, _, /, .
// Forbidden patterns: .., ~, ^, :, ?, *, [, \, whitespace
// Forbidden prefixes: -, refs/
// Max length: 256
var validGitRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-_/.]*$`)

func isValidGitRef(ref string) bool {
	if len(ref) == 0 || len(ref) > 256 {
		return false
	}
	if !validGitRef.MatchString(ref) {
		return false
	}
	if strings.Contains(ref, "..") || strings.HasPrefix(ref, "refs/") {
		return false
	}
	return true
}

// validateCommitHash validates a hex commit hash (7-40 chars, hex only).
func isValidCommitHash(hash string) bool {
	if len(hash) < 7 || len(hash) > 40 {
		return false
	}
	for _, ch := range hash {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

// validateFilePath validates a file path for git commands.
// Blocks path traversal and null bytes.
var invalidPathPattern = regexp.MustCompile(`[\x00\n\r]`)

func isValidFilePath(p string) bool {
	if len(p) == 0 || len(p) > 1024 {
		return false
	}
	if invalidPathPattern.MatchString(p) {
		return false
	}
	if strings.HasPrefix(p, "-") {
		return false
	}
	return true
}

// gitCommit represents a single commit for the log endpoint.
type gitCommit struct {
	Hash      string   `json:"hash"`
	ShortHash string   `json:"short_hash"`
	Author    string   `json:"author"`
	Email     string   `json:"email"`
	Date      string   `json:"date"`
	Subject   string   `json:"subject"`
	Parents   []string `json:"parents"`
	Refs      []string `json:"refs"`
}

// GET /api/projects/:name/git/log
func (s *Server) handleGitLog(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	limit := c.QueryInt("limit", 100)
	if limit < 1 || limit > 500 {
		limit = 100
	}
	skip := c.QueryInt("skip", 0)
	if skip < 0 {
		skip = 0
	}

	// Build git log command
	args := []string{"-C", p.Path, "log",
		"--format=%H|%h|%an|%ae|%aI|%s|%P|%D",
		"--all",
		fmt.Sprintf("-n%d", limit),
		fmt.Sprintf("--skip=%d", skip),
	}
	if branch := c.Query("branch"); branch != "" {
		if !isValidGitRef(branch) {
			return fail(c, fiber.StatusBadRequest, "invalid branch name")
		}
		// Replace --all with specific branch
		args = []string{"-C", p.Path, "log",
			"--format=%H|%h|%an|%ae|%aI|%s|%P|%D",
			fmt.Sprintf("-n%d", limit),
			fmt.Sprintf("--skip=%d", skip),
			branch,
		}
	}

	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "git log failed: "+err.Error())
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	commits := make([]gitCommit, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 8)
		if len(parts) < 8 {
			continue
		}

		var parents []string
		if parts[6] != "" {
			parents = strings.Split(parts[6], " ")
		}

		var refs []string
		if parts[7] != "" {
			for _, r := range strings.Split(parts[7], ", ") {
				r = strings.TrimSpace(r)
				if r != "" {
					refs = append(refs, r)
				}
			}
		}

		commits = append(commits, gitCommit{
			Hash:      parts[0],
			ShortHash: parts[1],
			Author:    parts[2],
			Email:     parts[3],
			Date:      parts[4],
			Subject:   parts[5],
			Parents:   parents,
			Refs:      refs,
		})
	}

	// Get total commit count
	countCmd := exec.Command("git", "-C", p.Path, "rev-list", "--count", "--all")
	countOut, _ := countCmd.Output()
	total, _ := strconv.Atoi(strings.TrimSpace(string(countOut)))

	return success(c, fiber.Map{
		"commits": commits,
		"total":   total,
	})
}

// gitBranch represents a branch.
type gitBranch struct {
	Name      string `json:"name"`
	Head      string `json:"head"`
	IsCurrent bool   `json:"is_current"`
	IsRemote  bool   `json:"is_remote"`
}

// GET /api/projects/:name/git/branches
func (s *Server) handleGitBranches(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	cmd := exec.Command("git", "-C", p.Path, "branch", "-a",
		"--format=%(refname:short)|%(objectname:short)|%(HEAD)")
	out, err := cmd.Output()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "git branch failed: "+err.Error())
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	branches := make([]gitBranch, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		branches = append(branches, gitBranch{
			Name:      parts[0],
			Head:      parts[1],
			IsCurrent: strings.TrimSpace(parts[2]) == "*",
			IsRemote:  strings.HasPrefix(parts[0], "origin/"),
		})
	}

	return success(c, branches)
}

// GET /api/projects/:name/git/commits/:hash
func (s *Server) handleGitCommitDetail(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	hash := c.Params("hash")
	if !isValidCommitHash(hash) {
		return fail(c, fiber.StatusBadRequest, "invalid commit hash")
	}

	// Get commit metadata using NUL byte separator (%x00) to avoid issues
	// with pipe characters or newlines in commit messages
	cmd := exec.Command("git", "-C", p.Path, "log", "-1",
		"--format=%H%x00%h%x00%an%x00%ae%x00%aI%x00%s%x00%b%x00%P%x00%D",
		hash)
	out, err := cmd.Output()
	if err != nil {
		return fail(c, fiber.StatusNotFound, "commit not found")
	}

	parts := strings.SplitN(string(out), "\x00", 9)
	if len(parts) < 9 {
		return fail(c, fiber.StatusInternalServerError, "unexpected format")
	}

	subject := strings.TrimSpace(parts[5])
	body := strings.TrimSpace(parts[6])
	// Combine subject + body for full message
	fullBody := subject
	if body != "" {
		fullBody = subject + "\n\n" + body
	}

	var parents []string
	if p7 := strings.TrimSpace(parts[7]); p7 != "" {
		parents = strings.Split(p7, " ")
	}

	var refs []string
	if p8 := strings.TrimSpace(parts[8]); p8 != "" {
		for _, r := range strings.Split(p8, ", ") {
			r = strings.TrimSpace(r)
			if r != "" {
				refs = append(refs, r)
			}
		}
	}

	// Get file changes using numstat (exact add/del counts, tab-separated)
	type fileChange struct {
		Path      string `json:"path"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	}

	statCmd := exec.Command("git", "-C", p.Path, "diff-tree",
		"--no-commit-id", "--numstat", "-r", hash)
	statOut, _ := statCmd.Output()

	var files []fileChange
	for _, sl := range strings.Split(strings.TrimSpace(string(statOut)), "\n") {
		// numstat format: "additions\tdeletions\tfilename"
		fields := strings.SplitN(sl, "\t", 3)
		if len(fields) == 3 {
			adds, _ := strconv.Atoi(fields[0])
			dels, _ := strconv.Atoi(fields[1])
			files = append(files, fileChange{
				Path:      fields[2],
				Additions: adds,
				Deletions: dels,
			})
		}
	}

	if files == nil {
		files = []fileChange{}
	}

	return success(c, fiber.Map{
		"hash":       strings.TrimSpace(parts[0]),
		"short_hash": strings.TrimSpace(parts[1]),
		"author":     strings.TrimSpace(parts[2]),
		"email":      strings.TrimSpace(parts[3]),
		"date":       strings.TrimSpace(parts[4]),
		"body":       fullBody,
		"parents":    parents,
		"refs":       refs,
		"files":      files,
	})
}

// GET /api/projects/:name/git/changes
func (s *Server) handleGitChanges(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	cmd := exec.Command("git", "-C", p.Path, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "git status failed: "+err.Error())
	}

	type changeEntry struct {
		Path   string `json:"path"`
		Status string `json:"status"`
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	changes := make([]changeEntry, 0, len(lines))
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		if path != "" {
			changes = append(changes, changeEntry{
				Path:   path,
				Status: status,
			})
		}
	}

	return success(c, changes)
}

// --- Diff types and parser ---

type diffLine struct {
	Type    string `json:"type"`    // "add", "delete", "context"
	Content string `json:"content"` // line content (without prefix)
	OldLine int    `json:"old_line"`
	NewLine int    `json:"new_line"`
}

type diffHunk struct {
	Header   string     `json:"header"`
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Lines    []diffLine `json:"lines"`
}

type fileDiff struct {
	Path      string     `json:"path"`
	Status    string     `json:"status"` // "added", "modified", "deleted"
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Hunks     []diffHunk `json:"hunks"`
	IsBinary  bool       `json:"is_binary"`
}

// parseUnifiedDiff parses git unified diff output into structured data.
func parseUnifiedDiff(raw string) *fileDiff {
	fd := &fileDiff{Status: "modified"}
	lines := strings.Split(raw, "\n")

	var currentHunk *diffHunk
	oldLine, newLine := 0, 0

	for _, line := range lines {
		// Detect file status from diff headers
		if strings.HasPrefix(line, "new file") {
			fd.Status = "added"
			continue
		}
		if strings.HasPrefix(line, "deleted file") {
			fd.Status = "deleted"
			continue
		}
		if strings.HasPrefix(line, "Binary files") || strings.HasPrefix(line, "GIT binary") {
			fd.IsBinary = true
			continue
		}
		// Skip diff metadata
		if strings.HasPrefix(line, "diff --git") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ") {
			continue
		}

		// Parse hunk header: @@ -oldStart,oldLines +newStart,newLines @@
		if strings.HasPrefix(line, "@@") {
			if currentHunk != nil {
				fd.Hunks = append(fd.Hunks, *currentHunk)
			}
			h := diffHunk{Header: line}
			// Parse line numbers
			_, _ = fmt.Sscanf(line, "@@ -%d,%d +%d,%d @@",
				&h.OldStart, &h.OldLines, &h.NewStart, &h.NewLines)
			if h.OldStart == 0 && h.OldLines == 0 {
				fmt.Sscanf(line, "@@ -%d +%d,%d @@", &h.OldStart, &h.NewStart, &h.NewLines)
				h.OldLines = 1
			}
			if h.NewStart == 0 && h.NewLines == 0 {
				fmt.Sscanf(line, "@@ -%d,%d +%d @@", &h.OldStart, &h.OldLines, &h.NewStart)
				h.NewLines = 1
			}
			oldLine = h.OldStart
			newLine = h.NewStart
			currentHunk = &h
			continue
		}

		if currentHunk == nil {
			continue
		}

		if strings.HasPrefix(line, "+") {
			currentHunk.Lines = append(currentHunk.Lines, diffLine{
				Type:    "add",
				Content: line[1:],
				OldLine: 0,
				NewLine: newLine,
			})
			newLine++
			fd.Additions++
		} else if strings.HasPrefix(line, "-") {
			currentHunk.Lines = append(currentHunk.Lines, diffLine{
				Type:    "delete",
				Content: line[1:],
				OldLine: oldLine,
				NewLine: 0,
			})
			oldLine++
			fd.Deletions++
		} else {
			// Context line (starts with space or is empty)
			content := line
			if len(content) > 0 && content[0] == ' ' {
				content = content[1:]
			}
			currentHunk.Lines = append(currentHunk.Lines, diffLine{
				Type:    "context",
				Content: content,
				OldLine: oldLine,
				NewLine: newLine,
			})
			oldLine++
			newLine++
		}
	}

	if currentHunk != nil {
		fd.Hunks = append(fd.Hunks, *currentHunk)
	}
	if fd.Hunks == nil {
		fd.Hunks = []diffHunk{}
	}

	return fd
}

// GET /api/projects/:name/git/commits/:hash/diff?file=path
func (s *Server) handleGitCommitDiff(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	hash := c.Params("hash")
	if !isValidCommitHash(hash) {
		return fail(c, fiber.StatusBadRequest, "invalid commit hash")
	}

	filePath := c.Query("file")
	if filePath == "" {
		return fail(c, fiber.StatusBadRequest, "file query param required")
	}
	if !isValidFilePath(filePath) {
		return fail(c, fiber.StatusBadRequest, "invalid file path")
	}

	cmd := exec.Command("git", "-C", p.Path, "show",
		"--format=", "-p", "--unified=3",
		hash, "--", filePath)
	out, err := cmd.Output()
	if err != nil {
		return fail(c, fiber.StatusNotFound, "diff not found")
	}

	fd := parseUnifiedDiff(string(out))
	fd.Path = filePath

	return success(c, fd)
}
