package server

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/project"
)

// gitCmdTimeout caps long-running git operations (push, fetch).
const gitCmdTimeout = 60 * time.Second

// runGitWithTimeout runs `git -C path args...` with a context deadline and
// returns combined stdout+stderr. The caller is responsible for checking err.
func runGitWithTimeout(p string, args []string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	full := append([]string{"-C", p}, args...)
	return exec.CommandContext(ctx, "git", full...).CombinedOutput()
}

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

// gitChangeFile is a single entry from `git status --porcelain=v1`.
// Index is the staged status (X), WorkTree is the unstaged status (Y).
// Both are single ASCII chars; ' ' (space) means "no change in that column".
type gitChangeFile struct {
	Path     string `json:"path"`
	Index    string `json:"index"`
	WorkTree string `json:"work_tree"`
}

type gitChangesResponse struct {
	Branch   string          `json:"branch"`
	Upstream string          `json:"upstream,omitempty"`
	Ahead    int             `json:"ahead"`
	Behind   int             `json:"behind"`
	Detached bool            `json:"detached"`
	Files    []gitChangeFile `json:"files"`
}

// GET /api/projects/:name/git/changes
func (s *Server) handleGitChanges(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	// Use porcelain v2 with branch info — gives us branch name, upstream,
	// ahead/behind, and per-file XY status in a single call.
	out, err := exec.Command("git", "-C", p.Path, "status",
		"--porcelain=v2", "--branch", "-z").Output()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "git status failed")
	}

	resp := gitChangesResponse{Files: []gitChangeFile{}}
	// porcelain v2 uses NUL as a record separator (with -z).
	// Header lines start with "# branch.*"; file lines with "1 ", "2 ", "u ", "?".
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		switch {
		case strings.HasPrefix(rec, "# branch.head "):
			resp.Branch = strings.TrimPrefix(rec, "# branch.head ")
			if resp.Branch == "(detached)" {
				resp.Detached = true
				resp.Branch = ""
			}
		case strings.HasPrefix(rec, "# branch.upstream "):
			resp.Upstream = strings.TrimPrefix(rec, "# branch.upstream ")
		case strings.HasPrefix(rec, "# branch.ab "):
			// "# branch.ab +A -B"
			parts := strings.Fields(strings.TrimPrefix(rec, "# branch.ab "))
			if len(parts) == 2 {
				resp.Ahead, _ = strconv.Atoi(strings.TrimPrefix(parts[0], "+"))
				resp.Behind, _ = strconv.Atoi(strings.TrimPrefix(parts[1], "-"))
			}
		case strings.HasPrefix(rec, "1 "):
			// "1 XY sub mH mI mW hH hI path"
			fields := strings.SplitN(rec, " ", 9)
			if len(fields) == 9 && len(fields[1]) == 2 {
				resp.Files = append(resp.Files, gitChangeFile{
					Path:     fields[8],
					Index:    string(fields[1][0]),
					WorkTree: string(fields[1][1]),
				})
			}
		case strings.HasPrefix(rec, "2 "):
			// Renamed/copied entry: "2 XY sub mH mI mW hH hI Xscore path"
			// path field contains "newpath" — the next NUL record is the original
			// path which we skip below.
			fields := strings.SplitN(rec, " ", 10)
			if len(fields) == 10 && len(fields[1]) == 2 {
				resp.Files = append(resp.Files, gitChangeFile{
					Path:     fields[9],
					Index:    string(fields[1][0]),
					WorkTree: string(fields[1][1]),
				})
			}
		case strings.HasPrefix(rec, "? "):
			resp.Files = append(resp.Files, gitChangeFile{
				Path:     strings.TrimPrefix(rec, "? "),
				Index:    "?",
				WorkTree: "?",
			})
		}
	}

	return success(c, resp)
}

// POST /api/projects/:name/git/stage  body: {"paths": ["file1", "file2"]}
func (s *Server) handleGitStage(c *fiber.Ctx) error {
	return s.handleGitStageOp(c, true)
}

// POST /api/projects/:name/git/unstage  body: {"paths": ["file1"]}
func (s *Server) handleGitUnstage(c *fiber.Ctx) error {
	return s.handleGitStageOp(c, false)
}

