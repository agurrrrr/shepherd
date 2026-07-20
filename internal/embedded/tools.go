package embedded

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

// MCPImage is an image returned by an MCP tool call (e.g. a screenshot from
// mobile_take_screenshot or a browser screenshot). The embedded loop surfaces it
// to a vision model as an image_url message part, mirroring how read_file
// handles image files on disk (task #6684).
type MCPImage struct {
	MIMEType string // e.g. "image/png"
	Data     string // base64-encoded image bytes (no "data:" prefix)
}

// MCPDispatcher dispatches a tool call to an external MCP server, returning the
// text result and any image content blocks the tool produced.
type MCPDispatcher func(name string, args map[string]interface{}) (string, []MCPImage, error)

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
	// mu protects mutable session state that pure-read tools may touch when
	// a parallel read batch runs (pendingImages, readImages, lastRead*).
	// Write/side-effect tools stay sequential, so they only contend with
	// concurrent readers in the all-read parallel path.
	mu sync.Mutex
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
	// parallelReadDepth is >0 while a pure-read parallel batch is in flight.
	// Auto-paging is disabled in that window so concurrent same-path reads
	// cannot race each other into "already read entire file" (Phase 3-3).
	parallelReadDepth int

	// subagentSpawner enables the spawn_subagents tool. When nil, the tool
	// is not registered (depth 1 enforcement for MAGI proposers and
	// sub-agents themselves).
	subagentSpawner SubagentSpawner

	// subagentEndpointHint is a short list of available endpoint IDs injected
	// into the spawn_subagents tool schema so models do not guess systemd
	// service names or ports. Empty when unset (description falls back to
	// generic text). Set via SetSubagentEndpointHint before OpenAIToolDefs.
	subagentEndpointHint string

	// spawnCount tracks how many times spawn_subagents has been called
	// in the current task. Capped at maxSpawnsPerTask (3) to prevent
	// runaway cost (#7463 review: Important #4).
	spawnCount int

	// todoEnabled gates the opt-in todo_write tool (Phase 3-2 / task #7547).
	// Default false: tool absent from OpenAIToolDefs and Dispatch unknown.
	// When true, Todo holds session plan state for the turn-end incomplete gate.
	todoEnabled bool
	todo        TodoState
}

// maxSpawnsPerTask is the per-task limit on spawn_subagents calls to prevent
// runaway cost (#7463 review: README guard).
const maxSpawnsPerTask = 3

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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.readImages[path] = true
}

// MarkPreReadImages scans the initial user prompt for image file paths
// (e.g. from "[Attached files]" block) and marks them as already read.
func (tr *ToolRegistry) MarkPreReadImages(prompt string) {
	// Match common image extensions in file paths (uses shared imagePathRe).
	matches := imagePathRe.FindAllStringSubmatch(prompt, -1)
	tr.mu.Lock()
	defer tr.mu.Unlock()
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.pendingImages) == 0 {
		return nil
	}
	imgs := tr.pendingImages
	tr.pendingImages = nil
	return imgs
}

// beginParallelReads / endParallelReads bracket a pure-read parallel batch so
// read_file auto-paging (lastRead*) stays off for the duration — concurrent
// same-path reads would otherwise race into false "already complete" replies.
func (tr *ToolRegistry) beginParallelReads() {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	tr.parallelReadDepth++
	tr.mu.Unlock()
}

func (tr *ToolRegistry) endParallelReads() {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	if tr.parallelReadDepth > 0 {
		tr.parallelReadDepth--
	}
	tr.mu.Unlock()
}

// SetSubagentSpawner registers the sub-agent spawning callback. When set,
// the spawn_subagents tool becomes available in OpenAIToolDefs and Dispatch.
func (tr *ToolRegistry) SetSubagentSpawner(fn SubagentSpawner) {
	tr.subagentSpawner = fn
}

// SetSubagentEndpointHint sets the comma-separated available endpoint IDs
// shown in the spawn_subagents endpoint_id parameter description. Call before
// OpenAIToolDefs so the model sees exact ids (not labels/ports).
func (tr *ToolRegistry) SetSubagentEndpointHint(hint string) {
	tr.subagentEndpointHint = hint
}

