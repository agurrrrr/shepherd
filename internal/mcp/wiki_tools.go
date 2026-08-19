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
	s.tools["wiki_create"] = handleWikiCreate
	s.tools["wiki_edit"] = handleWikiEdit
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
					"project_name":     {Type: "string", Description: "Project name"},
					"query":            {Type: "string", Description: "Search query"},
					"regex":            {Type: "boolean", Description: "Interpret query as regular expression"},
					"tag":              {Type: "string", Description: "Filter by tag"},
					"category":         {Type: "string", Description: "Filter by category"},
					"title_only":       {Type: "boolean", Description: "Search title only (exclude body)"},
					"case_insensitive": {Type: "boolean", Description: "Case insensitive search (default: true)"},
				},
				Required: []string{"project_name", "query"},
			},
		},
		{
			Name: "wiki_create",
			Description: "새 페이지 생성 전용. 기존 slug 덮어쓰기 금지 — 기존 페이지 수정은 wiki_edit 사용. " +
				"content는 비어 있으면 안 된다(마크다운 본문 필수).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
					"slug":         {Type: "string", Description: "Wiki page slug (must be unique)"},
					"title":        {Type: "string", Description: "Page title"},
					"content":      {Type: "string", Description: "Markdown body (required, non-empty)"},
					"category": {
						Type:        "string",
						Enum:        []string{"architecture", "patterns", "troubleshooting", "deployment", "lessons", "entity", "custom"},
						Description: "Page category",
					},
					"tags": {Type: "string", Description: "Comma-separated tags"},
				},
				Required: []string{"project_name", "slug", "title", "content"},
			},
		},
		{
			Name: "wiki_edit",
			Description: "한 호출에 모드 하나만. 대량 덮어쓰기 대신 append 우선 권장. " +
				"mode=append→text; mode=section→section+line_text; mode=line→line_num+line_text; " +
				"mode=find_replace→find+replace. 모드와 무관한 필드는 무시된다.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "Project name"},
					"slug":         {Type: "string", Description: "Wiki page slug"},
					"mode": {
						Type:        "string",
						Enum:        []string{"append", "section", "line", "find_replace"},
						Description: "Edit mode (exactly one per call)",
					},
					"text":      {Type: "string", Description: "Text to append (mode=append)"},
					"section":   {Type: "string", Description: "Section header name (mode=section)"},
					"line_num":  {Type: "number", Description: "1-based line number (mode=line)"},
					"line_text": {Type: "string", Description: "New line/section content (mode=section|line)"},
					"find":      {Type: "string", Description: "Regex to find (mode=find_replace)"},
					"replace":   {Type: "string", Description: "Replacement text (mode=find_replace)"},
					"summary":   {Type: "string", Description: "Change summary for version history"},
				},
				Required: []string{"project_name", "slug", "mode"},
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

	opts := wiki.SearchOptions{
		Query:           query,
		Regex:           toBool(args["regex"]),
		Tag:             toString(args["tag"]),
		Category:        toString(args["category"]),
		TitleOnly:       toBool(args["title_only"]),
		CaseInsensitive: true,
	}
	if ci, ok := args["case_insensitive"]; ok {
		opts.CaseInsensitive = toBool(ci)
	}

	results, err := wiki.SearchPagesAdvanced(projectName, opts)
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return fmt.Sprintf("No wiki pages matched %q in project %q", query, projectName), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for %q in %q (%d pages):\n\n", query, projectName, len(results)))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. **%s** (slug: %s)\n", i+1, r.Title, r.Slug))
		sb.WriteString(fmt.Sprintf("   Category: %s\n", r.Category))
		if len(r.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("   Tags: %s\n", strings.Join(r.Tags, ", ")))
		}
		sb.WriteString("   Matches:\n")
		for _, m := range r.Matches {
			if m.LineNum == 0 {
				sb.WriteString(fmt.Sprintf("     ↪ %s\n", m.Line))
			} else {
				sb.WriteString(fmt.Sprintf("     %d: %s\n", m.LineNum, m.Line))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func handleWikiCreate(args map[string]interface{}) (string, error) {
	projectName := toString(args["project_name"])
	slug := toString(args["slug"])
	title := toString(args["title"])
	content := toString(args["content"])
	// MCP 의도적 강화: 하부 CreatePage는 빈 content 허용하지만 여기선 거부 (§3.5)
	if projectName == "" || slug == "" || title == "" || content == "" {
		return "", fmt.Errorf("project_name, slug, title, content are required")
	}
	var tags []string
	if t := toString(args["tags"]); t != "" {
		for _, x := range strings.Split(t, ",") {
			if x = strings.TrimSpace(x); x != "" {
				tags = append(tags, x)
			}
		}
	}
	page, err := wiki.CreatePage(projectName, slug, title, toString(args["category"]), content, tags)
	if err != nil {
		// 선택: 중복 slug 친절 래핑 (#7797)
		if strings.Contains(err.Error(), "already exists") {
			return "", fmt.Errorf("wiki page %q already exists — use wiki_edit to modify existing pages (no overwrite)", slug)
		}
		return "", err
	}
	return fmt.Sprintf("위키 페이지 생성됨: %s (slug: %s, category: %s)", page.Title, page.Slug, page.Category), nil
}

func handleWikiEdit(args map[string]interface{}) (string, error) {
	projectName := toString(args["project_name"])
	slug := toString(args["slug"])
	mode := toString(args["mode"])
	if projectName == "" || slug == "" || mode == "" {
		return "", fmt.Errorf("project_name, slug, mode are required")
	}

	opts := &wiki.PartialEditOptions{
		Summary: toString(args["summary"]),
	}
	// mode에 맞는 필드만 세팅 → validate()가 단일 모드 강제. 모드 무관 필드는 무시.
	switch mode {
	case "append":
		opts.Append = toString(args["text"])
	case "section":
		opts.Section = toString(args["section"])
		opts.LineText = toString(args["line_text"])
	case "line":
		opts.LineNum = toInt(args["line_num"])
		opts.LineText = toString(args["line_text"])
	case "find_replace":
		opts.Find = toString(args["find"])
		opts.Replace = toString(args["replace"])
	default:
		return "", fmt.Errorf("invalid mode %q: must be append|section|line|find_replace", mode)
	}

	page, err := wiki.PartiallyEditPage(projectName, slug, opts)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("위키 페이지 수정됨: %s (slug: %s, mode: %s)", page.Title, page.Slug, mode), nil
}

// ListWikiToolDefs returns the list of wiki tool definitions.
// Exported for use by the embedded provider.
func ListWikiToolDefs() []Tool {
	return getWikiToolsList()
}

