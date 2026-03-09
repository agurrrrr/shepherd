package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/gofiber/fiber/v2"
)

type mcpProviderStatus struct {
	Registered  bool   `json:"registered"`
	ConfigPath  string `json:"config_path"`
	ConfigExists bool  `json:"config_exists"`
	Error       string `json:"error,omitempty"`
}

// GET /api/mcp/status
func (s *Server) handleMCPStatus(c *fiber.Ctx) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "cannot determine home directory")
	}

	return success(c, fiber.Map{
		"claude":   checkClaudeMCP(homeDir),
		"opencode": checkOpenCodeMCP(homeDir),
	})
}

// POST /api/mcp/register
func (s *Server) handleMCPRegister(c *fiber.Ctx) error {
	var body struct {
		Provider string `json:"provider"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "cannot determine home directory")
	}

	switch body.Provider {
	case "claude":
		if err := registerClaudeMCP(homeDir); err != nil {
			return fail(c, fiber.StatusInternalServerError, err.Error())
		}
	case "opencode":
		if err := registerOpenCodeMCP(homeDir); err != nil {
			return fail(c, fiber.StatusInternalServerError, err.Error())
		}
	default:
		return fail(c, fiber.StatusBadRequest, "unknown provider: "+body.Provider)
	}

	return success(c, nil)
}

// checkClaudeMCP checks if shepherd is registered in ~/.claude/settings.json
func checkClaudeMCP(homeDir string) mcpProviderStatus {
	configPath := filepath.Join(homeDir, ".claude", "settings.json")
	status := mcpProviderStatus{ConfigPath: configPath}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status
		}
		status.Error = "cannot read file: " + err.Error()
		return status
	}
	status.ConfigExists = true

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		status.Error = "invalid JSON: " + err.Error()
		return status
	}

	mcpServers, ok := settings["mcpServers"].(map[string]interface{})
	if !ok {
		return status
	}

	_, status.Registered = mcpServers["shepherd"]
	return status
}

// checkOpenCodeMCP checks if shepherd is registered in OpenCode's config.json
func checkOpenCodeMCP(homeDir string) mcpProviderStatus {
	configPath := config.OpenCodeNativeConfigPath()
	status := mcpProviderStatus{ConfigPath: configPath}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status
		}
		status.Error = "cannot read file: " + err.Error()
		return status
	}
	status.ConfigExists = true

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		status.Error = "invalid JSON: " + err.Error()
		return status
	}

	mcp, ok := config["mcp"].(map[string]interface{})
	if !ok {
		return status
	}

	_, status.Registered = mcp["shepherd"]
	return status
}

// registerClaudeMCP adds shepherd to ~/.claude/settings.json
func registerClaudeMCP(homeDir string) error {
	configPath := filepath.Join(homeDir, ".claude", "settings.json")

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("cannot create directory: %w", err)
	}

	var settings map[string]interface{}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("cannot read %s: %w", configPath, err)
		}
		settings = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("invalid JSON in %s: %w", configPath, err)
		}
	}

	mcpServers, ok := settings["mcpServers"].(map[string]interface{})
	if !ok {
		mcpServers = make(map[string]interface{})
	}

	mcpServers["shepherd"] = map[string]interface{}{
		"command": "shepherd",
		"args":    []string{"mcp"},
	}
	settings["mcpServers"] = mcpServers

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize config: %w", err)
	}
	return os.WriteFile(configPath, append(out, '\n'), 0644)
}

// registerOpenCodeMCP adds shepherd to OpenCode's config.json
func registerOpenCodeMCP(homeDir string) error {
	configPath := config.OpenCodeNativeConfigPath()

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("cannot create directory: %w", err)
	}

	var config map[string]interface{}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("cannot read %s: %w", configPath, err)
		}
		config = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("invalid JSON in %s: %w", configPath, err)
		}
	}

	mcp, ok := config["mcp"].(map[string]interface{})
	if !ok {
		mcp = make(map[string]interface{})
	}

	mcp["shepherd"] = map[string]interface{}{
		"type":    "local",
		"command": []string{"shepherd", "mcp", "--minimal"},
		"enabled": true,
	}
	config["mcp"] = mcp

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize config: %w", err)
	}
	return os.WriteFile(configPath, append(out, '\n'), 0644)
}