// HasSubagentSpawner returns whether this registry can spawn sub-agents.
func (tr *ToolRegistry) HasSubagentSpawner() bool {
	return tr.subagentSpawner != nil
}

// endpointIDParamDescription builds the spawn_subagents endpoint_id field
// description. When a hint was set, lists exact available IDs so models do
// not invent systemd unit names, labels, or port numbers.
func (tr *ToolRegistry) endpointIDParamDescription() string {
	base := "Embedded endpoint id from ~/.shepherd/embedded.yaml (empty = parent's endpoint). " +
		"Use the exact id field, not label, model name, systemd unit, or port."
	if tr == nil || strings.TrimSpace(tr.subagentEndpointHint) == "" {
		return base
	}
	return base + " Available ids: " + tr.subagentEndpointHint
}

// CanSpawn returns false if the per-task spawn limit has been reached.
func (tr *ToolRegistry) CanSpawn() bool {
	return tr.spawnCount < maxSpawnsPerTask
}

// EnableTodo turns on the opt-in todo_write tool and session TodoState.
// Must be called before OpenAIToolDefs / Dispatch when embedded_todo_gate is on.
// Default is off so existing false-completion guards keep sole responsibility.
func (tr *ToolRegistry) EnableTodo() {
	if tr == nil {
		return
	}
	tr.todoEnabled = true
	tr.nativeTools["todo_write"] = tr.execTodoWrite
}

// TodoEnabled reports whether todo_write is active for this registry.
func (tr *ToolRegistry) TodoEnabled() bool {
	return tr != nil && tr.todoEnabled
}

