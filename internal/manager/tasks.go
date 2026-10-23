package manager

import (
	"fmt"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// Shepherd task kinds (#594). Stored on tasks.shepherd_kind; empty means the
// task is not a shepherd task.
const (
	KindCommand = "command" // user instruction typed into the shepherd console
	KindReview  = "review"  // delegated-task review (stage #597)
	KindPatrol  = "patrol"  // periodic patrol (stage #600)
)

// ValidKind reports whether kind is one of the shepherd task kinds.
func ValidKind(kind string) bool {
	switch kind {
	case KindCommand, KindReview, KindPatrol:
		return true
	}
	return false
}

// EnqueueShepherdTask creates a pending shepherd (목자) task on the manager
// sheep (#594). The task has no project association; the queue dispatcher
// runs it in the configured workspace_path.
//
// priority: user commands pass 1 so a queued command runs ahead of the
// automatic review/patrol tasks (priority 0). kind must be one of
// KindCommand/KindReview/KindPatrol.
//
// When shepherd_model is configured it is persisted as the task's per-task
// model override (queue.SetTaskModel), so the run — claude included — picks
// it up through opts.Model at execution time instead of the provider default.
func EnqueueShepherdTask(kind, prompt string, priority int) (*ent.Task, error) {
	if !ValidKind(kind) {
		return nil, fmt.Errorf("invalid shepherd kind: %q", kind)
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	mgr, err := worker.GetOrCreateManager()
	if err != nil {
		return nil, fmt.Errorf("manager sheep not available: %w", err)
	}

	t, err := queue.CreateManagerTaskWithKind(prompt, mgr.ID, priority, kind)
	if err != nil {
		return nil, err
	}

	if model := strings.TrimSpace(config.GetString("shepherd_model")); model != "" {
		queue.SetTaskModel(t.ID, model)
	}

	return t, nil
}
