package embedded

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxOutputBytes = 64 * 1024 // 64KB output cap for bash

// MCPToolDef is a tool definition from an external MCP source.
type MCPToolDef struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// MCPDispatcher dispatches tool calls to an external MCP server.
type MCPDispatcher func(name string, args map[string]interface{}) (string, error)

// ToolRegistry holds all tools available to the embedded agent loop.
type ToolRegistry struct {
	projectPath string
	sheepName   string
	nativeTools map[string]toolFunc
	mcpDefs     []MCPToolDef
	mcpDispatch MCPDispatcher
	// visionEnabled lets read_file surface image files as viewable images
	// instead of a "cannot read binary" notice. Set by the loop when the task
	// prompt carries attached files.
	visionEnabled bool
	// pendingImages collects images produced by read_file during the current
	// turn. The loop drains them after tool results and appends them as an
	// image_url user message. See DrainPendingImages.
	pendingImages []pendingImage
	// readImages tracks image file paths that have already been loaded into the
	// chat context. This prevents the model from calling read_file on the same
	// image repeatedly, which would cause an infinite loop.
	readImages map[string]bool
}

// pendingImage is an image loaded by read_file, awaiting injection into the
// chat history as a multimodal message part.
type pendingImage struct {
	name    string
	dataURL string
}

// SetVision enables or disables vision mode for read_file image handling.
func (tr *ToolRegistry) SetVision(enabled bool) {
	tr.visionEnabled = enabled
}

// MarkImageRead marks an image file path as already loaded into the context.
// Call this for images pre-attached to the initial prompt so the model doesn't
// try to read_file them again (which would cause an infinite loop).
func (tr *ToolRegistry) MarkImageRead(path string) {
	tr.readImages[path] = true
}

// MarkPreReadImages scans the initial user prompt for image file paths
// (e.g. from "[Attached files]" block) and marks them as already read.
func (tr *ToolRegistry) MarkPreReadImages(prompt string) {
	// Match common image extensions in file paths (uses shared imagePathRe).
	matches := imagePathRe.FindAllStringSubmatch(prompt, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			tr.readImages[m[1]] = true
		}
	}
}

// DrainPendingImages returns images collected since the last drain and clears
// the buffer. The loop calls this after appending tool results so the images
// can follow as a separate user message (OpenAI requires tool results to
// immediately follow the assistant's tool_calls).
func (tr *ToolRegistry) DrainPendingImages() []pendingImage {
	if len(tr.pendingImages) == 0 {
		return nil
	}
	imgs := tr.pendingImages
	tr.pendingImages = nil
	return imgs
}

// toolFunc receives the loop's context so task stop (ctx cancel) propagates
// into long-running tools (notably bash subprocesses).
type toolFunc func(ctx context.Context, args map[string]interface{}) (string, error)

// NewToolRegistry creates a tool registry with native coding tools and optional MCP tools.
func NewToolRegistry(projectPath, sheepName string, mcpDefs []MCPToolDef, mcpDispatch MCPDispatcher) *ToolRegistry {
	tr := &ToolRegistry{
		projectPath: projectPath,
		sheepName:   sheepName,
		mcpDefs:     mcpDefs,
		mcpDispatch: mcpDispatch,
		nativeTools: make(map[string]toolFunc),
		readImages:  make(map[string]bool),
	}
	tr.registerNativeTools()
	return tr
}

// registerNativeTools registers the core coding tools (read, write, edit, bash, grep, glob).
func (tr *ToolRegistry) registerNativeTools() {
	tr.nativeTools["read_file"] = tr.readfile
	tr.nativeTools["write_file"] = tr.writefile
	tr.nativeTools["edit_file"] = tr.editfile
	tr.nativeTools["bash"] = tr.execBash
	tr.nativeTools["grep"] = tr.execGrep
	tr.nativeTools["glob"] = tr.execGlob
}