// Todo returns a pointer to the session TodoState (nil-safe empty when disabled).
func (tr *ToolRegistry) Todo() *TodoState {
	if tr == nil {
		return nil
	}
	return &tr.todo
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
				Name: "read_file",
				Description: "Read the contents of a file. For text files, each line is prefixed with its 1-based line number as `N→` " +
					"(e.g. `42→func main() {`). That `N→` prefix is display-only — NOT part of the file. " +
					"When calling edit_file, put only the text AFTER `→` into oldText. " +
					"For image files (png/jpeg/gif/webp), when the task has attached images, returns the image for you to view directly — so call this on attached image paths to look at them. " +
					"Other binary files (archives, executables) return a short notice instead. " +
					"Large text files are returned one page at a time, ending with a footer like " +
					"'[File has N lines. Showing lines A-B. Call read_file with offset=C to read more.]' — " +
					"call read_file again with that exact offset to continue paging through the file.",
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
				Name: "write_file",
				Description: "Create or overwrite a file with the given content. " +
					"For large files, prefer a short skeleton via write_file then fill with multiple edit_file calls — " +
					"a single very large content payload can hit max_tokens and be refused mid-stream.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string", "description": "Path to the file"},
						"content": map[string]interface{}{"type": "string", "description": "Full file content to write (keep moderately sized; split large files)"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: OpenAIFunction{
				Name: "edit_file",
				Description: "Edit a file by replacing exact text. The old text must match uniquely in the file " +
					"unless replace_all is true. Aliases old_string/new_string are accepted. " +
					"Falls back to Unicode confusable-normalized matching (smart quotes, dashes, ellipsis, NBSP) if exact match fails. " +
					"IMPORTANT: read_file shows lines as `N→content` — the `N→` prefix is NOT file content. " +
					"oldText must contain only the real file bytes after `→` (never include the line-number prefix).",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":        map[string]interface{}{"type": "string", "description": "Path to the file"},
						"oldText":     map[string]interface{}{"type": "string", "description": "Exact text to find and replace (alias: old_string). Do not include read_file line prefixes (`N→`)."},
						"newText":     map[string]interface{}{"type": "string", "description": "Replacement text (alias: new_string)"},
						"old_string":  map[string]interface{}{"type": "string", "description": "Alias for oldText (Claude/grok compatibility)"},
						"new_string":  map[string]interface{}{"type": "string", "description": "Alias for newText (Claude/grok compatibility)"},
						"replace_all": map[string]interface{}{"type": "boolean", "description": "If true, replace every occurrence; if false (default), require a unique match"},
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

	// spawn_subagents tool — only available when a SubagentSpawner is registered
	// (parent task with sub-agent support). Sub-agents themselves and MAGI
	// proposers do not set the spawner, so the tool is invisible to them
	// (depth 1 enforcement).
	if tr.subagentSpawner != nil {
		defs = append(defs, OpenAIToolDef{
			Type: "function",
			Function: OpenAIFunction{
				Name: "spawn_subagents",
				Description: "Spawn read-only sub-agents that run in parallel. Each sub-agent gets its own " +
					"context window and can use read-only tools (read_file, grep, glob, browser, etc.). " +
					"Write tools are NOT available to sub-agents. Results are returned as a combined summary. " +
					"Use this for parallel research, multi-model code review, or exploring multiple approaches.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"subagents": map[string]interface{}{
							"type":        "array",
							"description": "List of sub-agents to spawn (max 4)",
							"maxItems":    4,
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"name": map[string]interface{}{
										"type":        "string",
										"description": "A short name for this sub-agent (used in output prefix)",
									},
									"prompt": map[string]interface{}{
										"type":        "string",
										"description": "The task/prompt for this sub-agent",
									},
									"endpoint_id": map[string]interface{}{
										"type":        "string",
										"description": tr.endpointIDParamDescription(),
									},
									"max_iterations": map[string]interface{}{
										"type":        "integer",
										"description": "Max agent loop iterations for this sub-agent (default 15)",
									},
								},
								"required": []string{"name", "prompt"},
							},
						},
					},
					"required": []string{"subagents"},
				},
			},
		})
	}

	// todo_write — opt-in structured plan tool (Phase 3-2). Absent when
	// embedded_todo_gate is off so existing false-completion guards are sole path.
	if tr.todoEnabled {
		defs = append(defs, OpenAIToolDef{
			Type: "function",
			Function: OpenAIFunction{
				Name: "todo_write",
				Description: "Create and manage a structured task list for multi-step work. " +
					"Use for any task with 3+ steps. Skip for trivial single-step work. " +
					"When merge is true (default), updates by id; send just id+status to flip status. " +
					"When merge is false, the list fully replaces prior state. " +
					"Statuses: pending, in_progress, completed, cancelled.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"merge": map[string]interface{}{
							"type":        "boolean",
							"description": "Optional. When true (default), merge by id. When false, replace the full list.",
						},
						"todos": map[string]interface{}{
							"type":        "array",
							"description": "Todo items to write. Alias: steps.",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"id": map[string]interface{}{
										"type":        "string",
										"description": "Unique id (auto 1,2,… if omitted)",
									},
									"content": map[string]interface{}{
										"type":        "string",
										"description": "Item text (aliases: text, description)",
									},
									"text": map[string]interface{}{
										"type":        "string",
										"description": "Alias for content",
									},
									"status": map[string]interface{}{
										"type":        "string",
										"description": "pending | in_progress | completed | cancelled",
									},
								},
							},
						},
						"steps": map[string]interface{}{
							"type":        "array",
							"description": "Alias for todos",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"id":      map[string]interface{}{"type": "string"},
									"text":    map[string]interface{}{"type": "string"},
									"content": map[string]interface{}{"type": "string"},
									"status":  map[string]interface{}{"type": "string"},
								},
							},
						},
					},
					"required": []string{"todos"},
				},
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

func (tr *ToolRegistry) Dispatch(ctx context.Context, name string, args map[string]interface{}) (result string, err error) {
	// Recover from panics in native tool implementations or MCP dispatch so
	// a single bad tool call cannot crash the entire shepherd process.
	defer func() {
		if r := recover(); r != nil {
			result = ""
			err = fmt.Errorf("tool %s panicked: %v", name, r)
		}
	}()

	// Check native tools first
	if fn, ok := tr.nativeTools[name]; ok {
		return fn(ctx, args)
	}

	// Fall back to MCP tools
	if tr.mcpDispatch != nil {
		mcpResult, images, mcpErr := tr.mcpDispatch(name, args)
		if mcpErr != nil {
			return mcpResult, mcpErr
		}
		return tr.bufferMCPImages(name, mcpResult, images), nil
	}

	return "", fmt.Errorf("unknown tool: %s", name)
}

