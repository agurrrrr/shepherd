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
// The extension uses a lazy-load pattern: only a gateway "shepherd" tool is
// visible by default. Actual tools expand on demand or via keyword detection.
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
// natively via the pi extension API. Uses a lazy-load (expand/collapse) pattern
// similar to atsel-mcp.ts: only a gateway "shepherd" tool is visible by default,
// and actual tools are registered and activated on demand.
func registerPiMCP(homeDir string) error {
	extDir := filepath.Join(homeDir, ".pi", "agent", "extensions")
	extPath := filepath.Join(extDir, "shepherd-mcp.ts")

	if err := os.MkdirAll(extDir, 0755); err != nil {
		return fmt.Errorf("cannot create directory: %w", err)
	}

	extensionCode := `// Shepherd MCP Extension for Pi — Lazy/On-Demand Pattern
// ---------------------------------------------------------------------------
// shepherd MCP 도구들을 Pi의 네이티브 도구로 노출하는 확장입니다.
//
// atsel-mcp.ts와 같은 "필요할 때만 펼치게" 패턴을 사용합니다:
// - 평소에는 게이트웨이 도구 shepherd 하나만 시스템 프롬프트에 노출
// - task/wiki/browser/skill 작업이 필요할 때 expand 하여 실제 도구 활성화
// - 작업 완료 후 collapse 로 컨텍스트 가볍게 유지
// ---------------------------------------------------------------------------

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { StringEnum } from "@earendil-works/pi-ai";
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
  let registered = false;
  let expanded = false;
  let toolNames: string[] = [];

  const activeNames = (): string[] =>
    pi.getActiveTools().map((t: any) => (typeof t === "string" ? t : t?.name)).filter(Boolean);

  // All shepherd 도구 정의 (register 시 사용)
  const sheep = { sheep_name: Type.String({ description: "Sheep name" }) };
  const allTools = [
    // Task management
    { name: "task_start", desc: "Queue a task in Shepherd", params: Type.Object({ sheep_name: Type.Optional(Type.String({ description: "Name of the sheep to assign" })), project_name: Type.String({ description: "Project name" }), prompt: Type.String({ description: "Task description/prompt" }) }) },
    { name: "task_complete", desc: "Record task completion", params: Type.Object({ task_id: Type.Number({ description: "Task ID" }), summary: Type.String({ description: "Completion summary" }) }) },
    { name: "task_error", desc: "Record task error", params: Type.Object({ task_id: Type.Number({ description: "Task ID" }), error: Type.String({ description: "Error message" }) }) },
    { name: "get_history", desc: "Query project task history", params: Type.Object({ project_name: Type.String({ description: "Project name" }), limit: Type.Optional(Type.Number({ description: "Max results (default 10)" })) }) },
    { name: "get_status", desc: "Get overall Shepherd system status", params: Type.Object({}) },
    // Skills
    { name: "skill_load", desc: "Load full content of a skill by name", params: Type.Object({ name: Type.String({ description: "Skill name to load" }) }) },
    // Wiki
    { name: "wiki_read_page", desc: "Read a wiki page by project and slug", params: Type.Object({ project_name: Type.String({ description: "Project name" }), slug: Type.String({ description: "Page slug" }) }) },
    { name: "wiki_list_pages", desc: "List wiki pages for a project", params: Type.Object({ project_name: Type.String({ description: "Project name" }) }) },
    { name: "wiki_search", desc: "Search wiki pages by query", params: Type.Object({ project_name: Type.String({ description: "Project name" }), query: Type.String({ description: "Search query" }) }) },
    // Browser automation
    { name: "browser_session_start", desc: "Start browser session", params: Type.Object({ ...sheep, headless: Type.Optional(Type.Boolean({ description: "Headless mode (default true)" })) }) },
    { name: "browser_session_stop", desc: "End browser session", params: Type.Object({ ...sheep }) },
    { name: "browser_list_pages", desc: "List open browser pages", params: Type.Object({ ...sheep }) },
    { name: "browser_open", desc: "Open URL in browser", params: Type.Object({ ...sheep, url: Type.String({ description: "URL to open" }) }) },
    { name: "browser_close", desc: "Close current browser page", params: Type.Object({ ...sheep }) },
    { name: "browser_navigate", desc: "Navigate to URL", params: Type.Object({ ...sheep, url: Type.String({ description: "URL to navigate to" }) }) },
    { name: "browser_reload", desc: "Reload page", params: Type.Object({ ...sheep }) },
    { name: "browser_back", desc: "Go back", params: Type.Object({ ...sheep }) },
    { name: "browser_forward", desc: "Go forward", params: Type.Object({ ...sheep }) },
    { name: "browser_click", desc: "Click element", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }) }) },
    { name: "browser_type", desc: "Type text", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }), text: Type.String({ description: "Text to type" }) }) },
    { name: "browser_select", desc: "Select dropdown option", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }), value: Type.String({ description: "Option value" }) }) },
    { name: "browser_check", desc: "Toggle checkbox", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }), checked: Type.Boolean({ description: "Checked state" }) }) },
    { name: "browser_hover", desc: "Hover over element", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }) }) },
    { name: "browser_scroll", desc: "Scroll page", params: Type.Object({ ...sheep, x: Type.Optional(Type.Number({ description: "Horizontal scroll amount" })), y: Type.Optional(Type.Number({ description: "Vertical scroll amount" })) }) },
    { name: "browser_get_text", desc: "Extract text content", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }) }) },
    { name: "browser_get_html", desc: "Get HTML content", params: Type.Object({ ...sheep, selector: Type.Optional(Type.String({ description: "CSS selector (empty for full page)" })) }) },
    { name: "browser_get_attribute", desc: "Get element attribute", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }), attribute: Type.String({ description: "Attribute name" }) }) },
    { name: "browser_get_url", desc: "Get current URL", params: Type.Object({ ...sheep }) },
    { name: "browser_get_title", desc: "Get page title", params: Type.Object({ ...sheep }) },
    { name: "browser_eval", desc: "Execute JavaScript", params: Type.Object({ ...sheep, js: Type.String({ description: "JavaScript code to execute" }) }) },
    { name: "browser_wait_selector", desc: "Wait for element", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }), timeout: Type.Optional(Type.Number({ description: "Timeout in seconds (default 30)" })) }) },
    { name: "browser_wait_hidden", desc: "Wait for element to hide", params: Type.Object({ ...sheep, selector: Type.String({ description: "CSS selector" }), timeout: Type.Optional(Type.Number({ description: "Timeout in seconds (default 30)" })) }) },
    { name: "browser_wait_load", desc: "Wait for page load", params: Type.Object({ ...sheep }) },
    { name: "browser_wait_idle", desc: "Wait for network idle", params: Type.Object({ ...sheep, timeout: Type.Optional(Type.Number({ description: "Timeout in seconds (default 30)" })) }) },
    { name: "browser_screenshot", desc: "Capture screenshot", params: Type.Object({ ...sheep, selector: Type.Optional(Type.String({ description: "Element selector (empty for full page)" })), path: Type.Optional(Type.String({ description: "Save path (optional)" })) }) },
    { name: "browser_pdf", desc: "Generate PDF", params: Type.Object({ ...sheep, path: Type.Optional(Type.String({ description: "Save path (optional)" })) }) },
    { name: "browser_console_start", desc: "Start console monitoring", params: Type.Object({ ...sheep }) },
    { name: "browser_console_messages", desc: "Get console messages", params: Type.Object({ ...sheep, clear: Type.Optional(Type.Boolean({ description: "Delete messages after reading" })) }) },
    { name: "browser_network_start", desc: "Start network monitoring", params: Type.Object({ ...sheep }) },
    { name: "browser_network_requests", desc: "Get network requests", params: Type.Object({ ...sheep, clear: Type.Optional(Type.Boolean({ description: "Delete list after reading" })) }) },
    { name: "browser_network_request", desc: "Get request detail", params: Type.Object({ ...sheep, request_id: Type.String({ description: "Request ID" }) }) },
  ];

  // 도구들을 (최초 1회) 발견·등록
  async function ensureRegistered(): Promise<string[]> {
    if (registered) return toolNames;
    for (const t of allTools) {
      const tname = t.name;
      pi.registerTool({
        name: tname,
        label: tname.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase()),
        description: t.desc,
        parameters: t.params,
        async execute(_id, params) {
          const out = await callTool(tname, params as any);
          return { content: [{ type: "text", text: out }] };
        },
      });
    }
    toolNames = allTools.map((t) => t.name);
    registered = true;
    return toolNames;
  }

  // 펼치기: shepherd 도구들을 활성 집합에 추가 (게이트웨이 shepherd 유지)
  async function expand(): Promise<string[]> {
    const names = await ensureRegistered();
    const next = new Set(activeNames());
    for (const n of names) next.add(n);
    next.add("shepherd");
    pi.setActiveTools([...next]);
    expanded = true;
    return names;
  }

  // 접기: shepherd 도구들만 활성 집합에서 제거 (게이트웨이 shepherd는 남음)
  function collapse(): void {
    const set = new Set(toolNames);
    const remaining = activeNames().filter((n) => !set.has(n));
    remaining.push("shepherd");
    pi.setActiveTools([...new Set(remaining)]);
    expanded = false;
  }

  function statusText(): string {
    if (!registered) return "Shepherd: not yet loaded (collapsed). " + allTools.length + " tools available.";
    return "Shepherd: " + (expanded ? "expanded" : "collapsed") + " — " + toolNames.length + " tools.\n" + toolNames.map((n) => "- " + n).join("\n");
  }

  const text = (s: string) => ({ content: [{ type: "text" as const, text: s }] });

  // --- 게이트웨이 도구: 평소엔 이것 하나만 노출 ----------------------------
  pi.registerTool({
    name: "shepherd",
    label: "Shepherd (Gateway)",
    description:
      "On-demand gateway to Shepherd project management tools. " +
      "Actual Shepherd tools (task_start, task_complete, get_history, skill_load, " +
      "wiki_read_page, wiki_search, browser_*, get_status, ...) stay hidden to " +
      "keep context lean. Call with action='expand' to load & activate them, " +
      "'collapse' to hide again, 'status' to inspect state.",
    promptSnippet: "Load/unload Shepherd tools on demand",
    promptGuidelines: [
      "Call shepherd with action='expand' before doing any Shepherd work (task management, wiki, browser automation, skills). It reveals all Shepherd tools.",
      "Call shepherd with action='collapse' once Shepherd work is finished to keep the toolset and context lean.",
    ],
    parameters: Type.Object({
      action: Type.Optional(
        StringEnum(["expand", "collapse", "status"], {
          description: "expand(default): Shepherd 도구 활성화 / collapse: 숨김 / status: 상태 조회",
        }),
      ),
    }),
    async execute(_id, params: any, _signal, _onUpdate, ctx) {
      const action = params?.action ?? "expand";
      if (action === "collapse") {
        collapse();
        return text("Shepherd collapsed. " + toolNames.length + " tools hidden.");
      }
      if (action === "status") {
        return text(statusText());
      }
      const names = await expand();
      ctx?.ui?.notify?.("Shepherd: " + names.length + " tools active", "info");
      return text(
        "Shepherd toolset expanded — " + names.length + " tools now active:\n" +
          names.map((n) => "- " + n).join("\n") +
          "\n\nCall these tools directly. When done, call shepherd(action='collapse').",
      );
    },
  });

  // --- 자동 펼침: 사용자 입력에서 Shepherd 의도가 감지되면 미리 펼침 -------
  const BROWSER_KW = /(browser|브라우저|크롤|스크린샷|캡처|네트워크|콘솔|페이지|웹)/i;
  const TASK_KW = /(task_start|task_complete|task_error|get_history|get_status|큐|작업\s*등록|작업\s*완료|프로젝트\s*히스토리)/i;
  const WIKI_KW = /(wiki_read|wiki_list|wiki_search|wiki|위키|문서)/i;
  const SKILL_KW = /(skill_load|skill|스킬)/i;

  pi.on("input", async (event: any, ctx) => {
    if (expanded) return;
    const raw = (event?.text ?? "").toString();
    if (!raw) return;
    if (BROWSER_KW.test(raw) || TASK_KW.test(raw) || WIKI_KW.test(raw) || SKILL_KW.test(raw)) {
      try {
        const names = await expand();
        ctx?.ui?.notify?.("Shepherd auto-expanded (" + names.length + " tools)", "info");
      } catch (e: any) {
        ctx?.ui?.notify?.("Shepherd auto-expand failed: " + (e?.message ?? e), "error");
      }
    }
  });

  // --- 편의 명령어: /shepherd [expand|collapse|status] -------------------
  pi.registerCommand("shepherd", {
    description: "Expand/collapse/inspect Shepherd toolset",
    getArgumentCompletions: (prefix: string) => {
      const items = ["expand", "collapse", "status"].map((v) => ({ value: v, label: v }));
      const filtered = items.filter((i) => i.value.startsWith(prefix));
      return filtered.length > 0 ? filtered : null;
    },
    handler: async (args: string, ctx) => {
      const action = (args || "status").trim();
      try {
        if (action === "expand") {
          const names = await expand();
          ctx.ui.notify("Shepherd expanded: " + names.length + " tools", "info");
        } else if (action === "collapse") {
          collapse();
          ctx.ui.notify("Shepherd collapsed: " + toolNames.length + " tools hidden", "info");
        } else {
          ctx.ui.notify(statusText(), "info");
        }
      } catch (e: any) {
        ctx.ui.notify("Shepherd error: " + (e?.message ?? e), "error");
      }
    },
  });

  // --- 정리: 세션 종료 시 정리 -------------------------------------------
  pi.on("session_shutdown", async () => {
    // 추가 정리 작업이 필요하면 여기에 추가
  });
}
`;

	return os.WriteFile(extPath, []byte(extensionCode), 0644)
}
