package queue

import (
	"sync"

	"github.com/agurrrrr/shepherd/internal/config"
)

// taskThinkingFlags is an in-memory taskID → bool map used to forward the
// per-request "thinking" toggle from the HTTP handler to the worker without
// persisting it on the task row. Cleared after the task finishes.
var taskThinkingFlags sync.Map

// SetTaskThinking marks a task with an explicit thinking-mode preference.
// Only the explicit value is stored; absence falls back to the global default
// in GetTaskThinking.
func SetTaskThinking(taskID int, enabled bool) {
	taskThinkingFlags.Store(taskID, enabled)
}

// GetTaskThinking returns the effective thinking-mode flag for a task. Falls
// back to opencode_thinking_default config when no per-task override exists.
func GetTaskThinking(taskID int) bool {
	if v, ok := taskThinkingFlags.Load(taskID); ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return config.GetBool("opencode_thinking_default")
}

// ClearTaskThinking removes the per-task override (call after task completion
// to avoid unbounded growth across long-running daemons).
func ClearTaskThinking(taskID int) {
	taskThinkingFlags.Delete(taskID)
}
