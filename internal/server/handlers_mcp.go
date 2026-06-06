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
		"pi":       checkPiMCP(homeDir),
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
	case "pi":
		if err := registerPiMCP(homeDir); err != nil {
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
		"command": []string{"shepherd", "mcp"},
		"enabled": true,
	}
	config["mcp"] = mcp

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize config: %w", err)
	}
	return os.WriteFile(configPath, append(out, '\n'), 0644)
}

// checkPiMCP checks if the pi extension for shepherd MCP is registered.
// Pi does not use MCP server config like Claude/OpenCode.
// Instead, Pi uses a TypeScript extension at ~/.pi/agent/extensions/shepherd-mcp.ts
// that registers shepherd tools as native Pi tools via the extension API.
func checkPiMCP(homeDir string) mcpProviderStatus {
	extensionPath := filepath.Join(homeDir, ".pi", "agent", "extensions", "shepherd-mcp.ts")
	status := mcpProviderStatus{ConfigPath: extensionPath}

	_, err := os.Stat(extensionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status
		}
		status.Error = "cannot check file: " + err.Error()
		return status
	}
	status.ConfigExists = true
	status.Registered = true
	return status
}

// registerPiMCP creates a pi extension that registers shepherd MCP tools
// natively via the pi extension API, so pi can call them as first-class tools
// in interactive sessions (complementing the system-prompt injection used by
// shepherd tasks).
func registerPiMCP(homeDir string) error {
	extDir := filepath.Join(homeDir, ".pi", "agent", "extensions")
	extPath := filepath.Join(extDir, "shepherd-mcp.ts")

	if err := os.MkdirAll(extDir, 0755); err != nil {
		return fmt.Errorf("cannot create directory: %w", err)
	}

	extensionCode := `// Shepherd MCP Extension for Pi
// Auto-generated by Shepherd — manages shepherd MCP tools as native Pi tools.
// These tools give the LLM access to Shepherd's project management, browser
// automation, wiki, skills, and task scheduling capabilities.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

function readRuntime(): { addr: string; mcpToken: string } {
  const runtimePath = path.join(os.homedir(), ".shepherd", "runtime.json");
  const raw = fs.readFileSync(runtimePath, "utf-8");
  const info = JSON.parse(raw);
  return { addr: info.addr, mcpToken: info.mcp_token };
}

async function callTool(name: string, args: Record<string, unknown>): Promise<string> {
  try {
    const runtime = readRuntime();
    const body = JSON.stringify({ tool: name, args });
    const resp = await fetch(runtime.addr + "/api/_internal/mcp/call", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-MCP-Token": runtime.mcpToken,
      },
      body,
    });
    const envelope = await resp.json();
    if (!resp.ok || !envelope.success) {
      throw new Error(envelope.error || "MCP call failed: " + resp.statusText);
    }
    return typeof envelope.data === "string"
      ? envelope.data
      : JSON.stringify(envelope.data);
  } catch (e: any) {
    return "Error: " + e.message;
  }
}

export default function (pi: ExtensionAPI) {
  pi.on("session_start", async (_event, ctx) => {
    ctx.ui.notify("Shepherd MCP tools loaded", "info");
  });

  // --- Task management ---
  pi.registerTool({
    name: "task_start",
    label: "Task Start",
    description: "Queue a task in Shepherd. Requires sheep_name, project_name, prompt.",
    parameters: Type.Object({
      sheep_name: Type.String({ description: "Name of the sheep to assign" }),
      project_name: Type.String({ description: "Project name" }),
      prompt: Type.String({ description: "Task description/prompt" }),
    }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: await callTool("task_start", params as any) }] };
    },
  });

  pi.registerTool({
    name: "task_complete",
    label: "Task Complete",
    description: "Record task completion. Requires task_id and summary.",
    parameters: Type.Object({
      task_id: Type.Number({ description: "Task ID" }),
      summary: Type.String({ description: "Completion summary" }),
    }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: await callTool("task_complete", params as any) }] };
    },
  });

  pi.registerTool({
    name: "task_error",
    label: "Task Error",
    description: "Record task error. Requires task_id and error message.",
    parameters: Type.Object({
      task_id: Type.Number({ description: "Task ID" }),
      error: Type.String({ description: "Error message" }),
    }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: await callTool("task_error", params as any) }] };
    },
  });

  pi.registerTool({
    name: "get_history",
    label: "Get History",
    description: "Query project task history. Requires project_name, limit is optional.",
    parameters: Type.Object({
      project_name: Type.String({ description: "Project name" }),
      limit: Type.Optional(Type.Number({ description: "Max results (default 10)" })),
    }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: await callTool("get_history", params as any) }] };
    },
  });

  pi.registerTool({
    name: "get_status",
    label: "Get Status",
    description: "Get overall Shepherd system status.",
    parameters: Type.Object({}),
    async execute() {
      return { content: [{ type: "text", text: await callTool("get_status", {}) }] };
    },
  });

  // --- Skills ---
  pi.registerTool({
    name: "skill_load",
    label: "Skill Load",
    description: "Load full content of a skill by name.",
    parameters: Type.Object({
      name: Type.String({ description: "Skill name to load" }),
    }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: await callTool("skill_load", params as any) }] };
    },
  });

  // --- Wiki ---
  pi.registerTool({
    name: "wiki_read_page",
    label: "Wiki Read Page",
    description: "Read a wiki page by project and slug.",
    parameters: Type.Object({
      project_name: Type.String({ description: "Project name" }),
      slug: Type.String({ description: "Page slug" }),
    }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: await callTool("wiki_read_page", params as any) }] };
    },
  });

  pi.registerTool({
    name: "wiki_list_pages",
    label: "Wiki List Pages",
    description: "List wiki pages for a project.",
    parameters: Type.Object({
      project_name: Type.String({ description: "Project name" }),
    }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: await callTool("wiki_list_pages", params as any) }] };
    },
  });

  pi.registerTool({
    name: "wiki_search",
    label: "Wiki Search",
    description: "Search wiki pages by query.",
    parameters: Type.Object({
      project_name: Type.String({ description: "Project name" }),
      query: Type.String({ description: "Search query" }),
    }),
    async execute(_id, params) {
      return { content: [{ type: "text", text: await callTool("wiki_search", params as any) }] };
    },
  });

  // --- Browser automation ---
  const browserTools = [
    { name: "browser_session_start", params: { sheep_name: Type.String({ description: "Sheep name (required)" }) } },
    { name: "browser_session_stop", params: { sheep_name: Type.Optional(Type.String({ description: "Sheep name" })) } },
    { name: "browser_list_pages", params: {} },
    { name: "browser_open", params: { url: Type.String({ description: "URL to open" }) } },
    { name: "browser_close", params: {} },
    { name: "browser_navigate", params: { url: Type.String({ description: "URL to navigate to" }) } },
    { name: "browser_reload", params: {} },
    { name: "browser_back", params: {} },
    { name: "browser_forward", params: {} },
    { name: "browser_click", params: { selector: Type.String({ description: "CSS selector" }) } },
    { name: "browser_type", params: { selector: Type.String({ description: "CSS selector" }), text: Type.String({ description: "Text to type" }) } },
    { name: "browser_select", params: { selector: Type.String({ description: "CSS selector" }), value: Type.String({ description: "Option value" }) } },
    { name: "browser_check", params: { selector: Type.String({ description: "CSS selector" }) } },
    { name: "browser_hover", params: { selector: Type.String({ description: "CSS selector" }) } },
    { name: "browser_scroll", params: { direction: Type.Optional(Type.String({ description: "up|down|left|right" })), amount: Type.Optional(Type.Number({ description: "Scroll amount" })) } },
    { name: "browser_get_text", params: { selector: Type.Optional(Type.String({ description: "Element selector (default: body)" })) } },
    { name: "browser_get_html", params: { selector: Type.Optional(Type.String({ description: "Element selector (default: body)" })) } },
    { name: "browser_get_attribute", params: { selector: Type.String({ description: "CSS selector" }), attribute: Type.String({ description: "Attribute name" }) } },
    { name: "browser_get_url", params: {} },
    { name: "browser_get_title", params: {} },
    { name: "browser_eval", params: { script: Type.String({ description: "JavaScript to execute" }) } },
    { name: "browser_wait_selector", params: { selector: Type.String({ description: "CSS selector" }), timeout: Type.Optional(Type.Number({ description: "Timeout ms" })) } },
    { name: "browser_wait_hidden", params: { selector: Type.String({ description: "CSS selector" }), timeout: Type.Optional(Type.Number({ description: "Timeout ms" })) } },
    { name: "browser_wait_load", params: { timeout: Type.Optional(Type.Number({ description: "Timeout ms" })) } },
    { name: "browser_wait_idle", params: { timeout: Type.Optional(Type.Number({ description: "Timeout ms" })) } },
    { name: "browser_screenshot", params: { selector: Type.Optional(Type.String({ description: "Element selector (default: full page)" })) } },
    { name: "browser_pdf", params: { selector: Type.Optional(Type.String({ description: "Element selector" })) } },
  ];

  for (const tool of browserTools) {
    pi.registerTool({
      name: tool.name,
      label: tool.name.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase()),
      description: "Browser automation: " + tool.name,
      parameters: Type.Object(tool.params),
      async execute(_id, params) {
        return { content: [{ type: "text", text: await callTool(tool.name, params as any) }] };
      },
    });
  }
}
`;

	return os.WriteFile(extPath, []byte(extensionCode), 0644)
}
