package mcp

import (
	"fmt"
	"strings"

	"github.com/agurrrrr/shepherd/internal/wiki"
)

func (s *Server) registerWikiTools() {
	s.tools["wiki_read_page"] = handleWikiReadPage
	s.tools["wiki_list_pages"] = handleWikiListPages
	s.tools["wiki_search"] = handleWikiSearch
}

func getWikiToolsList() []Tool {
	return []Tool{
		{
			Name:        "wiki_read_page",
			Description: "Read a wiki page by slug. Returns the page content in markdown format.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
					"slug":         {Type: "string", Description: "Wiki page slug"},
				},
				Required: []string{"project_name", "slug"},
			},
		},
		{
			Name:        "wiki_list_pages",
			Description: "List all wiki pages for a project. Returns slug, title, category, and tags for each page.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
				},
				Required: []string{"project_name"},
			},
		},
		{
			Name:        "wiki_search",
			Description: "Search wiki pages by query. Returns matching pages with slug, title, and content preview.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
					"query":        {Type: "string", Description: "Search query"},
				},
				Required: []string{"project_name", "query"},
			},
		},
	}
}

func handleWikiReadPage(args map[string]interface{}) (string, error) {
	projectName, _ := args["project_name"].(string)
	slug, _ := args["slug"].(string)

	if projectName == "" || slug == "" {
		return "", fmt.Errorf("project_name and slug are required")
	}

	page, err := wiki.GetPage(projectName, slug)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n", page.Title))
	sb.WriteString(fmt.Sprintf("Slug: %s\nCategory: %s\n", page.Slug, page.Category))
	if len(page.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(page.Tags, ", ")))
	}
	sb.WriteString("\n")
	sb.WriteString(page.Content)

	return sb.String(), nil
}

func handleWikiListPages(args map[string]interface{}) (string, error) {
	projectName, _ := args["project_name"].(string)

	if projectName == "" {
		return "", fmt.Errorf("project_name is required")
	}

	pages, err := wiki.ListPages(projectName)
	if err != nil {
		return "", err
	}

	if len(pages) == 0 {
		return fmt.Sprintf("No wiki pages found for project %q", projectName), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Wiki pages for %q (%d pages):\n\n", projectName, len(pages)))

	for _, p := range pages {
		sb.WriteString(fmt.Sprintf("- **%s** (%s)\n", p.Title, p.Slug))
		sb.WriteString(fmt.Sprintf("  Category: %s", p.Category))
		if len(p.Tags) > 0 {
			sb.WriteString(fmt.Sprintf(", Tags: %s", strings.Join(p.Tags, ", ")))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func handleWikiSearch(args map[string]interface{}) (string, error) {
	projectName, _ := args["project_name"].(string)
	query, _ := args["query"].(string)

	if projectName == "" || query == "" {
		return "", fmt.Errorf("project_name and query are required")
	}

	pages, err := wiki.ListPages(projectName)
	if err != nil {
		return "", err
	}

	queryLower := strings.ToLower(query)
	type match struct {
		title, slug, category string
		tags                  []string
		snippet               string
	}
	var matches []match

	for _, p := range pages {
		searchText := strings.ToLower(p.Title + " " + p.Slug + " " + strings.Join(p.Tags, " ") + " " + p.Content)
		idx := strings.Index(searchText, queryLower)
		if idx >= 0 {
			start := idx
			if start > 60 {
				start = idx - 60
			}
			end := idx + len(query) + 80
			if start < 0 {
				start = 0
			}
			if end > len(p.Content) {
				end = len(p.Content)
			}
			snippet := p.Content[start:end]
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			if start > 0 {
				snippet = "..." + snippet
			}
			if end < len(p.Content) {
				snippet = snippet + "..."
			}
			matches = append(matches, match{
				title: p.Title, slug: p.Slug, category: string(p.Category),
				tags: p.Tags, snippet: snippet,
			})
		}
	}

	if len(matches) == 0 {
		return fmt.Sprintf("No wiki pages matched %q in project %q", query, projectName), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for %q in %q (%d matches):\n\n", query, projectName, len(matches)))

	for i, m := range matches {
		sb.WriteString(fmt.Sprintf("%d. **%s** (slug: %s)\n", i+1, m.title, m.slug))
		sb.WriteString(fmt.Sprintf("   Category: %s\n", m.category))
		if len(m.tags) > 0 {
			sb.WriteString(fmt.Sprintf("   Tags: %s\n", strings.Join(m.tags, ", ")))
		}
		sb.WriteString(fmt.Sprintf("   Preview: %s\n\n", m.snippet))
	}

	return sb.String(), nil
}
