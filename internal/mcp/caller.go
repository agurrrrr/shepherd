// Caller-task identification for MCP tool calls (#597).
//
// A tool call can originate from a running queue task (the agent's own task).
// Knowing which task made the call lets the daemon decide, for example, that
// an issue_upsert/issue_execute issued by a shepherd (목자) task should
// auto-mark the issue as watched. Three entry paths carry the caller:
//
//   - embedded dispatch (daemon in-process): server.go's mcpDispatch injects
//     args[CallerTaskArgKey] = opts.TaskID before calling ExecuteTool.
//   - stateless `shepherd mcp` stdio child: the worker injects
//     SHEPHERD_TASK_ID into the child env (applyTaskIDEnv); handleToolsCall
//     falls back to it and injects the reserved arg key itself.
//   - daemon proxy (handleMCPProxy): the stdio forwarder moves the reserved
//     key out of args into the body's caller_task_id field; the proxy puts it
//     back into args before ExecuteTool.
//
// worker cannot import mcp (mcp imports worker), so worker/embedded.go keeps
// a duplicate of CallerTaskArgKey — change both together.
package mcp

import (
	"os"
	"strconv"
)

// CallerTaskArgKey is the reserved tool-argument key carrying the calling
// queue task's ID. Providers never see it: it is injected internally and
// stripped before any result echoes args back.
const CallerTaskArgKey = "_shepherd_caller_task_id"

// internalArgPrefix marks any other reserved argument keys. StripInternalArgs
// removes every key with this prefix as well as CallerTaskArgKey.
const internalArgPrefix = "_internal_"

// SetCallerTaskID injects the reserved key into args (creating the map when
// nil). taskID <= 0 is a no-op so ordinary runs never carry the key.
func SetCallerTaskID(args map[string]interface{}, taskID int) map[string]interface{} {
	if taskID <= 0 {
		return args
	}
	if args == nil {
		args = make(map[string]interface{}, 1)
	}
	args[CallerTaskArgKey] = taskID
	return args
}

// CallerTaskIDFromArgs returns the caller task ID carried in args, or 0.
// The key stays in args (handlers other than the caller-aware ones ignore it).
func CallerTaskIDFromArgs(args map[string]interface{}) int {
	if args == nil {
		return 0
	}
	return toInt(args[CallerTaskArgKey])
}

// StripInternalArgs removes the reserved caller key and every `_internal_`
// prefixed key from args and returns the caller task ID it found (0 when the
// args carry none). Used by the stdio forwarder so internal keys never travel
// inside user-level tool args to the daemon.
func StripInternalArgs(args map[string]interface{}) int {
	if args == nil {
		return 0
	}
	caller := toInt(args[CallerTaskArgKey])
	delete(args, CallerTaskArgKey)
	for k := range args {
		if len(k) >= len(internalArgPrefix) && k[:len(internalArgPrefix)] == internalArgPrefix {
			delete(args, k)
		}
	}
	return caller
}

// CallerTaskIDFromEnv reads SHEPHERD_TASK_ID (set by worker.applyTaskIDEnv on
// provider children, inherited by the `shepherd mcp` stdio child). Returns 0
// when unset or unparsable — the normal case for daemon/CLI processes.
func CallerTaskIDFromEnv() int {
	v := os.Getenv("SHEPHERD_TASK_ID")
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
