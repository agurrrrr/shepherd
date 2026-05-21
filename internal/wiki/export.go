package wiki

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/wikipage"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type ExportOptions struct {
	Format  string // "markdown", "single-md", "html"
	Output  string // output path
	Project string // project name
}

type ImportOptions struct {
	Project string // project name to import into
	Path    string // input file or directory path
	Force   bool   // overwrite existing pages without confirmation
	DryRun  bool   // preview without actually importing
}

// Heading represents a markdown heading for TOC generation.
type Heading struct {
	Level int
	Text  string
}

// ExportAll exports all wiki pages for a project to the given output path.
func ExportAll(opts ExportOptions) error {
	if opts.Project == "" {
		return fmt.Errorf("project name is required")
	}
	if opts.Output == "" {
		opts.Output = "."
	}
	if opts.Format == "" {
		opts.Format = "markdown"
	}

	switch opts.Format {
	case "markdown":
		return exportToMarkdown(opts)
	case "single-md":
		return exportToSingleMD(opts)
	case "html":
		return exportToHTML(opts)
	default:
		return fmt.Errorf("unsupported export format: %q (supported: markdown, single-md, html)", opts.Format)
	}
}

// exportToMarkdown exports each page as an individual markdown file into a directory.
func exportToMarkdown(opts ExportOptions) error {
	pages, err := ListPages(opts.Project)
	if err != nil {
		return err
	}

	if len(pages) == 0 {
		return fmt.Errorf("no wiki pages found for project %q", opts.Project)
	}

	baseDir := opts.Output
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Slug < pages[j].Slug
	})

	var created []string
	for _, page := range pages {
		dir := baseDir
		if page.Category == wikipage.CategoryEntity {
			dir = filepath.Join(baseDir, "entities")
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		fileName := page.Slug + ".md"
		filePath := filepath.Join(dir, fileName)

		content := buildExportMarkdownContent(page)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", filePath, err)
		}

		created = append(created, fileName)
	}

	fmt.Printf("Exported %d page(s) to %s\n", len(created), baseDir)
	for _, f := range created {
		fmt.Printf("  - %s\n", f)
	}
	return nil
}

// exportToSingleMD exports all pages into a single markdown file.
func exportToSingleMD(opts ExportOptions) error {
	pages, err := ListPages(opts.Project)
	if err != nil {
		return err
	}

	if len(pages) == 0 {
		return fmt.Errorf("no wiki pages found for project %q", opts.Project)
	}

	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Slug < pages[j].Slug
	})

	outFile := opts.Output
	if filepath.Ext(outFile) != ".md" {
		outFile = outFile + ".md"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s Wiki\n\n", opts.Project))
	sb.WriteString(fmt.Sprintf("> 내보내기 시간: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString("---\n\n")

	for i, page := range pages {
		sb.WriteString(fmt.Sprintf("## %s\n\n", page.Title))
		if page.Category != "" {
			sb.WriteString(fmt.Sprintf("**카테고리:** %s  ", page.Category))
		}
		if len(page.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("**태그:** %s  ", strings.Join(page.Tags, ", ")))
		}
		if !page.UpdatedAt.IsZero() {
			sb.WriteString(fmt.Sprintf("**수정:** %s", page.UpdatedAt.Format("2006-01-02 15:04")))
		}
		sb.WriteString("\n\n")
		sb.WriteString(page.Content)

		if i < len(pages)-1 {
			sb.WriteString("\n\n---\n\n")
		}
	}

	if err := os.WriteFile(outFile, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outFile, err)
	}

	fmt.Printf("Exported %d page(s) to %s\n", len(pages), outFile)
	return nil
}

