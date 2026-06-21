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
	"strconv"
	"strings"
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
	// lastReadPath / lastReadEndLine remember the most recent read_file call so
	// a follow-up call to the same path WITHOUT an explicit offset can be
	// auto-advanced to the next page. Weak local models routinely emit the
	// offset only in their thinking ("offset 142") but omit it from the tool-
	// call arguments JSON — without this, they'd loop on page 1 until the stuck
	// guard kills the task (task #6505).
	lastReadPath    string
	lastReadEndLine int
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
				Description: "Read the contents of a file. For text files, returns the text. For image files (png/jpeg/gif/webp), when the task has attached images, returns the image for you to view directly — so call this on attached image paths to look at them. Other binary files (archives, executables) return a short notice instead. Large text files are returned one page at a time, ending with a footer like '[File has N lines. Showing lines A-B. Call read_file with offset=C to read more.]' — call read_file again with that exact offset to continue paging through the file.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":   map[string]interface{}{"type": "string", "description": "Path to the file"},
						"offset": map[string]interface{}{"type": "number", "description": "Line number to start from (1-indexed). Defaults to 1. Use the offset named in a previous page's footer to read the next page."},
						"limit":  map[string]interface{}{"type": "number", "description": "Maximum number of lines to read in this call. Defaults to a bounded page; output is also capped by total size."},
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

// defaultReadFileLines is the line window read_file returns when it is called
// without an explicit limit. Mirrors a Read-style default so large files are
// paged one window at a time instead of dumped in full — a full dump just gets
// silently chopped by truncateToolResult, hiding the file's tail (task #6309).
const defaultReadFileLines = 200

// maxReadFileChars caps the characters a single read_file call returns,
// INCLUDING when an explicit limit is given. It is kept safely below
// maxToolResultChars (loop.go) so the paging footer read_file appends always
// survives history truncation. (A 200-line window of normal source — ~40 chars a
// line — already approaches 8 000 chars, so a line cap alone is not enough; the
// char cap is the real guarantee.)
const maxReadFileChars = maxToolResultChars - 2000

