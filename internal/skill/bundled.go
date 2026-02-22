package skill

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/skill"
	"github.com/agurrrrr/shepherd/internal/db"
	"gopkg.in/yaml.v3"
)

//go:embed bundled/*.md
var bundledFS embed.FS

// SkillFrontmatter represents the YAML frontmatter of a skill file.
type SkillFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
	Scope       string   `yaml:"scope"`
}

// ParseSkillFile parses a markdown file with YAML frontmatter.
// Returns frontmatter, body content, and error.
func ParseSkillFile(content string) (*SkillFrontmatter, string, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return nil, content, nil
	}

	// Find closing ---
	rest := content[3:]
	idx := strings.Index(rest, "---")
	if idx < 0 {
		return nil, content, nil
	}

	fmRaw := strings.TrimSpace(rest[:idx])
	body := strings.TrimSpace(rest[idx+3:])

	var fm SkillFrontmatter
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return nil, content, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	return &fm, body, nil
}

// ExportSkillToMarkdown converts a skill entity to markdown with YAML frontmatter.
func ExportSkillToMarkdown(sk *ent.Skill) string {
	var sb strings.Builder

	fm := SkillFrontmatter{
		Name:        sk.Name,
		Description: sk.Description,
		Tags:        sk.Tags,
		Scope:       string(sk.Scope),
	}

	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		// Fallback: just return content
		return sk.Content
	}

	sb.WriteString("---\n")
	sb.Write(fmBytes)
	sb.WriteString("---\n\n")
	sb.WriteString(sk.Content)

	return sb.String()
}

// ImportSkillFromMarkdown parses a markdown file and creates a skill in DB.
func ImportSkillFromMarkdown(content string, projectID *int) (*ent.Skill, error) {
	fm, body, err := ParseSkillFile(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse skill file: %w", err)
	}

	name := "imported-skill"
	description := ""
	scope := "project"
	var tags []string

	if fm != nil {
		if fm.Name != "" {
			name = fm.Name
		}
		description = fm.Description
		if fm.Scope == "global" {
			scope = "global"
		}
		tags = fm.Tags
	}

	if body == "" {
		body = content
	}

	return CreateSkill(projectID, name, description, body, scope, tags)
}

// SeedBundledSkills loads bundled skill files and inserts them into DB if not present.
// Idempotent: checks by name + bundled=true to avoid duplicates.
// Updates existing bundled skills if content has changed.
func SeedBundledSkills() error {
	ctx := context.Background()
	client := db.Client()

	entries, err := bundledFS.ReadDir("bundled")
	if err != nil {
		return fmt.Errorf("failed to read bundled skills dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		data, err := bundledFS.ReadFile("bundled/" + entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read bundled skill %s: %w", entry.Name(), err)
		}

		fm, body, err := ParseSkillFile(string(data))
		if err != nil || fm == nil || fm.Name == "" {
			continue
		}

		scope := skill.ScopeGlobal
		if fm.Scope == "project" {
			scope = skill.ScopeProject
		}

		// Check if this bundled skill already exists
		existing, err := client.Skill.Query().
			Where(
				skill.Name(fm.Name),
				skill.BundledEQ(true),
			).
			Only(ctx)

		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("failed to query existing bundled skill %s: %w", fm.Name, err)
		}

		if existing != nil {
			// Update content if changed
			if existing.Content != body || existing.Description != fm.Description {
				_, err = client.Skill.UpdateOneID(existing.ID).
					SetContent(body).
					SetDescription(fm.Description).
					SetTags(fm.Tags).
					Save(ctx)
				if err != nil {
					return fmt.Errorf("failed to update bundled skill %s: %w", fm.Name, err)
				}
			}
			continue
		}

		// Create new bundled skill
		builder := client.Skill.Create().
			SetName(fm.Name).
			SetContent(body).
			SetScope(scope).
			SetBundled(true).
			SetEnabled(true)

		if fm.Description != "" {
			builder = builder.SetDescription(fm.Description)
		}
		if fm.Tags != nil {
			builder = builder.SetTags(fm.Tags)
		}

		if _, err := builder.Save(ctx); err != nil {
			return fmt.Errorf("failed to seed bundled skill %s: %w", fm.Name, err)
		}
	}

	return nil
}