// OpenAIToolDefs returns all tool definitions as OpenAI function-calling format.
func (tr *ToolRegistry) OpenAIToolDefs() []OpenAIToolDef {
	var defs []OpenAIToolDef

	// Native tools
	nativeDefs := []OpenAIToolDef{
		{
			Type: "function",
			Function: OpenAIFunction{
				Name:        "read_file",
				Description: "Read the contents of a file. For text files, returns the text. For image files (png/jpeg/gif/webp), when the task has attached images, returns the image for you to view directly — so call this on attached image paths to look at them. Other binary files (archives, executables) return a short notice instead. For large text files, use offset/limit parameters.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":   map[string]interface{}{"type": "string", "description": "Path to the file"},
						"offset": map[string]interface{}{"type": "number", "description": "Line number to start from (1-indexed)"},
						"limit":  map[string]interface{}{"type": "number", "description": "Maximum number of lines to read"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: OpenAIFunction{
				Name:        "write_file",
				Description: "Create or overwrite a file with the given content.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string", "description": "Path to the file"},
						"content": map[string]interface{}{"type": "string", "description": "Content to write"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: OpenAIFunction{
				Name:        "edit_file",
				Description: "Edit a file by replacing exact text. The old text must match uniquely in the file.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string", "description": "Path to the file"},
						"oldText": map[string]interface{}{"type": "string", "description": "Exact text to find and replace"},
						"newText": map[string]interface{}{"type": "string", "description": "Replacement text"},
					},
					"required": []string{"path", "oldText", "newText"},
				},
			},
		},
		{
			Type: "function",
			Function: OpenAIFunction{
				Name:        "bash",
				Description: "Execute a shell command in the project directory. Output is capped at 64KB.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{"type": "string", "description": "Shell command to execute"},
						"timeout": map[string]interface{}{"type": "number", "description": "Timeout in seconds (optional, default 120)"},
					},
					"required": []string{"command"},
				},
			},
		},
		{
			Type: "function",
			Function: OpenAIFunction{
				Name:        "grep",
				Description: "Search for a pattern in files using ripgrep. Falls back to Go regex if ripgrep is not available.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{"type": "string", "description": "Pattern to search for"},
						"glob":    map[string]interface{}{"type": "string", "description": "Glob pattern to filter files (optional)"},
					},
					"required": []string{"pattern"},
				},
			},
		},
		{
			Type: "function",
			Function: OpenAIFunction{
				Name:        "glob",
				Description: "Find files matching a glob pattern in the project directory.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{"type": "string", "description": "Glob pattern (e.g., **/*.go)"},
					},
					"required": []string{"pattern"},
				},
			},
		},
	}
	defs = append(defs, nativeDefs...)

	// MCP tools (provided externally via NewToolRegistry)
	for _, t := range tr.mcpDefs {
		defs = append(defs, OpenAIToolDef{
			Type: "function",
			Function: OpenAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	return defs
}

// Dispatch executes a tool call by name and arguments. The context is passed
// through to native tools so cancellation/timeout reaches subprocesses.
// WantsSheepName reports whether the named MCP tool declares a sheep_name
// property in its input schema. Only such tools (shepherd's own browser_*
// etc.) should have sheep_name injected; external MCP servers using strict
// unmarshaling reject unknown fields (task #6142).
func (tr *ToolRegistry) WantsSheepName(name string) bool {
	for _, def := range tr.mcpDefs {
		if def.Name != name {
			continue
		}
		props, ok := def.Parameters["properties"].(map[string]interface{})
		if !ok {
			return false
		}
		_, has := props["sheep_name"]
		return has
	}
	return false
}

func (tr *ToolRegistry) Dispatch(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	// Check native tools first
	if fn, ok := tr.nativeTools[name]; ok {
		return fn(ctx, args)
	}

	// Fall back to MCP tools
	if tr.mcpDispatch != nil {
		return tr.mcpDispatch(name, args)
	}

	return "", fmt.Errorf("unknown tool: %s", name)
}

// isBinary reports whether data looks like a non-text (binary) file. It samples
// the leading bytes: a NUL byte is a strong binary signal, and invalid UTF-8
// (beyond the sample boundary cutting a multi-byte rune) also marks it binary.
// Empty files are treated as text.
func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	const sampleSize = 8000
	sample := data
	if len(sample) > sampleSize {
		sample = sample[:sampleSize]
	}
	if bytes.IndexByte(sample, 0) != -1 {
		return true
	}
	// Trim a possibly-truncated trailing rune so a clean text file isn't
	// misjudged when the sample boundary splits a multi-byte UTF-8 sequence.
	if len(data) > sampleSize {
		for len(sample) > 0 && !utf8.RuneStart(sample[len(sample)-1]) {
			sample = sample[:len(sample)-1]
		}
	}
	return !utf8.Valid(sample)
}

// -- Native tool implementations --

func (tr *ToolRegistry) safePath(p string) (string, error) {
	// Prevent path traversal outside project directory.
	// NOTE: safePath is not a hard security boundary — it's a model mistake guard.
	// The bash tool can still escape via `cd` since cmd.Dir only sets the working
	// directory (tools.go:448), not a chroot jail.
	cleaned := filepath.Clean(p)
	if cleaned == "." || cleaned == "/" {
		cleaned = tr.projectPath
	}
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(tr.projectPath, cleaned)
	}
	// Ensure the path is within project directory.
	// Use rel == ".." || strings.HasPrefix(rel, "../") to avoid false positives
	// on legitimate filenames like "..foo" that happen to start with two dots.
	rel, err := filepath.Rel(tr.projectPath, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("path %q is outside project directory", p)
	}
	return cleaned, nil
}

func (tr *ToolRegistry) readfile(_ context.Context, args map[string]interface{}) (string, error) {
	pathStr, _ := args["path"].(string)
	if pathStr == "" {
		return "", fmt.Errorf("path is required")
	}
	path, err := tr.safePath(pathStr)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	// Guard against binary files (images, archives, executables, …). The embedded
	// provider's chat API only carries plain text, so returning raw binary bytes
	// as a tool result poisons the model's context and makes it spin out empty
	// responses ("empty response loop detected"). Detect binary content and return
	// a clear, descriptive message instead of the garbage bytes.
	if isBinary(data) {
		mime := http.DetectContentType(data)
		// In vision mode, surface image files as real images the model can view,
		// rather than refusing them. The bytes are buffered and the loop appends
		// them as an image_url user message after the tool results.
		if tr.visionEnabled && strings.HasPrefix(mime, "image/") {
			// Check if this image has already been loaded — prevent infinite loops
			// where the model keeps calling read_file on the same image.
			if tr.readImages[path] {
				return fmt.Sprintf(
					"[Image %s has already been loaded and is visible in the conversation context above. "+
						"Do NOT call read_file on it again. Please analyze the image you can already see and provide your response.]",
					filepath.Base(path),
				), nil
			}
			tr.readImages[path] = true
			dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
			tr.pendingImages = append(tr.pendingImages, pendingImage{
				name:    filepath.Base(path),
				dataURL: dataURL,
			})
			return fmt.Sprintf(
				"[Loaded image %s (%s, %d bytes). It is attached below as an image for you to view directly.]",
				filepath.Base(path), mime, len(data),
			), nil
		}
		return fmt.Sprintf(
			"[Cannot read %s as text: it is a binary file (%s, %d bytes). "+
				"The embedded provider cannot view image or binary contents. "+
				"Use the bash tool with utilities like `file`, `exiftool`, or `identify` to inspect its metadata if needed.]",
			filepath.Base(path), mime, len(data),
		), nil
	}

	content := string(data)

	// Handle offset/limit for large files
	if offsetVal, ok := args["offset"].(float64); ok && offsetVal > 0 {
		lines := strings.Split(content, "\n")
		offset := int(offsetVal)
		if offset > len(lines) {
			return "", fmt.Errorf("offset %d exceeds file length %d", offset, len(lines))
		}
		lines = lines[offset-1:]

		if limitVal, ok := args["limit"].(float64); ok && limitVal > 0 {
			limit := int(limitVal)
			if limit < len(lines) {
				lines = lines[:limit]
			}
		}
		content = strings.Join(lines, "\n")
	}

	return content, nil
}

func (tr *ToolRegistry) writefile(_ context.Context, args map[string]interface{}) (string, error) {
	pathStr, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if pathStr == "" {
		return "", fmt.Errorf("path is required")
	}
	path, err := tr.safePath(pathStr)
	if err != nil {
		return "", err
	}

	// Create parent directories if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
}

func (tr *ToolRegistry) editfile(_ context.Context, args map[string]interface{}) (string, error) {
	pathStr, _ := args["path"].(string)
	oldText, _ := args["oldText"].(string)
	newText, _ := args["newText"].(string)
	if pathStr == "" || oldText == "" {
		return "", fmt.Errorf("path and oldText are required")
	}
	path, err := tr.safePath(pathStr)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	content := string(data)

	// Check uniqueness
	count := strings.Count(content, oldText)
	if count == 0 {
		return "", fmt.Errorf("text not found in %s", path)
	}
	if count > 1 {
		return "", fmt.Errorf("text appears %d times in %s, must be unique", count, path)
	}

	newContent := strings.Replace(content, oldText, newText, 1)
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return fmt.Sprintf("Edited %s (replaced %d bytes with %d bytes)", path, len(oldText), len(newText)), nil
}

func (tr *ToolRegistry) execBash(ctx context.Context, args map[string]interface{}) (string, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := 120 // default 2 minutes
	if timeoutVal, ok := args["timeout"].(float64); ok && timeoutVal > 0 {
		timeout = int(timeoutVal)
	}

	// Derive a child context from the parent (loop ctx) so that task stop (ctx
	// cancel) propagates into the subprocess. The timeout is a local cap on top
	// of the parent's deadline — whichever comes first kills the process.
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "bash", "-c", command)
	cmd.Dir = tr.projectPath

	// Create a new process group so that on cancel/timeout we can kill the
	// entire process tree (bash + all children) rather than just the bash shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		output := stderr.String()
		if timeoutCtx.Err() != nil {
			output = fmt.Sprintf("command timed out after %ds", timeout)
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			output = fmt.Sprintf("exit %d: %s", exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
		} else {
			output = fmt.Sprintf("error: %s", err)
		}
		// Still return stdout if available
		if stdout.Len() > 0 {
			output = stdout.String() + "\n" + output
		}

		// Kill the entire process group on any error (especially ctx cancel or
		// timeout). exec.CommandContext kills the bash process itself, but child
		// processes may survive as orphans. Killing the group ensures cleanup.
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			cmd.Process.Wait() // avoid zombie
		}

		return tr.capOutput(output), nil
	}

	return tr.capOutput(stdout.String()), nil
}

func (tr *ToolRegistry) execGrep(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	globPattern, _ := args["glob"].(string)

	// Try ripgrep first
	cmd := exec.CommandContext(ctx, "rg", "--color=never", "-n", "--", pattern)
	if globPattern != "" {
		cmd.Args = append(cmd.Args, "-g", globPattern)
	}
	cmd.Dir = tr.projectPath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// ripgrep not found or error, fall back to Go regex
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 127 {
			// rg not found
		} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// rg found no matches
			return "No matches found", nil
		}
	} else {
		return tr.capOutput(stdout.String()), nil
	}

	// Fallback: Go regex recursive search using filepath.WalkDir.
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
	}

	var results []string
	fileFilter, err := regexp.Compile("^" + strings.ReplaceAll(filepath.Clean(globPattern), "**/", ".*") + "$")
	if globPattern == "" || err != nil {
		// No glob filter or invalid pattern — match all files
		fileFilter = regexp.MustCompile(".*")
	}

	err = filepath.WalkDir(tr.projectPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors, continue walking
		}
		if d.IsDir() {
			// Skip hidden directories and common non-source dirs
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !fileFilter.MatchString(filepath.Base(path)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(tr.projectPath, path)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				results = append(results, fmt.Sprintf("%s:%d:%s", relPath, i+1, line))
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk error: %w", err)
	}

	if len(results) == 0 {
		return "No matches found", nil
	}
	return tr.capOutput(strings.Join(results, "\n")), nil
}

func (tr *ToolRegistry) execGlob(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	// Use filepath.WalkDir for recursive matching (handles ** correctly).
	var matches []string
	err := filepath.WalkDir(tr.projectPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors, continue walking
		}
		if d.IsDir() {
			// Skip hidden directories (including .git, .gitignore files in dirs)
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}

		// Get path relative to project root for matching
		rel, err := filepath.Rel(tr.projectPath, path)
		if err != nil {
			return nil
		}

		if matchGlob(rel, pattern) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk error: %w", err)
	}

	if len(matches) == 0 {
		return "No files found", nil
	}
	return strings.Join(matches, "\n"), nil
}

// matchGlob matches a relative file path against a glob pattern that may contain **
// for recursive directory matching. For example: "**/*.go" matches "foo/bar.go",
// "src/**/*.go" matches "src/main.go" and "src/internal/helper.go".
func matchGlob(path, pattern string) bool {
	if !strings.Contains(pattern, "**") {
		// Simple glob — use filepath.Match directly
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	// Convert glob pattern with ** to regex:
	//   **/  →  (.*\/)?       (zero or more directory levels)
	//   **   →  .*            (anything including /)
	//   *    →  [^/]*         (single level wildcard)
	// Other special chars are regex-escaped.

	// First, escape all regex special chars except * and /
	reStr := "^"
	i := 0
	for i < len(pattern) {
		ch := pattern[i]

		// Handle ** (double-star)
		if ch == '*' && i+1 < len(pattern) && pattern[i+1] == '*' {
			i += 2
			// If followed by /, it matches zero or more dir levels
			if i < len(pattern) && pattern[i] == '/' {
				reStr += ".*" // **/ matches any depth including zero (handled by optional groups)
				i++
				continue
			} else if i < len(pattern) && pattern[i] == '\\' {
				reStr += ".*"
				i++
				continue
			} else {
				// Trailing ** (e.g., "src/**") — match everything remaining
				reStr += ".*"
				continue
			}
		}

		// Handle single * (matches within a single directory level)
		if ch == '*' {
			reStr += "[^/]*"
			i++
			continue
		}

		// Escape regex special characters
		switch ch {
		case '.', '+', '?', '[', ']', '(', ')', '{', '}', '|', '^', '$', '\\':
			reStr += "\\" + string(ch)
		default:
			reStr += string(ch)
		}
		i++
	}
	reStr += "$"

	re, err := regexp.Compile(reStr)
	if err != nil {
		// Fallback to filepath.Match on compile error
		matched, _ := filepath.Match(pattern, path)
		return matched
	}

	return re.MatchString(path)
}

func (tr *ToolRegistry) capOutput(s string) string {
	if len(s) > maxOutputBytes {
		return s[:maxOutputBytes] + fmt.Sprintf("\n\n... [output truncated, %d total bytes]", len(s))
	}
	return s
}