// bufferMCPImages queues any images an MCP tool returned for injection into the
// chat as an image_url message (drained later by appendPendingImages), mirroring
// the read_file vision path. It is a no-op when the tool returned no images.
//
// When vision is enabled the images are buffered and a short note is appended to
// the (often empty) text result so the model knows a picture is attached below.
// When vision is disabled the images can't be shown, so it returns a plain note
// instead of silently dropping them — otherwise mobile_take_screenshot looks
// like it returned nothing and the model works blind (task #6684).
func (tr *ToolRegistry) bufferMCPImages(toolName, text string, images []MCPImage) string {
	if len(images) == 0 {
		return text
	}
	if !tr.visionEnabled {
		note := fmt.Sprintf(
			"[%s returned %d image(s), but vision is not enabled for this endpoint so they cannot be viewed.]",
			toolName, len(images),
		)
		if strings.TrimSpace(text) == "" {
			return note
		}
		return text + "\n" + note
	}
	for i, img := range images {
		// Optimize (resize + re-encode) large images before they enter the
		// chat context. A single 1080p PNG screenshot is ~2–3 MB as base64
		// (3 000+ tokens); repeated mobile_take_screenshot calls would
		// quickly exhaust the context window. optimizeMCPImage resizes to
		// maxImageDim and re-encodes as JPEG quality 85, cutting payload by
		// up to 80 % with negligible quality loss for screenshots (task #6688).
		dataURL := optimizeMCPImage(img)
		tr.mu.Lock()
		tr.pendingImages = append(tr.pendingImages, pendingImage{
			name:    fmt.Sprintf("%s#%d", toolName, i+1),
			dataURL: dataURL,
		})
		tr.mu.Unlock()
	}
	note := fmt.Sprintf("[%s returned %d image(s), attached below for you to view directly.]", toolName, len(images))
	if strings.TrimSpace(text) == "" {
		return note
	}
	return text + "\n" + note
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
	// Trim the sample so it ends on a complete UTF-8 rune boundary.
	// The old code only trimmed trailing continuation bytes (10xxxxxx) via
	// utf8.RuneStart, but that left a dangling leading byte (e.g. the first
	// byte of a 3-byte Korean character) whose expected continuation bytes
	// were truncated. utf8.Valid() then returned false, causing every
	// non-ASCII text file > 8 KB to be misclassified as binary (#6624).
	//
	// Fix: use utf8.DecodeLastRune to detect whether the trailing bytes form
	// a complete rune. If not (RuneError + size 1), drop the last byte and
	// retry. This correctly trims both continuation bytes AND the leading
	// byte of a truncated multi-byte sequence.
	if len(data) > sampleSize {
		for len(sample) > 0 {
			r, size := utf8.DecodeLastRune(sample)
			if size == 0 {
				break // empty sample — shouldn't happen but guard anyway
			}
			if r == utf8.RuneError && size == 1 {
				// Last byte doesn't form a complete rune — drop it.
				sample = sample[:len(sample)-1]
				continue
			}
			break // Complete rune at the tail.
		}
	}
	// If trimming consumed everything, the sample was all invalid bytes
	// — definitely binary.
	if len(sample) == 0 && len(data) > 0 {
		return true
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
			// Locked: parallel read batches may load images concurrently.
			tr.mu.Lock()
			already := tr.readImages[path]
			if !already {
				tr.readImages[path] = true
			}
			tr.mu.Unlock()
			if already {
				return fmt.Sprintf(
					"[Image %s has already been loaded and is visible in the conversation context above. "+
						"Do NOT call read_file on it again. Please analyze the image you can already see and provide your response.]",
					filepath.Base(path),
				), nil
			}
			// Optimize large images before injecting into context: a raw phone
			// screenshot can be 2–3 MB as base64, consuming thousands of tokens.
			// optimizeImageFile resizes and re-encodes to keep payload small
			// while preserving visual quality (task #6688).
			dataURL := optimizeImageFile(data, mime)
			tr.mu.Lock()
			tr.pendingImages = append(tr.pendingImages, pendingImage{
				name:    filepath.Base(path),
				dataURL: dataURL,
			})
			tr.mu.Unlock()
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
	// lastRead* is protected by mu for parallel read batches. Auto-page is
	// skipped entirely while parallelReadDepth > 0 (see beginParallelReads).
	autoAdvanced := false
	if !offsetGiven {
		tr.mu.Lock()
		inParallel := tr.parallelReadDepth > 0
		samePath := !inParallel && tr.lastReadPath == path && tr.lastReadEndLine > 0
		lastEnd := tr.lastReadEndLine
		tr.mu.Unlock()
		if samePath {
			if lastEnd >= totalLines {
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
					totalLines, lastEnd), nil
			}
			start = lastEnd + 1
			autoAdvanced = true
		}
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
	// Prefix every body line with its real 1-based file line number ("N→").
	// Display-only — never written back by edit_file/write_file. Paging offsets
	// stay tied to these absolute line numbers (task #7550 / Phase 1-2).
	shown := formatReadFilePage(window, start)

	// endLine is the last line number (1-indexed) actually included in `shown`.
	endLine := start + len(window) - 1

	// Character cap. Even a small line window can blow past the history budget
	// (e.g. minified files with very long lines), and the footer below must
	// survive truncateToolResult, so cap by runes here. Prefixes are included
	// in the budget so the model never sees a partial page past the history cut.
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
	tr.mu.Lock()
	tr.lastReadPath = path
	tr.lastReadEndLine = endLine
	tr.mu.Unlock()

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

// formatReadFilePage renders a page of file lines with 1-based line-number
// prefixes in grok style ("123→content"). start is the 1-based line number of
// lines[0]. Prefixes are display-only metadata for the model.
func formatReadFilePage(lines []string, start int) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	// Rough size: content + ~6 runes per line for "N→".
	b.Grow(len(lines) * 8)
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d→%s", start+i, line)
	}
	return b.String()
}

