package manager

import (
	"fmt"

	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// The natural-language router that used to live here (Analyze / ClassifyIntent
// and their Claude/OpenCode CLI helpers) was removed with POST /api/command in
// issue #598. The shepherd (목자) itself now receives user instructions through
// POST /api/shepherd/tasks and decides what to do with the manager agent — a
// single LLM run with full MCP tools replaces the old one-shot intent
// classifier. See EnqueueShepherdTask in tasks.go.

// AutoAssign automatically assigns the best available sheep for a project.
// Returns the sheep name. If the project already has an assigned sheep, returns that.
func AutoAssign(projectName string) (string, error) {
	// Look up the project
	proj, err := project.Get(projectName)
	if err != nil {
		return "", err
	}

	// Return already-assigned sheep
	assignedSheep, _ := proj.Edges.SheepOrErr()
	if assignedSheep != nil {
		return assignedSheep.Name, nil
	}

	// Find an idle sheep
	sheepList, err := worker.List()
	if err != nil {
		return "", err
	}

	for _, s := range sheepList {
		if s.Status == sheep.StatusIdle {
			// Sheep not assigned to another project
			proj, _ := s.Edges.ProjectOrErr()
			if proj == nil {
				// Assign this sheep to the project
				if err := project.AssignSheep(projectName, s.Name); err != nil {
					return "", err
				}
				return s.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no available sheep (all working or assigned to other projects)")
}