// exportToHTML exports all pages into a single HTML document.
func exportToHTML(opts ExportOptions) error {
	pages, err := ListPages(opts.Project)
	if err != nil {
		return err
	}

	if len(pages) == 0 {
		return fmt.Errorf("no wiki pages found for project %q", opts.Project)
	}

	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Slug < pages[j].Slug
	})

	outFile := opts.Output
	if filepath.Ext(outFile) != ".html" {
		outFile = outFile + ".html"
	}

	md := goldmark.New()

	renderMD := func(src []byte, w io.Writer) error {
		return md.Convert(src, w)
	}

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html lang=\"ko\">\n<head>\n")
	sb.WriteString("  <meta charset=\"UTF-8\">\n")
	sb.WriteString("  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	sb.WriteString(fmt.Sprintf("  <title>%s Wiki</title>\n", opts.Project))
	sb.WriteString(`  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 40px auto; padding: 0 20px; line-height: 1.6; color: #333; }
    h1 { border-bottom: 2px solid #eee; padding-bottom: 10px; }
    h2 { border-bottom: 1px solid #eee; padding-bottom: 8px; margin-top: 40px; }
    .meta { color: #888; font-size: 0.9em; margin-bottom: 16px; }
    .separator { border: none; border-top: 2px solid #eee; margin: 40px 0; }
    code { background: #f5f5f5; padding: 2px 6px; border-radius: 3px; font-size: 0.9em; }
    pre { background: #f5f5f5; padding: 16px; border-radius: 6px; overflow-x: auto; }
    pre code { background: none; padding: 0; }
    .toc { background: #f9f9f9; padding: 16px 24px; border-radius: 6px; }
    .toc ul { margin: 8px 0 0; padding-left: 20px; }
    .toc li { margin: 4px 0; }
    a { color: #0366d6; text-decoration: none; }
    a:hover { text-decoration: underline; }
  </style>
`)
	sb.WriteString("</head>\n<body>\n")
	sb.WriteString(fmt.Sprintf("<h1>%s Wiki</h1>\n", opts.Project))
	sb.WriteString(fmt.Sprintf("<p class=\"meta\">내보내기 시간: %s</p>\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// Table of contents
	sb.WriteString("<nav class=\"toc\">\n<h2>목차</h2>\n<ul>\n")
	for _, page := range pages {
		anchor := strings.ToLower(strings.ReplaceAll(page.Title, " ", "-"))
		sb.WriteString(fmt.Sprintf("<li><a href=\"#%s\">%s</a></li>\n", anchor, page.Title))
	}
	sb.WriteString("</ul>\n</nav>\n\n")

	for i, page := range pages {
		anchor := strings.ToLower(strings.ReplaceAll(page.Title, " ", "-"))
		sb.WriteString(fmt.Sprintf("<h2 id=\"%s\">%s</h2>\n", anchor, page.Title))

		metaParts := []string{}
		if page.Category != "" {
			metaParts = append(metaParts, fmt.Sprintf("카테고리: %s", page.Category))
		}
		if len(page.Tags) > 0 {
			metaParts = append(metaParts, fmt.Sprintf("태그: %s", strings.Join(page.Tags, ", ")))
		}
		if !page.UpdatedAt.IsZero() {
			metaParts = append(metaParts, fmt.Sprintf("수정: %s", page.UpdatedAt.Format("2006-01-02 15:04")))
		}
		if len(metaParts) > 0 {
			sb.WriteString(fmt.Sprintf("<p class=\"meta\">%s</p>\n", strings.Join(metaParts, "  ")))
		}

		var contentBuf bytes.Buffer
		if err := renderMD([]byte(page.Content), &contentBuf); err != nil {
			return fmt.Errorf("failed to convert markdown for %q: %w", page.Slug, err)
		}
		sb.WriteString(contentBuf.String())

		if i < len(pages)-1 {
			sb.WriteString("<hr class=\"separator\">\n\n")
		}
	}

	sb.WriteString("</body>\n</html>\n")

	if err := os.WriteFile(outFile, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outFile, err)
	}

	fmt.Printf("Exported %d page(s) to %s\n", len(pages), outFile)
	return nil
}

// buildExportMarkdownContent builds the markdown content for a single exported page.
func buildExportMarkdownContent(page *ent.WikiPage) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("title: %s\n", page.Title))
	sb.WriteString(fmt.Sprintf("slug: %s\n", page.Slug))
	sb.WriteString(fmt.Sprintf("category: %s\n", page.Category))
	if len(page.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("tags: [%s]\n", strings.Join(page.Tags, ", ")))
	}
	if !page.UpdatedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("last_updated: %s\n", page.UpdatedAt.Format("2006-01-02")))
	}
	sb.WriteString("---\n")
	sb.WriteString(page.Content)
	return sb.String()
}

// ImportAll imports wiki pages from a file or directory into a project.
func ImportAll(opts ImportOptions) error {
	if opts.Path == "" {
		return fmt.Errorf("input path is required")
	}

	info, err := os.Stat(opts.Path)
	if err != nil {
		return fmt.Errorf("failed to access path: %w", err)
	}

	if info.IsDir() {
		return importFromDirectory(opts)
	}

	return importFromFile(opts)
}

