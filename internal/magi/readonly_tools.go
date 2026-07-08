package magi

import (
	"strings"
)

// Phase 1.5 - Tool permission filtering for MAGI proposers.
//
// Proposers run 3-way parallel against the same filesystem / cluster /
// network. For those shared resources write-side effects would collide, so
// we restrict proposers to side-effect-free reads (reading code, listing
// K8s pods, checking OPNsense firewall rules — none of that mutates
// anything). Browser tools are the deliberate exception: ALL browser tools
// are permitted — including interaction (click/type) and JS eval — because
// each proposer runs in its own isolated browser session (per-proposer
// profile via PersonaSheepName, tasks #7138/#7139), so there is no shared
// browser state to collide on. The set of permitted tools is therefore:
//
//   - Native:     read_file, grep, glob
//   - Shepherd:   get_history, get_task_detail, get_status,
//                 skill_load,
//                 wiki_read_page, wiki_list_pages, wiki_search
//   - Browser:    ALL browser tools (navigation, interaction, session
//                 lifecycle, capture, debug) — safe because each proposer is
//                 isolated to its own session (PersonaSheepName, #7138/#7139)
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

// allowedBrowserTools are browser automation tools that are all permitted
// for MAGI proposers. Previously, interaction tools (click, type, select,
// etc.) and session lifecycle tools (start/stop) were blocked to prevent
// race conditions between three concurrent models. However, each proposer
// now runs in its own browser session (via PersonaSheepName, task #7139),
// so all browser tools are safe to use concurrently.
var allowedBrowserTools = map[string]bool{
	// Navigation & page control
	"browser_open":     true, // navigate to a URL (opens a page)
	"browser_navigate": true, // navigate current page to URL
	"browser_back":     true, // browser history back
	"browser_forward":  true, // browser history forward
	"browser_reload":   true, // reload current page
	"browser_close":    true, // close a page
	// Element interaction
	"browser_click":  true, // clicks an element
	"browser_type":   true, // types text into an input
	"browser_select": true, // selects a dropdown option
	"browser_check":  true, // checks/unchecks a checkbox
	"browser_hover":  true, // hovers over an element
	"browser_scroll": true, // scroll the page
	"browser_eval":   true, // executes JavaScript
	// Information extraction
	"browser_get_text":      true, // extract text from selector
	"browser_get_html":      true, // extract HTML from selector
	"browser_get_attribute": true, // get element attribute
	"browser_get_url":       true, // get current URL
	"browser_get_title":     true, // get page title
	// Wait / synchronization
	"browser_wait_load":     true, // wait for page load
	"browser_wait_idle":     true, // wait for network idle
	"browser_wait_selector": true, // wait for element to appear
	"browser_wait_hidden":   true, // wait for element to disappear
	// Capture
	"browser_screenshot": true, // capture screenshot
	"browser_pdf":        true, // generate PDF
	// Session lifecycle
	"browser_session_start": true, // creates a new Chrome profile session
	"browser_session_stop":  true, // destroys a session
	"browser_list_pages":    true, // list open pages
	"browser_list_sessions": true, // list active sessions
	// Debug / monitoring
	"browser_console_start":    true, // start console message collection
	"browser_console_messages": true, // read collected console messages
	"browser_network_start":    true, // start network monitoring
	"browser_network_requests": true, // list collected network requests
	"browser_network_request":  true, // get specific network request details
}

// blockedShepherdMCPTools are explicitly excluded even if a name-based
// heuristic might suggest they are reads. These mutate task state or spawn
// browser sessions.
var blockedShepherdMCPTools = map[string]bool{
	"task_start":    true,
	"task_complete": true,
	"task_error":    true,
}

// blockedBrowserTools is intentionally empty — all browser tools are allowed
// for MAGI proposers (tasks #7138/#7139). Kept as an empty map (rather than
// deleted) so the IsAllowedProposerTool block/allow check structure remains
// symmetric and re-blocking a browser tool later is a one-line change.
var blockedBrowserTools = map[string]bool{}

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
// These heuristics apply to external MCP tools only: browser tools are
// resolved earlier via the explicit allowedBrowserTools map, so patterns like
// "_start"/"_stop" never reach browser_session_start / browser_session_stop.
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

// IsAllowedProposerTool reports whether the named tool may be invoked by a
// MAGI proposer. Filesystem / cluster / network tools are limited to
// side-effect-free reads (safe for concurrent proposers); browser tools are
// all allowed because each proposer runs in its own isolated session.
func IsAllowedProposerTool(name string) bool {
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