// copiedLinePrefixRe matches leading read_file display prefixes ("12→") that
// local models often paste into edit_file oldText by mistake.
var copiedLinePrefixRe = regexp.MustCompile(`(?m)^\d+→`)

// stripCopiedLinePrefixes removes per-line "N→" display prefixes from a string.
// Used only as an edit_file match recovery path when exact match fails.
func stripCopiedLinePrefixes(s string) string {
	return copiedLinePrefixRe.ReplaceAllString(s, "")
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
	// Prefer camelCase; fall back to Claude/grok snake_case aliases.
	oldText, _ := args["oldText"].(string)
	if oldText == "" {
		oldText, _ = args["old_string"].(string)
	}
	newText, hasNewText := args["newText"].(string)
	if !hasNewText {
		newText, _ = args["new_string"].(string)
	}
	replaceAll := false
	if v, ok := args["replace_all"].(bool); ok {
		replaceAll = v
	}

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

	// Try needles in order: raw oldText first, then oldText with accidental
	// read_file "N→" prefixes stripped (models often paste the display form).
	needles := []string{oldText}
	if stripped := stripCopiedLinePrefixes(oldText); stripped != "" && stripped != oldText {
		needles = append(needles, stripped)
	}

	for needleIdx, needle := range needles {
		// 1) Exact match first (preserves historical behavior and uniqueness).
		count := strings.Count(content, needle)
		if count > 0 {
			if count > 1 && !replaceAll {
				return "", fmt.Errorf("text appears %d times in %s, must be unique (set replace_all=true to replace every occurrence)", count, path)
			}
			n := 1
			if replaceAll {
				n = -1
			}
			// First occurrence byte offset for snippet (exact path).
			startPos := strings.Index(content, needle)
			newContent := strings.Replace(content, needle, newText, n)
			if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
				return "", fmt.Errorf("write %s: %w", path, err)
			}
			replaced := count
			if !replaceAll {
				replaced = 1
			}
			snippet := editSnippet(newContent, startPos, newText, 3)
			note := ""
			if needleIdx > 0 {
				note = " after stripping read_file line-number prefixes"
			}
			return fmt.Sprintf("Edited %s (replaced %d occurrence(s)%s, %d→%d bytes)\n\nSnippet:\n%s",
				path, replaced, note, len(needle), len(newText), snippet), nil
		}

		// 2) Confusable-normalized fallback (typography only). Match discovery only;
		// replacements still use original byte ranges so file encoding is preserved
		// outside the matched span.
		kind, matches := findNormalizedMatchPositions(content, needle)
		switch kind {
		case normAmbiguous:
			return "", fmt.Errorf(
				"text match is ambiguous in %s after Unicode confusable normalization "+
					"(partial/overlapping candidates). Use a longer oldText anchored on nearby ASCII-only context, "+
					"or re-read the file with read_file and retry with the exact bytes",
				path)
		case normMatches:
			if len(matches) > 1 && !replaceAll {
				return "", fmt.Errorf(
					"text appears %d times in %s after confusable normalization, must be unique "+
						"(set replace_all=true to replace every occurrence)",
					len(matches), path)
			}
			use := matches
			if !replaceAll {
				use = matches[:1]
			}
			startPos := use[0].originalStart
			// newContent positions for snippet: first replacement lands at originalStart
			// (prefix length unchanged before the first match).
			newContent := replaceNormalizedMatches(content, use, newText)
			if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
				return "", fmt.Errorf("write %s: %w", path, err)
			}
			snippet := editSnippet(newContent, startPos, newText, 3)
			note := " via confusable-normalized match"
			if needleIdx > 0 {
				note += " after stripping read_file line-number prefixes"
			}
			return fmt.Sprintf("Edited %s (replaced %d occurrence(s)%s, %d→%d bytes)\n\nSnippet:\n%s",
				path, len(use), note, use[0].originalLen, len(newText), snippet), nil
		}
	}

	// 3) Not found — rich error so the model stops retrying the same wrong string.
	return "", fmt.Errorf("%s", editNotFoundMessage(path, content, oldText, strings.Count(content, oldText)))
}

