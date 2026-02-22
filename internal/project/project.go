package project

import (
	"context"
	"fmt"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
)

// AddResult holds the result of adding a project.
type AddResult struct {
	Project       *ent.Project
	AssignedSheep *ent.Sheep
	SheepCreated  bool
	AssignError   error
}

// Add creates a new project and automatically assigns an available sheep.
// If no sheep is available, a new one will be created.
func Add(name, path, description string) (*ent.Project, error) {
	result := AddWithResult(name, path, description)
	return result.Project, nil
}

// AddWithResult creates a new project and returns detailed result including sheep assignment info.
func AddWithResult(name, path, description string) *AddResult {
	ctx := context.Background()
	client := db.Client()
	result := &AddResult{}

	// Check if already exists
	exists, err := client.Project.Query().
		Where(entProject.Name(name)).
		Exist(ctx)
	if err != nil {
		result.AssignError = fmt.Errorf("failed to query project: %w", err)
		return result
	}
	if exists {
		result.AssignError = fmt.Errorf("project '%s' already exists", name)
		return result
	}

	// Create project
	builder := client.Project.Create().
		SetName(name).
		SetPath(path)

	if description != "" {
		builder.SetDescription(description)
	}

	p, err := builder.Save(ctx)
	if err != nil {
		result.AssignError = fmt.Errorf("failed to create project: %w", err)
		return result
	}
	result.Project = p

	// Auto-assign sheep
	assignedSheep, created, err := autoAssignSheep(ctx, client, p)
	if err != nil {
		result.AssignError = err
	} else {
		result.AssignedSheep = assignedSheep
		result.SheepCreated = created
	}

	return result
}

// autoAssignSheep finds an available sheep or creates a new one and assigns it to the project.
// Returns (sheep, created, error) where created is true if a new sheep was created.
func autoAssignSheep(ctx context.Context, client *ent.Client, p *ent.Project) (*ent.Sheep, bool, error) {
	// 1. Find unassigned sheep (excluding manager)
	idleSheep, err := client.Sheep.Query().
		Where(
			sheep.NameNEQ(names.ManagerName),
			sheep.Not(sheep.HasProject()),
		).
		First(ctx)

	if err == nil {
		// Found unassigned sheep - assign it
		_, err = p.Update().SetSheep(idleSheep).Save(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("failed to assign sheep: %w", err)
		}
		return idleSheep, false, nil
	}

	// 2. No unassigned sheep available - create new one
	usedNames, err := getUsedSheepNames(ctx, client)
	if err != nil {
		return nil, false, err
	}

	newName := names.GetNext(usedNames)
	if newName == "" {
		return nil, false, fmt.Errorf("no available sheep names")
	}

	newSheep, err := client.Sheep.Create().
		SetName(newName).
		SetStatus(sheep.StatusIdle).
		Save(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create sheep: %w", err)
	}

	// Assign new sheep to project
	_, err = p.Update().SetSheep(newSheep).Save(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to assign sheep: %w", err)
	}

	return newSheep, true, nil
}

// getUsedSheepNames returns all used sheep names.
func getUsedSheepNames(ctx context.Context, client *ent.Client) ([]string, error) {
	sheepList, err := client.Sheep.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	var usedNames []string
	for _, s := range sheepList {
		usedNames = append(usedNames, s.Name)
	}
	return usedNames, nil
}

// Remove deletes a project by name.
// If the project has an assigned sheep, the sheep is also deleted.
func Remove(name string) error {
	ctx := context.Background()
	client := db.Client()

	p, err := client.Project.Query().
		Where(entProject.Name(name)).
		WithSheep().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("project '%s' not found", name)
		}
		return fmt.Errorf("failed to query project: %w", err)
	}

	// Delete assigned sheep first if exists
	if p.Edges.Sheep != nil {
		if err := client.Sheep.DeleteOne(p.Edges.Sheep).Exec(ctx); err != nil {
			return fmt.Errorf("failed to delete assigned sheep: %w", err)
		}
	}

	if err := client.Project.DeleteOne(p).Exec(ctx); err != nil {
		return fmt.Errorf("failed to delete project: %w", err)
	}

	return nil
}

// List returns all projects.
func List() ([]*ent.Project, error) {
	ctx := context.Background()
	client := db.Client()

	projects, err := client.Project.Query().
		WithSheep().
		Order(ent.Asc(entProject.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	return projects, nil
}

// Get returns a project by name.
func Get(name string) (*ent.Project, error) {
	ctx := context.Background()
	client := db.Client()

	p, err := client.Project.Query().
		Where(entProject.Name(name)).
		WithSheep().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("project '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to query project: %w", err)
	}

	return p, nil
}

// AssignSheep assigns a sheep to a project.
// If the sheep is already assigned to another project, returns an error.
func AssignSheep(projectName, sheepName string) error {
	ctx := context.Background()
	client := db.Client()

	p, err := client.Project.Query().
		Where(entProject.Name(projectName)).
		WithSheep().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("project '%s' not found", projectName)
		}
		return fmt.Errorf("failed to query project: %w", err)
	}

	// Already assigned with same sheep - success
	if p.Edges.Sheep != nil && p.Edges.Sheep.Name == sheepName {
		return nil
	}

	s, err := client.Sheep.Query().
		Where(sheep.Name(sheepName)).
		WithProject().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("sheep '%s' not found", sheepName)
		}
		return fmt.Errorf("failed to query sheep: %w", err)
	}

	// Sheep already assigned to another project
	if s.Edges.Project != nil && s.Edges.Project.ID != p.ID {
		return fmt.Errorf("sheep '%s' already assigned to project '%s'", sheepName, s.Edges.Project.Name)
	}

	_, err = p.Update().SetSheep(s).Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to assign sheep: %w", err)
	}

	return nil
}

// UnassignSheep removes the sheep assignment from a project.
func UnassignSheep(projectName string) error {
	ctx := context.Background()
	client := db.Client()

	p, err := client.Project.Query().
		Where(entProject.Name(projectName)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("project '%s' not found", projectName)
		}
		return fmt.Errorf("failed to query project: %w", err)
	}

	_, err = p.Update().ClearSheep().Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to unassign sheep: %w", err)
	}

	return nil
}

// Count returns the number of projects.
func Count() (int, error) {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Project.Query().Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count projects: %w", err)
	}

	return count, nil
}
