package queue

import "sync"

// taskModelOverrides is an in-memory taskID → model string map used to forward
// the per-request model override from the HTTP handler to the worker without
// persisting it on the task row. Cleared after the task finishes.
var taskModelOverrides sync.Map

// SetTaskModel marks a task with an explicit model preference.
func SetTaskModel(taskID int, model string) {
	taskModelOverrides.Store(taskID, model)
}

// GetTaskModel returns the per-task model override. Returns empty string when
// no override exists, meaning the worker should fall back to the global config.
func GetTaskModel(taskID int) string {
	if v, ok := taskModelOverrides.Load(taskID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ClearTaskModel removes the per-task override (call after task completion
// to avoid unbounded growth across long-running daemons).
func ClearTaskModel(taskID int) {
	taskModelOverrides.Delete(taskID)
}