// editNotFoundMessage builds a diagnostic error for a failed edit_file match.
// Includes occurrence count (0), a nearest-match line hint, line-prefix warning,
// and a re-read nudge.
func editNotFoundMessage(path, content, oldText string, exactCount int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "text not found in %s (exact occurrences: %d)", path, exactCount)
	if hint := buildNearestMatchHint(content, oldText); hint != "" {
		b.WriteString(hint)
	}
	// Prefer the stripped form for nearest-match when the model pasted prefixes.
	if stripped := stripCopiedLinePrefixes(oldText); stripped != "" && stripped != oldText {
		if hint := buildNearestMatchHint(content, stripped); hint != "" && !strings.Contains(b.String(), hint) {
			b.WriteString(hint)
		}
		b.WriteString("\n\nHint: oldText looks like it still has read_file line-number prefixes (`N→`). " +
			"Remove them — only the text after `→` is real file content.")
	} else {
		b.WriteString("\n\nHint: if you copied from read_file, remove the line-number prefix (`N→`) first — " +
			"only the text after `→` is real file content.")
	}
	b.WriteString("\n\nRe-read the file with read_file, then retry edit_file with the exact text from the file content (not approximate memory).")
	return b.String()
}

// buildNearestMatchHint finds the first file line containing the longest token
// from oldText's first line. Returns "\n\nNearest match: line N: ..." (≤200 chars)
// or empty if no useful token/match exists. Ported from grok-build search_replace.
func buildNearestMatchHint(file, oldText string) string {
	firstLine := oldText
	if i := strings.IndexByte(oldText, '\n'); i >= 0 {
		firstLine = oldText[:i]
	}
	keyword := ""
	for _, w := range strings.Fields(firstLine) {
		if len(w) > len(keyword) {
			keyword = w
		}
	}
	if keyword == "" {
		return ""
	}
	for i, line := range strings.Split(file, "\n") {
		if strings.Contains(line, keyword) {
			full := fmt.Sprintf("\n\nNearest match: line %d: %s", i+1, strings.TrimRight(line, "\r"))
			if len(full) > 200 {
				// Truncate with ellipsis (ASCII) to keep error messages bounded.
				return full[:199] + "…"
			}
			return full
		}
	}
	return ""
}

