package magi

import (
	"strings"
)

// Phase 1.5 - Read-only tool filtering for MAGI proposers.
//
// Proposers run 3-way parallel against the same filesystem / cluster /
// network. Write-side effects would collide. But reading code, listing
// K8s pods, checking OPNsense firewall rules - none of that mutates
// anything. So we give proposers every tool whose execution is free of
// persistent side effects:
//
//   - Native:     read_file, grep, glob
//   - Shepherd:   get_history, get_task_detail, get_status,
//                 skill_load,
//                 wiki_read_page, wiki_list_pages, wiki_search
//   - Browser:    navigation + reading tools (open, navigate, get_text,
//                 screenshot, etc.) — enables web research without DOM
//                 mutation. Interaction tools (click, type, select) and
//                 lifecycle tools (session start/stop) remain blocked.
//   - External:   any project-enabled MCP server method whose name
//                 matches a readonly heuristic

// allowedNativeTools are the core coding tools with no side effects.
var allowedNativeTools = map[string]bool{
	"read_file": true,
	"grep":      true,
	"glob":      true,
}

// allowedShepherdMCPTools are the built-in shepherd MCP methods that only
// query or read data - no mutations, no side effects.
var allowedShepherdMCPTools = map[string]bool{
	"get_history":     true,
	"get_task_detail": true,
	"get_status":      true,
	"skill_load":      true,
	"wiki_read_page":  true,
	"wiki_list_pages": true,
	"wiki_search":     true,
}

// allowedBrowserTools are browser automation tools that only navigate or read
// page state — no DOM mutation, no side effects on the page being viewed.
// These enable web research for MAGI proposers without allowing interaction
// that could race between three concurrent models.
var allowedBrowserTools = map[string]bool{
	"browser_open":            true, // navigate to a URL (opens a page)
	"browser_navigate":        true, // navigate current page to URL
	"browser_back":            true, // browser history back
	"browser_forward":         true, // browser history forward
	"browser_reload":           true, // reload current page
	"browser_get_text":        true, // extract text from selector
	"browser_get_html":        true, // extract HTML from selector
	"browser_get_attribute":   true, // get element attribute
	"browser_get_url":         true, // get current URL
	"browser_get_title":       true, // get page title
	"browser_screenshot":      true, // capture screenshot
	"browser_pdf":             true, // generate PDF
	"browser_scroll":          true, // scroll the page (no DOM mutation)
	"browser_wait_load":       true, // wait for page load
	"browser_wait_idle":       true, // wait for network idle
	"browser_wait_selector":   true, // wait for element to appear
	"browser_wait_hidden":     true, // wait for element to disappear
	"browser_list_pages":      true, // list open pages
	"browser_list_sessions":   true, // list active sessions
	"browser_console_start":   true, // start console message collection
	"browser_console_messages": true, // read collected console messages
	"browser_network_start":   true, // start network monitoring
	"browser_network_requests": true, // list collected network requests
	"browser_network_request": true, // get specific network request details
}

// blockedShepherdMCPTools are explicitly excluded even if a name-based
// heuristic might suggest they are reads. These mutate task state or spawn
// browser sessions.
var blockedShepherdMCPTools = map[string]bool{
	"task_start":    true,
	"task_complete": true,
	"task_error":    true,
}

// blockedBrowserTools are browser automation tools that mutate DOM state or
// manage session lifecycle — three concurrent models would race on these.
var blockedBrowserTools = map[string]bool{
	"browser_session_start": true, // creates a new Chrome profile session
	"browser_session_stop":  true, // destroys a session
	"browser_close":         true, // closes a page
	"browser_click":        true, // clicks an element (DOM mutation)
	"browser_type":         true, // types text into an input (DOM mutation)
	"browser_select":       true, // selects a dropdown option (DOM mutation)
	"browser_check":        true, // checks/unchecks a checkbox (DOM mutation)
	"browser_hover":        true, // hovers over an element (may trigger JS)
	"browser_eval":         true, // executes arbitrary JS (can mutate DOM)
}

// readonlyKeywordPatterns are substrings that strongly suggest a tool is a
// read/query operation (no side effects). Used for external MCP server tools
// whose exact semantics we don't control.
var readonlyKeywordPatterns = []string{
	"list_",
	"get_",
	"read_",
	"status",
	"info",
	"query",
	"search",
	"_stats",
	"_statistic",
	"_config",
	"_leases",
}

// mutatingKeywordPatterns are substrings that strongly suggest a tool mutates
// state. Negative wins ties - if both patterns match, the tool is excluded.
var mutatingKeywordPatterns = []string{
	"_add_",
	"_delete_",
	"_update_",
	"_create_",
	"_remove_",
	"_toggle_",
	"_apply_",
	"_start",
	"_stop",
	"_restart",
	"_reboot",
	"_shutdown",
	"_scale_",
	"_deploy_",
	"_promote_",
}

// IsReadOnlyTool reports whether the named tool has no persistent side
// effects and is safe for concurrent use by multiple MAGI proposers.
func IsReadOnlyTool(name string) bool {
	if blockedShepherdMCPTools[name] {
		return false
	}
	if blockedBrowserTools[name] {
		return false
	}
	if allowedNativeTools[name] || allowedShepherdMCPTools[name] || allowedBrowserTools[name] {
		return true
	}

	lower := strings.ToLower(name)

	for _, p := range mutatingKeywordPatterns {
		if strings.Contains(lower, p) {
			return false
		}
	}
	for _, p := range readonlyKeywordPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
