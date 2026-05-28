package config

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
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
func readOpenCodeConfigFile() ([]byte, string, error) {
	for _, configPath := range OpenCodeNativeConfigPaths() {
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		data = stripUTF8BOM(data)
		return data, configPath, nil
	}
	return nil, "", os.ErrNotExist
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

// ListOpenCodeModels returns the list of models defined in OpenCode's native
// config.json (custom providers defined by the user). The currently-selected
// `model` field is also included at the top of the list so users can keep
// using it even if it points to a built-in provider not declared under
// `provider.*.models.*`.
func ListOpenCodeModels() []OpenCodeModelOption {
	data, configPath, err := readOpenCodeConfigFile()
	if err != nil {
		log.Printf("[opencode] ListOpenCodeModels: config not found: %v", err)
		return nil
	}

	var raw struct {
		Model    string `json:"model"`
		Provider map[string]struct {
			Name   string `json:"name"`
			Models map[string]struct {
				Name string `json:"name"`
			} `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Printf("[opencode] ListOpenCodeModels: JSON parse failed for %s: %v", configPath, err)
		return nil
	}

	seen := map[string]bool{}
	out := make([]OpenCodeModelOption, 0)

	// Current default first, so users recognize "what I'm already using".
	if raw.Model != "" {
		out = append(out, OpenCodeModelOption{ID: raw.Model, Label: raw.Model + " (current default)"})
		seen[raw.Model] = true
	}

	for provKey, prov := range raw.Provider {
		for modelKey, model := range prov.Models {
			id := provKey + "/" + modelKey
			if seen[id] {
				continue
			}
			seen[id] = true
			label := id
			if model.Name != "" {
				label = model.Name + " (" + id + ")"
			} else if prov.Name != "" {
				label = prov.Name + " / " + modelKey
			}
			out = append(out, OpenCodeModelOption{ID: id, Label: label})
		}
	}

	return out
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