// editSnippet returns ~contextLines lines before/after the edit region in
// newContent, with 1-based line prefixes "N→" for model self-verification.
func editSnippet(newContent string, startPos int, inserted string, contextLines int) string {
	if startPos < 0 {
		startPos = 0
	}
	if startPos > len(newContent) {
		startPos = len(newContent)
	}
	// Line index (0-based) where the insertion begins.
	startLine := strings.Count(newContent[:startPos], "\n")
	// How many lines the inserted text spans (at least 1).
	insertedLines := strings.Count(inserted, "\n") + 1
	if inserted == "" {
		insertedLines = 1
	}
	endLine := startLine + insertedLines - 1

	lines := strings.SplitAfter(newContent, "\n")
	// SplitAfter keeps trailing empty after final \n; drop if file ends with \n.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	if total == 0 {
		return "(empty file)"
	}
	if endLine >= total {
		endLine = total - 1
	}

	snippetStart := startLine - contextLines
	if snippetStart < 0 {
		snippetStart = 0
	}
	snippetEnd := endLine + contextLines
	if snippetEnd >= total {
		snippetEnd = total - 1
	}

	var b strings.Builder
	for i := snippetStart; i <= snippetEnd; i++ {
		line := lines[i]
		// Strip trailing newline for display; keep content as-is otherwise.
		display := strings.TrimSuffix(line, "\n")
		display = strings.TrimSuffix(display, "\r")
		fmt.Fprintf(&b, "%d→%s\n", i+1, display)
	}
	return strings.TrimRight(b.String(), "\n")
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

