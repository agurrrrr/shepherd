package mcp

import (
	"strings"
	"testing"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/task"
)

func TestFilterFinishedTasks_DropsInProgress(t *testing.T) {
	all := []*ent.Task{
		{ID: 3, Status: task.StatusRunning},   // the caller's own task
		{ID: 2, Status: task.StatusPending},   // queued behind it
		{ID: 1, Status: task.StatusCompleted}, //
		{ID: 0, Status: task.StatusFailed},    //
	}

	finished, inProgress := filterFinishedTasks(all)

	if inProgress != 2 {
		t.Errorf("inProgress = %d, want 2", inProgress)
	}
	if len(finished) != 2 {
		t.Fatalf("finished = %d tasks, want 2", len(finished))
	}
	for _, ft := range finished {
		if ft.Status == task.StatusRunning || ft.Status == task.StatusPending {
			t.Errorf("task #%d with status %s should have been filtered out", ft.ID, ft.Status)
		}
	}
}

func TestFilterFinishedTasks_KeepsStopped(t *testing.T) {
	finished, inProgress := filterFinishedTasks([]*ent.Task{
		{ID: 1, Status: task.StatusStopped},
	})

	if inProgress != 0 {
		t.Errorf("inProgress = %d, want 0", inProgress)
	}
	if len(finished) != 1 {
		t.Fatalf("finished = %d tasks, want 1 (stopped tasks are history)", len(finished))
	}
}

func TestFilterFinishedTasks_PreservesOrder(t *testing.T) {
	finished, _ := filterFinishedTasks([]*ent.Task{
		{ID: 9, Status: task.StatusCompleted},
		{ID: 8, Status: task.StatusRunning},
		{ID: 7, Status: task.StatusCompleted},
	})

	if len(finished) != 2 || finished[0].ID != 9 || finished[1].ID != 7 {
		t.Errorf("finished = %v, want newest-first [9 7]", finished)
	}
}

// get_history no longer lists in-flight work, so its description has to say so
// — otherwise a model reads an empty history as "no work has ever run here".
func TestGetHistoryToolDef_DocumentsInProgressExclusion(t *testing.T) {
	var desc string
	for _, def := range ListCoreToolDefs() {
		if def.Name == "get_history" {
			desc = def.Description
			break
		}
	}
	if desc == "" {
		t.Fatal("get_history tool definition not found")
	}
	if !strings.Contains(desc, "진행 중") {
		t.Errorf("description does not mention in-progress exclusion: %q", desc)
	}
	if !strings.Contains(desc, "get_status") {
		t.Errorf("description does not point at get_status for live state: %q", desc)
	}
}

func TestGetTaskDetailToolDef_Present(t *testing.T) {
	var found bool
	for _, def := range ListCoreToolDefs() {
		if def.Name == "get_task_detail" {
			found = true
			if _, ok := def.InputSchema.Properties["task_id"]; !ok {
				t.Error("get_task_detail missing task_id property")
			}
			break
		}
	}
	if !found {
		t.Fatal("get_task_detail tool definition not found")
	}
}