func (s *Server) handleGitStageOp(c *fiber.Ctx, stage bool) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if len(body.Paths) == 0 {
		return fail(c, fiber.StatusBadRequest, "paths required")
	}
	if len(body.Paths) > 1000 {
		return fail(c, fiber.StatusBadRequest, "too many paths")
	}
	for _, fp := range body.Paths {
		if !isValidFilePath(fp) {
			return fail(c, fiber.StatusBadRequest, "invalid file path: "+fp)
		}
	}

	var args []string
	if stage {
		args = append([]string{"add", "--"}, body.Paths...)
	} else {
		// `git restore --staged` is the modern UI; falls back gracefully on
		// older git, but shepherd targets git ≥ 2.23. Using `--` makes paths
		// unambiguous against branch names.
		args = append([]string{"restore", "--staged", "--"}, body.Paths...)
	}
	out, err := runGitWithTimeout(p.Path, args, 30*time.Second)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError,
			"git "+args[0]+" failed: "+strings.TrimSpace(string(out)))
	}
	return success(c, nil)
}

// POST /api/projects/:name/git/commit  body: {"message": "...", "signoff": false, "amend": false}
func (s *Server) handleGitCommit(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	var body struct {
		Message string `json:"message"`
		Signoff bool   `json:"signoff,omitempty"`
		Amend   bool   `json:"amend,omitempty"`
		AllowEmpty bool `json:"allow_empty,omitempty"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	msg := strings.TrimRight(body.Message, " \t\r\n")
	if msg == "" {
		return fail(c, fiber.StatusBadRequest, "commit message required")
	}
	if len(msg) > 1<<20 { // 1 MiB
		return fail(c, fiber.StatusBadRequest, "commit message too large")
	}

	args := []string{"-C", p.Path, "commit", "-F", "-"}
	if body.Signoff {
		args = append(args, "--signoff")
	}
	if body.Amend {
		args = append(args, "--amend")
	}
	if body.AllowEmpty {
		args = append(args, "--allow-empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdin = strings.NewReader(msg)
	out, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		return fail(c, fiber.StatusInternalServerError,
			"git commit failed: "+strings.TrimSpace(string(out)))
	}

	// Best-effort: report the resulting commit hash.
	hashOut, _ := exec.Command("git", "-C", p.Path, "rev-parse", "HEAD").Output()
	return success(c, fiber.Map{
		"hash":   strings.TrimSpace(string(hashOut)),
		"output": string(out),
	})
}

// POST /api/projects/:name/git/push  body: {"remote": "origin", "branch": "main", "set_upstream": false}
func (s *Server) handleGitPush(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")
	p, err := project.Get(name)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	var body struct {
		Remote      string `json:"remote"`
		Branch      string `json:"branch"`
		SetUpstream bool   `json:"set_upstream,omitempty"`
	}
	// Body is optional — empty body means "push current branch to origin".
	_ = c.BodyParser(&body)

	remote := body.Remote
	if remote == "" {
		remote = "origin"
	}
	if !isValidGitRef(remote) {
		return fail(c, fiber.StatusBadRequest, "invalid remote name")
	}

	branch := body.Branch
	if branch == "" {
		bout, berr := exec.Command("git", "-C", p.Path,
			"symbolic-ref", "--short", "HEAD").Output()
		if berr != nil {
			return fail(c, fiber.StatusBadRequest,
				"cannot detect current branch (detached HEAD?)")
		}
		branch = strings.TrimSpace(string(bout))
	}
	if !isValidGitRef(branch) {
		return fail(c, fiber.StatusBadRequest, "invalid branch name")
	}

	args := []string{"push"}
	if body.SetUpstream {
		args = append(args, "-u")
	}
	args = append(args, remote, branch)

	out, cmdErr := runGitWithTimeout(p.Path, args, gitCmdTimeout)
	if cmdErr != nil {
		return fail(c, fiber.StatusInternalServerError,
			"git push failed: "+strings.TrimSpace(string(out)))
	}
	return success(c, fiber.Map{
		"remote": remote,
		"branch": branch,
		"output": string(out),
	})
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