// execTodoWrite updates the session TodoState. Pure state storage — no LLM call.
func (tr *ToolRegistry) execTodoWrite(_ context.Context, args map[string]interface{}) (string, error) {
	if !tr.todoEnabled {
		return "", fmt.Errorf("todo_write is disabled (embedded_todo_gate is off)")
	}
	merge, updates, err := parseTodoWriteArgs(args)
	if err != nil {
		return "", err
	}
	if err := tr.todo.Apply(merge, updates); err != nil {
		return "", err
	}
	return tr.todo.Summary(), nil
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

// executeSpawnSubagents runs multiple read-only sub-agents in parallel.
// Each sub-agent runs via the SubagentSpawner callback with its own context.
// Results are combined and truncated to fit within truncateToolResult limits.
//
// This function is called directly from the loop's tool dispatch section,
// bypassing dispatchTool to avoid the 5-minute hard timeout (#7461 I1).
//
// Returns SubagentSpawnResult with combined content and aggregated token/cost
// usage for parent task accumulation (#7463 review: Critical #2 — struct from
// the start, no return-type mismatch with step-05).
func executeSpawnSubagents(ctx context.Context, tr *ToolRegistry, args map[string]interface{}, onOutput func(string)) (*SubagentSpawnResult, error) {
	if !tr.HasSubagentSpawner() {
		return nil, fmt.Errorf("spawn_subagents is not available in this context")
	}

	// Per-task spawn limit (#7463 review: Important #4 — README guard)
	if !tr.CanSpawn() {
		return nil, fmt.Errorf("spawn_subagents call limit reached (%d/%d per task); "+
			"consolidate remaining work into existing results", tr.spawnCount, maxSpawnsPerTask)
	}

	rawList, ok := args["subagents"]
	if !ok {
		return nil, fmt.Errorf("missing 'subagents' parameter")
	}
	list, ok := rawList.([]interface{})
	if !ok {
		return nil, fmt.Errorf("'subagents' must be an array")
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("'subagents' array is empty")
	}

	const maxSubagents = 4 // #7461 C2: README 기준 4개
	if len(list) > maxSubagents {
		return nil, fmt.Errorf("too many sub-agents (%d); maximum is %d", len(list), maxSubagents)
	}

	// Increment spawn count only after all validation passes so malformed
	// arguments do not consume a spawn slot (#7478 review issue #4).
	tr.spawnCount++

	type subResult struct {
		name   string
		result *SubagentResult
		err    error
	}

	results := make([]subResult, len(list))
	var wg sync.WaitGroup

	emitOutput(onOutput, fmt.Sprintf("[SUB:*] %d개 서브에이전트 시작\n", len(list)))

	for i, raw := range list {
		spec, ok := raw.(map[string]interface{})
		if !ok {
			results[i] = subResult{name: fmt.Sprintf("agent-%d", i), err: fmt.Errorf("invalid spec")}
			continue
		}

		name, _ := spec["name"].(string)
		if name == "" {
			name = fmt.Sprintf("agent-%d", i)
		}
		prompt, _ := spec["prompt"].(string)
		if prompt == "" {
			results[i] = subResult{name: name, err: fmt.Errorf("empty prompt")}
			continue
		}
		endpointID, _ := spec["endpoint_id"].(string)

		maxIter := 15 // default
		if mi, ok := spec["max_iterations"].(float64); ok && mi > 0 {
			maxIter = int(mi)
		}

		wg.Add(1)
		go func(idx int, name, prompt, endpointID string, maxIter int) {
			defer wg.Done()

			emitOutput(onOutput, fmt.Sprintf("[SUB:%s] 시작 — %s\n", name, truncatePrompt(prompt)))

			result, err := tr.subagentSpawner(ctx, name, prompt, endpointID, maxIter, onOutput)
			results[idx] = subResult{name: name, result: result, err: err}

			if err != nil {
				emitOutput(onOutput, fmt.Sprintf("[SUB:%s] ❌ 실패 — %v\n", name, err))
			} else {
				emitOutput(onOutput, fmt.Sprintf("[SUB:%s] ✅ 완료 — %d chars\n", name, len([]rune(result.Content))))
			}
		}(i, name, prompt, endpointID, maxIter)
	}

	wg.Wait()

	emitOutput(onOutput, fmt.Sprintf("[SUB:*] %d개 서브에이전트 완료\n", len(list)))

	// Build combined result — truncate each result to fit within the 8000-char
	// truncateToolResult limit. Divide budget equally among successful agents.
	successCount := 0
	for _, r := range results {
		if r.err == nil {
			successCount++
		}
	}

	var perAgentBudget int
	if successCount > 0 {
		perAgentBudget = 7500 / successCount // leave room for headers/formatting
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## %d개 서브에이전트 실행 완료\n\n", len(results)))

	var totalPrompt, totalCompletion int64
	var totalCost float64

	for _, r := range results {
		sb.WriteString(fmt.Sprintf("### %s\n", r.name))
		if r.err != nil {
			sb.WriteString(fmt.Sprintf("❌ 오류: %v\n\n", r.err))
		} else {
			content := r.result.Content
			if len([]rune(content)) > perAgentBudget {
				content = string([]rune(content)[:perAgentBudget]) + "\n... (truncated)"
			}
			sb.WriteString(content)
			sb.WriteString("\n\n")

			// Aggregate token/cost usage (#7461 I3)
			totalPrompt += r.result.PromptTokens
			totalCompletion += r.result.CompletionTokens
			totalCost += r.result.CostUSD
		}
	}

	return &SubagentSpawnResult{
		Content:          sb.String(),
		PromptTokens:     totalPrompt,
		CompletionTokens: totalCompletion,
		CostUSD:          totalCost,
	}, nil
}

// truncatePrompt returns a short prefix of the prompt for log output.
func truncatePrompt(s string) string {
	r := []rune(s)
	if len(r) > 80 {
		return string(r[:80]) + "..."
	}
	return s
}
