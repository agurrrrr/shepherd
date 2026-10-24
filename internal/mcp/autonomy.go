// Shepherd autonomy gate for MCP tool calls (#601).
//
// All tier logic lives in internal/manager (autonomy.go): the tier table, the
// normalization, and both caps. This file only wires it to the MCP call sites.
// Permission checks apply ONLY when the caller is identifiable as a shepherd
// (manager-sheep) task. The caller id arrives through CallerTaskArgKey (set by
// the embedded dispatcher, the daemon proxy, or handleToolsCall's env
// fallback). For the stateless `shepherd mcp` stdio child that runs tools
// in-process, SHEPHERD_TASK_ID sits in the child's own env — callerTaskIDOrEnv
// merges both sources so a child-local gate never misses the identity.
//
// A refusal is returned as a regular tool error whose text names the tier and
// points at request_user_action, so the shepherd has a next move instead of a
// dead end (issue #601 §2).
package mcp

import (
	"github.com/agurrrrr/shepherd/internal/manager"
)

// callerTaskIDOrEnv resolves the caller identity from args, falling back to
// SHEPHERD_TASK_ID for in-process handlers running inside a provider's
// `shepherd mcp` child (handleToolsCall already injects the key, but tool
// handlers may also be called directly from tests/CLI where only env exists).
func callerTaskIDOrEnv(args map[string]interface{}) int {
	if id := CallerTaskIDFromArgs(args); id > 0 {
		return id
	}
	return CallerTaskIDFromEnv()
}

// checkShepherdPermission is the single gate every restricted MCP tool calls
// before doing its work. It refuses only shepherd callers below the tool's
// tier; ordinary sheep, users, and unidentified callers pass through.
func checkShepherdPermission(tool string, args map[string]interface{}) error {
	return manager.CheckShepherdToolPermission(tool, callerTaskIDOrEnv(args))
}

// checkShepherdExecuteCaps applies the delegation-count and daily-cost caps
// to a shepherd's issue_execute. Non-shepherd and unidentified callers pass.
func checkShepherdExecuteCaps(args map[string]interface{}) error {
	return manager.CheckShepherdExecuteLimits(callerTaskIDOrEnv(args))
}