// argInt extracts an integer-valued tool argument, tolerating the several ways
// local models encode numbers. A standard JSON number arrives as float64, but
// weaker local models routinely emit numeric arguments as quoted strings
// ("156") — and a plain `args[key].(float64)` assertion silently fails on those,
// leaving the argument unread.
//
// That silent failure was the task #6410 stuck loop: the model paged read_file
// with offset="156" (a string), the offset was ignored, read_file returned page 1
// again with a footer naming the same offset, and the model re-issued the byte-
// for-byte identical call until the repeated-tool-call guard killed the task.
// Returns (value, true) only when a number was successfully parsed.
func argInt(args map[string]interface{}, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		if n, err := strconv.Atoi(s); err == nil {
			return n, true
		}
		// Tolerate "156.0" style values too.
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f), true
		}
	}
	return 0, false
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
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	// Resolve the starting line (1-indexed). Default to the top of the file.
	start := 1
	offsetGiven := false
	if offsetVal, ok := argInt(args, "offset"); ok && offsetVal > 0 {
		start = offsetVal
		offsetGiven = true
	}

	// Auto-page: weak local models routinely emit the offset only in their
	// thinking ("offset 142") but omit it from the tool-call arguments JSON.
	// Without this, a follow-up read_file on the same path returns page 1 again,
	// the tool-call signature stays identical, and the model loops until the
	// stuck guard kills the task (task #6505). When the same path is read again
	// with no explicit offset, advance past the last line we showed so the call
	// makes guaranteed forward progress.
	autoAdvanced := false
	if !offsetGiven && tr.lastReadPath == path && tr.lastReadEndLine > 0 {
		if tr.lastReadEndLine >= totalLines {
			// The whole file has already been shown. Returning page 1 again
			// would let a spinning model cycle forever (every page changes the
			// signature), so return a stable message instead: it tells the model
			// it already has the file and — being byte-identical turn after turn
			// — lets the stuck guard catch genuine spinning. The offset=1 hatch
			// still allows a real re-read (e.g. after the earlier read was
			// trimmed from context). Deliberately leaves lastReadEndLine intact.
			return fmt.Sprintf(
				"[You have already read this entire file (%d lines); there is nothing after "+
					"line %d. Proceed with what you've read. To re-read from the top, call "+
					"read_file with \"offset\": 1.]",
				totalLines, tr.lastReadEndLine), nil
		}
		start = tr.lastReadEndLine + 1
		autoAdvanced = true
	}

	if start > totalLines {
		return "", fmt.Errorf("offset %d exceeds file length %d", start, totalLines)
	}

	// Resolve the line window. Without an explicit limit we apply a default
	// window so a large file is paged rather than dumped — a dump just gets
	// chopped by truncateToolResult and the model never sees the tail (#6309).
	limit := defaultReadFileLines
	if limitVal, ok := argInt(args, "limit"); ok && limitVal > 0 {
		limit = limitVal
	}

	window := lines[start-1:]
	if limit < len(window) {
		window = window[:limit]
	}
	shown := strings.Join(window, "\n")

	// endLine is the last line number (1-indexed) actually included in `shown`.
	endLine := start + len(window) - 1

	// Character cap. Even a small line window can blow past the history budget
	// (e.g. minified files with very long lines), and the footer below must
	// survive truncateToolResult, so cap by runes here.
	if runes := []rune(shown); len(runes) > maxReadFileChars {
		shown = string(runes[:maxReadFileChars])
		completeLines := strings.Count(shown, "\n")
		if completeLines == 0 {
			// A single line is longer than the budget. Advance past it so the
			// next read has a different signature (no deadlock); its tail is
			// unread — the model can inspect it with the bash tool.
			endLine = start
			shown += fmt.Sprintf(
				"\n...[line %d is longer than %d chars and was truncated here; "+
					"use the bash tool (e.g. sed/cut) to read the rest of this line]",
				start, maxReadFileChars)
		} else {
			// The last shown line is partial; have the next page re-read it.
			endLine = start + completeLines - 1
		}
	}

	// Append a paging footer whenever more of the file remains, naming the exact
	// next offset. This both informs the model and — crucially — changes the
	// tool-call signature on the follow-up read, so the stuck-loop guard
	// (maxRepeatedToolTurns) never trips on legitimate paging (task #6309).
	if endLine < totalLines {
		shown += fmt.Sprintf(
			"\n\n[File has %d lines. Showing lines %d-%d. "+
				"Call read_file with offset=%d to read more.]",
			totalLines, start, endLine, endLine+1)
	}

	// Remember where this read ended so a follow-up call to the same path with
	// no explicit offset auto-advances (see the auto-page block above).
	tr.lastReadPath = path
	tr.lastReadEndLine = endLine

	if autoAdvanced {
		shown = fmt.Sprintf(
			"[⚠ Auto-paged: your previous read_file on this file omitted the \"offset\" "+
				"argument, so this returned the NEXT page (lines %d-%d of %d) instead of "+
				"repeating the same page. To read a specific page include \"offset\": N; "+
				"to read from the top use \"offset\": 1.]\n\n",
			start, endLine, totalLines) + shown
	}

	return shown, nil
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
	if timeoutVal, ok := argInt(args, "timeout"); ok && timeoutVal > 0 {
		timeout = timeoutVal
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
	setupProcessGroup(cmd)

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
		killProcessGroup(cmd)

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

	// Build ripgrep args. Glob flags MUST come before the "--" terminator,
	// otherwise ripgrep treats them as positional search paths rather than
	// flags. Exclude build/vendor directories by default so the model never
	// gets compiled artifacts (e.g. ML model assets) injected into context.
	rgArgs := []string{"--color=never", "-n"}
	for _, ex := range defaultGrepExcludes {
		rgArgs = append(rgArgs, "-g", ex)
	}
	if globPattern != "" {
		rgArgs = append(rgArgs, "-g", globPattern)
	}
	rgArgs = append(rgArgs, "--", pattern)

	// Try ripgrep first
	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
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
		return tr.capOutput(filterBinaryLines(stdout.String())), nil
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
			if isExcludedGrepDir(d.Name()) {
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
		// Skip binary files so non-text bytes are never injected into context.
		if isBinary(data) {
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
	return tr.capOutput(filterBinaryLines(strings.Join(results, "\n"))), nil
}

// defaultGrepExcludes are glob patterns excluded from grep by default. These are
// build/vendor directories that typically hold large or compiled artifacts which
// add noise (and, for compiled assets, raw binary bytes) when injected into a
// small local model's context.
var defaultGrepExcludes = []string{
	"!**/build/**",
	"!**/node_modules/**",
	"!**/vendor/**",
	"!**/dist/**",
	"!**/target/**",
	"!**/.git/**",
}

// isExcludedGrepDir reports whether a directory name should be skipped during the
// fallback WalkDir grep. Mirrors defaultGrepExcludes for the no-ripgrep path.
func isExcludedGrepDir(name string) bool {
	switch name {
	case "build", "node_modules", "vendor", "dist", "target":
		return true
	}
	return false
}

// filterBinaryLines drops lines that look like binary/non-text data from grep
// output. ripgrep can match printable byte runs inside otherwise-binary files
// (e.g. protobuf model assets), and feeding those bytes to a small local model
// destabilizes it. We filter at the line level so legitimate matches in mixed
// files survive while the binary noise is removed.
func filterBinaryLines(output string) string {
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		if isBinaryLine(line) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	result := strings.Join(kept, "\n")
	if dropped > 0 {
		if result != "" {
			result += "\n"
		}
		result += fmt.Sprintf("[%d binary/non-text line(s) omitted]", dropped)
	}
	return result
}

// isBinaryLine reports whether a single line of grep output appears to be binary
// rather than source text: it contains a NUL byte, is not valid UTF-8, or has a
// high ratio of control bytes (tab excluded).
func isBinaryLine(line string) bool {
	if line == "" {
		return false
	}
	if strings.IndexByte(line, 0) != -1 {
		return true
	}
	if !utf8.ValidString(line) {
		return true
	}
	ctrl := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c < 0x20 && c != '\t' && c != '\r' {
			ctrl++
		}
	}
	return ctrl*100/len(line) > 10
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

// capOutput bounds a tool's raw output to maxOutputBytes before it is returned.
// This is the byte budget for the live stream/preview; the history copy is cut
// again — and made actionable — by truncateToolResult (loop.go). The trim lands
// on a rune boundary so a multi-byte character is never split into a replacement
// character, and the notice names a recovery path rather than dead-ending.
func (tr *ToolRegistry) capOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	cut := maxOutputBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf(
		"\n\n... [output truncated at %d of %d bytes — narrow the command's output "+
			"(head/tail/grep) or redirect it to a file and read it with read_file]", cut, len(s))
}
