package skill

import (
	"context"
	"fmt"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/ent/skill"
	"github.com/agurrrrr/shepherd/internal/db"
)

// CreateSkill creates a new skill, optionally associated with a project.
func CreateSkill(projectID *int, name, description, content, scope string, tags []string) (*ent.Skill, error) {
	ctx := context.Background()
	client := db.Client()

	builder := client.Skill.Create().
		SetName(name).
		SetContent(content).
		SetScope(skill.Scope(scope)).
		SetEnabled(true)

	if description != "" {
		builder = builder.SetDescription(description)
	}
	if tags != nil {
		builder = builder.SetTags(tags)
	}
	if projectID != nil {
		builder = builder.SetProjectID(*projectID)
	}

	s, err := builder.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create skill: %w", err)
	}

	return GetSkill(s.ID)
}

// GetSkill returns a skill by ID with project edge.
func GetSkill(id int) (*ent.Skill, error) {
	ctx := context.Background()
	client := db.Client()

	s, err := client.Skill.Query().
		Where(skill.ID(id)).
		WithProject().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("skill #%d not found", id)
		}
		return nil, fmt.Errorf("failed to query skill: %w", err)
	}

	return s, nil
}

// UpdateSkill updates a skill.
func UpdateSkill(id int, name, description, content string, enabled bool, tags []string) (*ent.Skill, error) {
	ctx := context.Background()
	client := db.Client()

	builder := client.Skill.UpdateOneID(id).
		SetName(name).
		SetContent(content).
		SetEnabled(enabled)

	if description != "" {
		builder = builder.SetDescription(description)
	} else {
		builder = builder.ClearDescription()
	}
	if tags != nil {
		builder = builder.SetTags(tags)
	}

	_, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("skill #%d not found", id)
		}
		return nil, fmt.Errorf("failed to update skill: %w", err)
	}

	return GetSkill(id)
}

// DeleteSkill deletes a skill by ID.
func DeleteSkill(id int) error {
	ctx := context.Background()
	client := db.Client()

	err := client.Skill.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("skill #%d not found", id)
		}
		return fmt.Errorf("failed to delete skill: %w", err)
	}

	return nil
}

// ListAll returns all skills with project edges.
func ListAll() ([]*ent.Skill, error) {
	ctx := context.Background()
	client := db.Client()

	skills, err := client.Skill.Query().
		WithProject().
		Order(ent.Desc(skill.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}

	return skills, nil
}

// ListGlobal returns only global-scope skills.
func ListGlobal() ([]*ent.Skill, error) {
	ctx := context.Background()
	client := db.Client()

	skills, err := client.Skill.Query().
		Where(skill.ScopeEQ(skill.ScopeGlobal)).
		WithProject().
		Order(ent.Desc(skill.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list global skills: %w", err)
	}

	return skills, nil
}

// ListByProject returns skills for the given project.
func ListByProject(projectName string) ([]*ent.Skill, error) {
	ctx := context.Background()
	client := db.Client()

	skills, err := client.Skill.Query().
		Where(skill.HasProjectWith(project.Name(projectName))).
		WithProject().
		Order(ent.Desc(skill.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list project skills: %w", err)
	}

	return skills, nil
}

// ToggleEnabled toggles the enabled state of a skill.
func ToggleEnabled(id int, enabled bool) (*ent.Skill, error) {
	ctx := context.Background()
	client := db.Client()

	_, err := client.Skill.UpdateOneID(id).
		SetEnabled(enabled).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("skill #%d not found", id)
		}
		return nil, fmt.Errorf("failed to toggle skill: %w", err)
	}

	return GetSkill(id)
}

// GetEnabledSkillsForProject returns all enabled skills relevant to a project:
// all enabled global skills + all enabled project-specific skills.
func GetEnabledSkillsForProject(projectName string) ([]*ent.Skill, error) {
	ctx := context.Background()
	client := db.Client()

	// Enabled global skills
	globalSkills, err := client.Skill.Query().
		Where(
			skill.ScopeEQ(skill.ScopeGlobal),
			skill.EnabledEQ(true),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query global skills: %w", err)
	}

	// Enabled project-specific skills
	projectSkills, err := client.Skill.Query().
		Where(
			skill.HasProjectWith(project.Name(projectName)),
			skill.EnabledEQ(true),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query project skills: %w", err)
	}

	return append(globalSkills, projectSkills...), nil
}
