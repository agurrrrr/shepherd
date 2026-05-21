package wiki

import (
	"fmt"
	"os"

	"github.com/agurrrrr/shepherd/ent/wikipage"
)

// InitPage defines a default wiki page template.
type InitPage struct {
	Slug         string
	Title        string
	Category     wikipage.Category
	TemplateName string
}

// defaultPages defines the initial wiki pages created for a new project.
// Each page references a template name; content is resolved at init time
// to allow per-project customization.
var defaultPages = []InitPage{
	{
		Slug:         "architecture",
		Title:        "Architecture",
		Category:     wikipage.CategoryArchitecture,
		TemplateName: "architecture",
	},
	{
		Slug:         "patterns",
		Title:        "Code Patterns",
		Category:     wikipage.CategoryPatterns,
		TemplateName: "patterns",
	},
	{
		Slug:         "troubleshooting",
		Title:        "Troubleshooting",
		Category:     wikipage.CategoryTroubleshooting,
		TemplateName: "troubleshooting",
	},
	{
		Slug:         "lessons_learned",
		Title:        "Lessons Learned",
		Category:     wikipage.CategoryLessons,
		TemplateName: "lessons_learned",
	},
}

// InitializeWiki creates default wiki pages for a project.
// Pages that already exist are skipped without error.
// Templates are resolved per-page, allowing project-level or global overrides.
func InitializeWiki(projectName string) error {
	if projectName == "" {
		return fmt.Errorf("project name is required")
	}

	// Create wiki directory
	wikiDir := WikiDir(projectName)
	if err := os.MkdirAll(wikiDir, 0755); err != nil {
		return fmt.Errorf("failed to create wiki directory: %w", err)
	}

	for _, page := range defaultPages {
		existing, err := findPageByProjectAndSlug(projectName, page.Slug)
		if err != nil {
			continue
		}
		if existing != nil {
			continue // Skip existing pages
		}

		template := ResolveTemplate(projectName, page.TemplateName)
		content := ProcessTemplate(template, NewTemplateData(page.Title, page.Slug))

		_, err = CreatePage(projectName, page.Slug, page.Title, string(page.Category), content, nil)
		if err != nil {
			// Log but don't fail on individual page creation errors
			continue
		}
	}

	// Generate index
	_ = GenerateIndex(projectName)

	return nil
}

// WikiInitialized checks whether a project has default wiki pages.
func WikiInitialized(projectName string) bool {
	pages, err := ListPages(projectName)
	if err != nil {
		return false
	}
	return len(pages) > 0
}

// InitializeWikiFromFileSystem initializes wiki pages from existing .md files
// in the project's wiki directory, without creating default templates.
func InitializeWikiFromFileSystem(projectName string) error {
	if projectName == "" {
		return fmt.Errorf("project name is required")
	}

	wikiDir := WikiDir(projectName)
	if _, err := os.Stat(wikiDir); os.IsNotExist(err) {
		return nil
	}

	// Sync existing files from disk to DB
	if err := SyncFromDisk(projectName); err != nil {
		return fmt.Errorf("failed to sync wiki from disk: %w", err)
	}

	_ = GenerateIndex(projectName)
	return nil
}
