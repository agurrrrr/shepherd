package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sync"

	"github.com/agurrrrr/shepherd/internal/procutil"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// OpenCodeConfig OpenCode provider configuration
type OpenCodeConfig struct {
	Model   string `yaml:"model"`   // Model name in provider/model format (e.g., openai/gpt-4o, local-llm/qwen2.5:14b)
	Timeout int    `yaml:"timeout"` // Timeout in seconds, default 300
}

// DefaultOpenCodeConfig returns default OpenCode config
func DefaultOpenCodeConfig() *OpenCodeConfig {
	return &OpenCodeConfig{
		Timeout: 300,
	}
}

// OpenCodeConfigPath returns path to OpenCode config file
func OpenCodeConfigPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".shepherd", "opencode.yaml")
}

// legacyConfigPath returns path to old local-llm.yaml config file
func legacyConfigPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".shepherd", "local-llm.yaml")
}

// LoadOpenCodeConfig loads OpenCode configuration.
// If no model is set in shepherd's config, reads from OpenCode's native config.json.
func LoadOpenCodeConfig() (*OpenCodeConfig, error) {
	configPath := OpenCodeConfigPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Try legacy path
			data, err = os.ReadFile(legacyConfigPath())
			if err != nil {
				if os.IsNotExist(err) {
					cfg := DefaultOpenCodeConfig()
					// Fill model from OpenCode's native config
					cfg.Model = ReadOpenCodeNativeModel()
					return cfg, nil
				}
				return nil, err
			}
			// Migrate: parse legacy format
			return parseLegacyConfig(data)
		}
		return nil, err
	}

	var config OpenCodeConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	if config.Timeout == 0 {
		config.Timeout = 300
	}

	// If no model override in shepherd config, read from OpenCode's native config
	if config.Model == "" {
		config.Model = ReadOpenCodeNativeModel()
	}

	return &config, nil
}