// importFromFile imports a single markdown file as a wiki page.
func importFromFile(opts ImportOptions) error {
	if !strings.HasSuffix(strings.ToLower(opts.Path), ".md") {
		return fmt.Errorf("file must be a markdown file (.md): %s", opts.Path)
	}

	data, err := os.ReadFile(opts.Path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	title, body, tags := parseFrontmatter(string(data))
	slug := strings.TrimSuffix(filepath.Base(opts.Path), filepath.Ext(opts.Path))

	if title == "" {
		title = slug
	}

	if opts.DryRun {
		fmt.Printf("[DRY RUN] Would import page:\n")
		fmt.Printf("  slug:     %s\n", slug)
		fmt.Printf("  title:    %s\n", title)
		fmt.Printf("  tags:     %v\n", tags)
		fmt.Printf("  content:  %d bytes\n", len(body))
		return nil
	}

	existing, err := findPageByProjectAndSlug(opts.Project, slug)
	if err != nil {
		return err
	}

	if existing != nil {
		if !opts.Force {
			return fmt.Errorf("page %q already exists. Use --force to overwrite", slug)
		}
		_, err = UpdatePageWithOptions(opts.Project, slug, title, body, tags, PageChangeOptions{
			Summary: fmt.Sprintf("가져오기: %s", opts.Path),
		})
		if err != nil {
			return fmt.Errorf("failed to update page %q: %w", slug, err)
		}
		fmt.Printf("Updated existing page: %s (%s)\n", slug, title)
	} else {
		_, err = CreatePageWithOptions(opts.Project, slug, title, "custom", body, tags, PageChangeOptions{
			Summary: fmt.Sprintf("가져오기: %s", opts.Path),
		})
		if err != nil {
			return fmt.Errorf("failed to create page %q: %w", slug, err)
		}
		fmt.Printf("Imported new page: %s (%s)\n", slug, title)
	}

	return nil
}

// importFromDirectory imports all .md files from a directory as wiki pages.
func importFromDirectory(opts ImportOptions) error {
	mdFiles, err := collectMarkdownFiles(opts.Path)
	if err != nil {
		return fmt.Errorf("failed to scan directory: %w", err)
	}

	if len(mdFiles) == 0 {
		return fmt.Errorf("no markdown files found in %s", opts.Path)
	}

	sort.Strings(mdFiles)

	var imported, updated, skipped int
	for _, mdFile := range mdFiles {
		relPath, _ := filepath.Rel(opts.Path, mdFile)
		slug := strings.TrimSuffix(relPath, filepath.ToSlash(filepath.Ext(relPath)))
		slug = filepath.ToSlash(slug)

		isEntity := false
		if strings.HasPrefix(slug, "entities/") {
			slug = strings.TrimPrefix(slug, "entities/")
			isEntity = true
		}

		data, err := os.ReadFile(mdFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to read %s: %v\n", mdFile, err)
			skipped++
			continue
		}

		title, body, tags := parseFrontmatter(string(data))
		if title == "" {
			title = slug
		}

		category := "custom"
		if isEntity {
			category = "entity"
		}

		if opts.DryRun {
			fmt.Printf("[DRY RUN] Would import page:\n")
			fmt.Printf("  file:     %s\n", relPath)
			fmt.Printf("  slug:     %s\n", slug)
			fmt.Printf("  title:    %s\n", title)
			fmt.Printf("  category: %s\n", category)
			fmt.Printf("  tags:     %v\n", tags)
			fmt.Printf("  content:  %d bytes\n\n", len(body))
			imported++
			continue
		}

		existing, err := findPageByProjectAndSlug(opts.Project, slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to check page %q: %v\n", slug, err)
			skipped++
			continue
		}

		if existing != nil {
			if !opts.Force {
				fmt.Fprintf(os.Stderr, "Warning: page %q already exists, skipping (use --force to overwrite)\n", slug)
				skipped++
				continue
			}
			_, err = UpdatePageWithOptions(opts.Project, slug, title, body, tags, PageChangeOptions{
				Summary: fmt.Sprintf("가져오기: %s", relPath),
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to update page %q: %v\n", slug, err)
				skipped++
				continue
			}
			updated++
		} else {
			_, err = CreatePageWithOptions(opts.Project, slug, title, category, body, tags, PageChangeOptions{
				Summary: fmt.Sprintf("가져오기: %s", relPath),
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create page %q: %v\n", slug, err)
				skipped++
				continue
			}
			imported++
		}
	}

	if opts.DryRun {
		fmt.Printf("\n[DRY RUN] Would import %d page(s)\n", imported)
	} else {
		fmt.Printf("\nImport complete: %d imported, %d updated, %d skipped\n", imported, updated, skipped)
	}
	return nil
}

// MarkdownToHTML converts markdown text to HTML using goldmark.
func MarkdownToHTML(mdContent string) (string, error) {
	md := goldmark.New()
	var buf bytes.Buffer
	if err := md.Convert([]byte(mdContent), &buf); err != nil {
		return "", fmt.Errorf("failed to convert markdown to html: %w", err)
	}
	return buf.String(), nil
}

// ExtractHeadings extracts markdown headings for TOC generation.
func ExtractHeadings(mdContent string) []Heading {
	md := goldmark.New()
	reader := text.NewReader([]byte(mdContent))
	doc := md.Parser().Parse(reader)

	var headings []Heading
	walkAST(doc, func(n ast.Node) {
		if h, ok := n.(*ast.Heading); ok {
			var text strings.Builder
			walkText(h, &text)
			headings = append(headings, Heading{
				Level: int(h.Level),
				Text:  text.String(),
			})
		}
	})

	return headings
}

// walkAST recursively walks an AST node tree, calling fn for each node.
func walkAST(node ast.Node, fn func(ast.Node)) {
	fn(node)
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		walkAST(child, fn)
	}
}

// walkText recursively extracts text content from an AST node.
func walkText(node ast.Node, buf *strings.Builder) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if s, ok := child.(*ast.Text); ok {
			buf.Write(s.Text(nil))
		} else {
			walkText(child, buf)
		}
	}
}
