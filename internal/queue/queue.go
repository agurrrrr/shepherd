package queue

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

// CreateTask creates a new task with pending status.
func CreateTask(prompt string, sheepID, projectID int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Create().
		SetPrompt(prompt).
		SetStatus(task.StatusPending).
		SetSheepID(sheepID).
		SetProjectID(projectID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	return t, nil
}

// CreateManagerTask creates a task for the manager (shepherd commands).
// Manager tasks don't have a project association.
func CreateManagerTask(prompt string, sheepID int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Create().
		SetPrompt(prompt).
		SetStatus(task.StatusPending).
		SetSheepID(sheepID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	return t, nil
}

// GetTask returns a task by ID.
func GetTask(id int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(task.ID(id)).
		WithSheep().
		WithProject().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("task #%d not found", id)
		}
		return nil, fmt.Errorf("failed to query task: %w", err)
	}

	return t, nil
}

// ListTasks returns all tasks ordered by creation time (newest first).
func ListTasks(limit int) ([]*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	query := client.Task.Query().
		WithSheep().
		WithProject().
		Order(ent.Desc(task.FieldCreatedAt))

	if limit > 0 {
		query = query.Limit(limit)
	}

	tasks, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// ListTasksByStatus returns tasks with the given status.
func ListTasksByStatus(status task.Status) ([]*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		Where(task.StatusEQ(status)).
		WithSheep().
		WithProject().
		Order(ent.Asc(task.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// ListTasksBySheep returns tasks for the given sheep.
func ListTasksBySheep(sheepName string) ([]*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		WithSheep().
		WithProject().
		Where(task.HasSheepWith(sheep.Name(sheepName))).
		Order(ent.Desc(task.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// ListTasksByProject returns tasks for the given project.
func ListTasksByProject(projectName string) ([]*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		WithSheep().
		WithProject().
		Where(task.HasProjectWith(project.Name(projectName))).
		Order(ent.Desc(task.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// GetPendingTask returns the oldest pending task (FIFO).
func GetPendingTask() (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(task.StatusEQ(task.StatusPending)).
		WithSheep().
		WithProject().
		Order(ent.Asc(task.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // No pending tasks
		}
		return nil, fmt.Errorf("failed to query task: %w", err)
	}

	return t, nil
}

// GetPendingTaskBySheep returns the oldest pending task for the given sheep (FIFO).
func GetPendingTaskBySheep(sheepID int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(
			task.StatusEQ(task.StatusPending),
			task.HasSheepWith(sheep.ID(sheepID)),
		).
		WithSheep().
		WithProject().
		Order(ent.Asc(task.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // No pending tasks
		}
		return nil, fmt.Errorf("failed to query task: %w", err)
	}

	return t, nil
}

// CountPendingTasksBySheep returns the number of pending tasks for the given sheep.
func CountPendingTasksBySheep(sheepID int) (int, error) {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Task.Query().
		Where(
			task.StatusEQ(task.StatusPending),
			task.HasSheepWith(sheep.ID(sheepID)),
		).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count tasks: %w", err)
	}

	return count, nil
}

// StartTask marks a task as running.
func StartTask(id int) error {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Task.Update().
		Where(task.ID(id)).
		SetStatus(task.StatusRunning).
		SetStartedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}

	return nil
}

// RequeueTask moves a running task back to pending status (e.g., on rate limit).
func RequeueTask(id int, output []string) error {
	ctx := context.Background()
	client := db.Client()

	updateQuery := client.Task.Update().
		Where(task.ID(id)).
		SetStatus(task.StatusPending).
		ClearStartedAt()

	if len(output) > 0 {
		updateQuery = updateQuery.SetOutput(output)
	}

	count, err := updateQuery.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to requeue task: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}

	return nil
}

// CompleteTask marks a task as completed with summary and modified files.
func CompleteTask(id int, summary string, filesModified []string) error {
	return CompleteTaskWithOutput(id, summary, filesModified, nil)
}

// CompleteTaskWithOutput marks a task as completed with summary, modified files, and output.
func CompleteTaskWithOutput(id int, summary string, filesModified []string, output []string) error {
	ctx := context.Background()
	client := db.Client()

	updateQuery := client.Task.Update().
		Where(task.ID(id)).
		SetStatus(task.StatusCompleted).
		SetSummary(summary).
		SetFilesModified(filesModified).
		SetCompletedAt(time.Now())

	if len(output) > 0 {
		updateQuery = updateQuery.SetOutput(output)
	}

	count, err := updateQuery.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}

	return nil
}

// FailTask marks a task as failed with an error message.
func FailTask(id int, errMsg string) error {
	return FailTaskWithOutput(id, errMsg, nil)
}

// FailTaskWithOutput marks a task as failed with an error message and output.
func FailTaskWithOutput(id int, errMsg string, output []string) error {
	ctx := context.Background()
	client := db.Client()

	updateQuery := client.Task.Update().
		Where(task.ID(id)).
		SetStatus(task.StatusFailed).
		SetError(errMsg).
		SetCompletedAt(time.Now())

	if len(output) > 0 {
		updateQuery = updateQuery.SetOutput(output)
		// Save the last few lines of output as summary (viewable even on interruption)
		summary := buildSummaryFromOutput(output)
		if summary != "" {
			updateQuery = updateQuery.SetSummary(summary)
		}
	}

	count, err := updateQuery.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to mark task as failed: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}

	return nil
}

// buildSummaryFromOutput builds a summary from output lines (last meaningful lines).
func buildSummaryFromOutput(output []string) string {
	// Collect last meaningful lines (excluding empty lines)
	var meaningful []string
	for _, line := range output {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && trimmed != "✅ Task complete" && trimmed != "🔧 Running tool..." && trimmed != "✅ Tool complete" {
			meaningful = append(meaningful, trimmed)
		}
	}
	if len(meaningful) == 0 {
		return ""
	}
	// Last 5 lines only
	start := len(meaningful) - 5
	if start < 0 {
		start = 0
	}
	return strings.Join(meaningful[start:], "\n")
}

// CountByStatus returns task counts by status.
func CountByStatus() (map[task.Status]int, error) {
	ctx := context.Background()
	client := db.Client()

	result := make(map[task.Status]int)

	for _, status := range []task.Status{
		task.StatusPending,
		task.StatusRunning,
		task.StatusCompleted,
		task.StatusFailed,
	} {
		count, err := client.Task.Query().
			Where(task.StatusEQ(status)).
			Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to count tasks: %w", err)
		}
		result[status] = count
	}

	return result, nil
}

// StatusToKorean converts task status to a display string.
func StatusToKorean(status task.Status) string {
	switch status {
	case task.StatusPending:
		return "pending"
	case task.StatusRunning:
		return "running"
	case task.StatusCompleted:
		return "completed"
	case task.StatusFailed:
		return "failed"
	default:
		return string(status)
	}
}

// RecoverStuckTasks marks running tasks as failed after abnormal termination.
// This should be called on startup to clean up after abnormal termination.
func RecoverStuckTasks() (int, error) {
	ctx := context.Background()
	client := db.Client()

	// Change tasks in running status to failed
	count, err := client.Task.Update().
		Where(task.StatusEQ(task.StatusRunning)).
		SetStatus(task.StatusFailed).
		SetError("interrupted due to abnormal termination").
		SetCompletedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to recover task status: %w", err)
	}

	return count, nil
}
