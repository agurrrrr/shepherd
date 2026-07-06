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
//   - External:   any project-enabled MCP server method whose name
//                 matches a readonly heuristic
//
// Browser session actions ARE excluded because three models sharing one
// Chrome profile would race on navigation/clicks.

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

// blockedShepherdMCPTools are explicitly excluded even if a name-based
// heuristic might suggest they are reads. These mutate task state or spawn
// browser sessions.
var blockedShepherdMCPTools = map[string]bool{
	"task_start":    true,
	"task_complete": true,
	"task_error":    true,
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
	if allowedNativeTools[name] || allowedShepherdMCPTools[name] {
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