// parseLegacyConfig parses old local-llm.yaml format into OpenCodeConfig
func parseLegacyConfig(data []byte) (*OpenCodeConfig, error) {
	var legacy struct {
		BaseURL string `yaml:"base_url"`
		Model   string `yaml:"model"`
		APIKey  string `yaml:"api_key"`
		Timeout int    `yaml:"timeout"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}

	cfg := &OpenCodeConfig{
		Timeout: legacy.Timeout,
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 300
	}

	// Convert legacy model format: "qwen2.5:14b" -> "local-llm/qwen2.5:14b"
	if legacy.Model != "" {
		if !strings.Contains(legacy.Model, "/") {
			cfg.Model = "local-llm/" + legacy.Model
		} else {
			cfg.Model = legacy.Model
		}
	}

	return cfg, nil
}

// SaveOpenCodeConfig saves OpenCode configuration
func SaveOpenCodeConfig(config *OpenCodeConfig) error {
	configPath := OpenCodeConfigPath()

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

// OpenCodeConfigExists checks if OpenCode config file exists
func OpenCodeConfigExists() bool {
	_, err := os.Stat(OpenCodeConfigPath())
	if err == nil {
		return true
	}
	// Also check legacy path
	_, err = os.Stat(legacyConfigPath())
	return err == nil
}

// OpenCodeNativeConfigPaths returns all possible paths to OpenCode's native config.json.
// OpenCode may store config in multiple locations depending on OS and version.
func OpenCodeNativeConfigPaths() []string {
	var paths []string

	// Primary: os.UserConfigDir (Go standard)
	if configDir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(configDir, "opencode", "config.json"))
	}

	// Windows-specific: APPDATA env var (may differ from UserConfigDir)
	if runtime.GOOS == "windows" {
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			p := filepath.Join(appdata, "opencode", "config.json")
			if !containsPath(paths, p) {
				paths = append(paths, p)
			}
		}
	}

	// Home-based fallbacks (OpenCode may use ~/.opencode/ pattern)
	homeDir, _ := os.UserHomeDir()
	homeBased := filepath.Join(homeDir, ".opencode", "config.json")
	if !containsPath(paths, homeBased) {
		paths = append(paths, homeBased)
	}

	// .config fallback
	dotConfig := filepath.Join(homeDir, ".config", "opencode", "config.json")
	if !containsPath(paths, dotConfig) {
		paths = append(paths, dotConfig)
	}

	return paths
}

// containsPath checks if a path already exists in a slice.
func containsPath(paths []string, p string) bool {
	for _, existing := range paths {
		if existing == p {
			return true
		}
	}
	return false
}

// OpenCodeNativeConfigPath returns the primary path to OpenCode's native config.json.
// Linux: ~/.config/opencode/config.json
// Windows: %APPDATA%\opencode\config.json
// macOS: ~/Library/Application Support/opencode/config.json
// Deprecated: Use OpenCodeNativeConfigPaths() for all possible locations.
func OpenCodeNativeConfigPath() string {
	paths := OpenCodeNativeConfigPaths()
	if len(paths) > 0 {
		return paths[0]
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".config", "opencode", "config.json")
}

// stripUTF8BOM removes UTF-8 BOM (0xEF 0xBB 0xBF) from the beginning of data.
func stripUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
}

// readOpenCodeConfigFile reads config from the first existing path.
// The returned bytes are normalized to strict JSON (see normalizeJSONC).
func readOpenCodeConfigFile() ([]byte, string, error) {
	for _, configPath := range OpenCodeNativeConfigPaths() {
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		data = normalizeJSONC(stripUTF8BOM(data))
		return data, configPath, nil
	}
	return nil, "", os.ErrNotExist
}

// normalizeJSONC converts JSONC (// and /* */ comments, trailing commas) into
// strict JSON. OpenCode parses config.json as JSONC, so a file OpenCode happily
// accepts can still make encoding/json fail — which silently emptied the model
// selector and the native model lookup.
func normalizeJSONC(data []byte) []byte {
	return stripJSONTrailingCommas(stripJSONComments(data))
}

// stripJSONComments removes // and /* */ comments outside string literals.
func stripJSONComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '/' && i+1 < len(data) {
			switch data[i+1] {
			case '/':
				// Skip to the end of the line but keep the newline itself.
				for i+1 < len(data) && data[i+1] != '\n' {
					i++
				}
				continue
			case '*':
				end := bytes.Index(data[i+2:], []byte("*/"))
				if end < 0 {
					return out
				}
				i += 2 + end + 1 // land on the closing '/'
				continue
			}
		}
		if c == '"' {
			inString = true
		}
		out = append(out, c)
	}
	return out
}

// stripJSONTrailingCommas drops commas (outside string literals) whose next
// non-whitespace character closes an object or array.
func stripJSONTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		if c == '"' {
			inString = true
		}
		out = append(out, c)
	}
	return out
}

// ReadOpenCodeNativeModel reads the model from OpenCode's own config file.
// This is the source of truth for which model OpenCode will use,
// so Shepherd reads it directly instead of maintaining a separate copy.
func ReadOpenCodeNativeModel() string {
	data, configPath, err := readOpenCodeConfigFile()
	if err != nil {
		log.Printf("[opencode] config not found (searched: %v): %v", OpenCodeNativeConfigPaths(), err)
		return ""
	}

	var nativeConfig struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(data, &nativeConfig); err != nil {
		log.Printf("[opencode] JSON parse failed for %s: %v", configPath, err)
		return ""
	}

	return nativeConfig.Model
}

// OpenCodeModelOption describes a single selectable OpenCode model.
type OpenCodeModelOption struct {
	ID    string `json:"id"`    // "<provider>/<model_key>" — passed to `opencode run -m`
	Label string `json:"label"` // human-readable display name
}

// ListOpenCodeModels returns the selectable OpenCode models exactly as reported
// by `opencode models`. The list is captured once when the daemon starts
// (InitOpenCodeModelList) instead of being derived from OpenCode's config.json,
// so it reflects what `opencode run -m` actually accepts (including built-in
// providers such as opencode/* and opencode-go/*).
func ListOpenCodeModels() []OpenCodeModelOption {
	cliIDs := CachedOpenCodeCLIModels()
	out := make([]OpenCodeModelOption, 0, len(cliIDs))
	seen := make(map[string]bool, len(cliIDs))
	for _, id := range cliIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, OpenCodeModelOption{ID: id, Label: id})
	}
	return out
}

// opencodeModelsTimeout bounds `opencode models`, which loads every provider
// plugin (~2s on a warm machine).
const opencodeModelsTimeout = 20 * time.Second

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// ListOpenCodeCLIModels runs `opencode models` in dir and returns the
// "<provider>/<model>" IDs it prints. This is the authoritative set of what
// `opencode run -m` accepts. dir matters because a project-local opencode.json
// can declare extra providers.
func ListOpenCodeCLIModels(ctx context.Context, dir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, opencodeModelsTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, GetOpenCodeBinary(), "models")
	cmd.Dir = dir
	procutil.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseOpenCodeModelsOutput(string(out)), nil
}

// parseOpenCodeModelsOutput extracts "<provider>/<model>" lines from
// `opencode models` output, ignoring blank lines and anything that is not a
// single provider-qualified token.
func parseOpenCodeModelsOutput(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(ansiEscapeRe.ReplaceAllString(line, ""))
		if line == "" || strings.ContainsAny(line, " \t") {
			continue
		}
		if i := strings.Index(line, "/"); i <= 0 || i == len(line)-1 {
			continue
		}
		ids = append(ids, line)
	}
	return ids
}

var opencodeCLIModelsCache struct {
	mu      sync.RWMutex
	ids     []string
	fetched bool
}

// InitOpenCodeModelList queries `opencode models` once at daemon startup and
// caches the result. The Settings model selector only ever reads this cache, so
// the CLI is not spawned again while the daemon is running. If the call fails
// the cache stays empty and CachedOpenCodeCLIModels retries lazily.
func InitOpenCodeModelList() {
	refreshOpenCodeCLIModels()
}

// CachedOpenCodeCLIModels returns the `opencode models` list captured by
// InitOpenCodeModelList. If the daemon has not initialized it yet (e.g. in
// tests or one-shot CLI paths), the CLI runs synchronously on first use.
func CachedOpenCodeCLIModels() []string {
	c := &opencodeCLIModelsCache
	c.mu.RLock()
	if c.fetched {
		ids := c.ids
		c.mu.RUnlock()
		return ids
	}
	c.mu.RUnlock()
	return refreshOpenCodeCLIModels()
}

func refreshOpenCodeCLIModels() []string {
	dir, _ := os.UserHomeDir()
	ids, err := ListOpenCodeCLIModels(context.Background(), dir)
	if err != nil {
		log.Printf("[opencode] `opencode models` failed: %v", err)
		return nil
	}

	c := &opencodeCLIModelsCache
	c.mu.Lock()
	c.ids = ids
	c.fetched = true
	c.mu.Unlock()
	return ids
}

// DiagnoseOpenCodeModel returns a hint when model is not something OpenCode
// knows, or "" when it is known (or the list cannot be obtained). OpenCode
// reports an unknown model only as "Unexpected server error" — the actual
// ProviderModelNotFoundError stays in its own server log — so callers append
// this hint to make the cause visible. dir should be the project directory the
// run happened in.
func DiagnoseOpenCodeModel(ctx context.Context, dir, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	ids, err := ListOpenCodeCLIModels(ctx, dir)
	if err != nil || len(ids) == 0 {
		return ""
	}
	return unknownOpenCodeModelHint(model, ids)
}

// unknownOpenCodeModelHint builds the hint for a model missing from ids,
// suggesting IDs with the same model key under another provider (e.g.
// "opencode-zen/x" → "opencode/x"). Returns "" when model is in ids.
func unknownOpenCodeModelHint(model string, ids []string) string {
	key := model
	if i := strings.Index(model, "/"); i >= 0 {
		key = model[i+1:]
	}
	var similar []string
	for _, id := range ids {
		if id == model {
			return ""
		}
		if i := strings.Index(id, "/"); i >= 0 && id[i+1:] == key {
			similar = append(similar, `"`+id+`"`)
		}
	}
	hint := fmt.Sprintf("model %q is not in `opencode models`", model)
	if len(similar) > 0 {
		hint += " (did you mean " + strings.Join(similar, " or ") + "?)"
	}
	return hint + "; check Settings > OpenCode Model or \"model\" in OpenCode's config.json"
}

// GetModelDisplayName returns display name (e.g., "opencode(local-llm/devstral-small-2)")
// Priority: shepherd config override → OpenCode native config → CLI detection → fallback
func (c *OpenCodeConfig) GetModelDisplayName() string {
	// 1. Shepherd's own config override (if explicitly set)
	if c.Model != "" {
		return "opencode(" + c.Model + ")"
	}

	// 2. Read from OpenCode's native config.json (fast, no subprocess)
	if model := ReadOpenCodeNativeModel(); model != "" {
		return "opencode(" + model + ")"
	}

	// 3. Try to detect model via CLI (slow fallback)
	if model := DetectOpenCodeModel(); model != "" {
		return "opencode(" + model + ")"
	}

	return "opencode"
}

// DetectOpenCodeModel tries to detect the current model from opencode CLI.
// This is a slow fallback that spawns a subprocess — prefer ReadOpenCodeNativeModel().
func DetectOpenCodeModel() string {
	binary := GetOpenCodeBinary()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "models")
	procutil.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Parse output: look for active/selected model
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Look for lines with active marker (*, >, or similar)
		if strings.HasPrefix(line, "*") || strings.HasPrefix(line, ">") {
			model := strings.TrimSpace(strings.TrimLeft(line, "*> "))
			if model != "" {
				return model
			}
		}
	}

	// If no active marker, return first non-empty line
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "─") && !strings.HasPrefix(line, "=") {
			return line
		}
	}

	return ""
}

// Backward compatibility aliases
type LocalLLMConfig = OpenCodeConfig

func LoadLocalLLMConfig() (*OpenCodeConfig, error) { return LoadOpenCodeConfig() }
func LocalLLMConfigPath() string                   { return OpenCodeConfigPath() }
func LocalLLMConfigExists() bool                   { return OpenCodeConfigExists() }
func DefaultLocalLLMConfig() *OpenCodeConfig       { return DefaultOpenCodeConfig() }
func SaveLocalLLMConfig(c *OpenCodeConfig) error   { return SaveOpenCodeConfig(c) }
