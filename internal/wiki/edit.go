package wiki

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
)

type PartialEditOptions struct {
	Append   string
	Section  string
	LineNum  int
	Find     string
	Replace  string
	LineText string
	Summary  string
	Author   string
}

func (o *PartialEditOptions) validate() error {
	count := 0
	if o.Append != "" {
		count++
	}
	if o.Section != "" {
		count++
	}
	if o.LineNum > 0 {
		count++
	}
	if o.Find != "" && o.Replace != "" {
		count++
	}
	if count == 0 {
		return fmt.Errorf("at least one edit flag is required: --append, --section, --line, or --find/--replace")
	}
	if count > 1 {
		return fmt.Errorf("only one edit mode can be used at a time")
	}
	if o.Find != "" && o.Replace == "" {
		return fmt.Errorf("--replace is required when using --find")
	}
	if o.Replace != "" && o.Find == "" {
		return fmt.Errorf("--find is required when using --replace")
	}
	if o.LineNum > 0 && o.LineText == "" {
		return fmt.Errorf("--line-text is required when using --line")
	}
	if o.Section != "" && o.LineText == "" {
		return fmt.Errorf("--line-text is required when using --section")
	}
	return nil
}

// backupFile creates a .bak backup of the given file.
func backupFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read file for backup: %w", err)
	}
	bakPath := filePath + ".bak"
	if err := os.WriteFile(bakPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}
	return nil
}

// PartiallyEditPage performs a partial edit on a wiki page.
func PartiallyEditPage(projectName, slug string, opts *PartialEditOptions) (*ent.WikiPage, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}

	page, err := GetPage(projectName, slug)
	if err != nil {
		return nil, err
	}

	filePath := pageFilePath(projectName, slug, page.Category)
	if err := backupFile(filePath); err != nil {
		return nil, err
	}

	content := page.Content
	var newContent string

	switch {
	case opts.Append != "":
		newContent = appendContent(content, opts.Append)
	case opts.Section != "":
		newContent, err = replaceSection(content, opts.Section, opts.LineText)
		if err != nil {
			return nil, err
		}
	case opts.LineNum > 0:
		newContent, err = replaceLine(content, opts.LineNum, opts.LineText)
		if err != nil {
			return nil, err
		}
	case opts.Find != "":
		newContent, err = findAndReplace(content, opts.Find, opts.Replace)
		if err != nil {
			return nil, err
		}
	}

	_, err = UpdatePageWithOptions(projectName, slug, "", newContent, nil, PageChangeOptions{
		Summary: opts.Summary,
		Author:  opts.Author,
	})
	if err != nil {
		return nil, err
	}

	return GetPage(projectName, slug)
}

// appendContent adds text to the end of the content.
func appendContent(content, text string) string {
	trimmed := strings.TrimRight(content, "\n\r ")
	if trimmed == "" {
		return text
	}
	if strings.HasSuffix(content, "\n") {
		return content + text
	}
	return content + "\n" + text
}

// replaceSection replaces the content under a markdown section header (## heading).
func replaceSection(content, sectionName, newText string) (string, error) {
	lines := strings.Split(content, "\n")
	var result []string
	sectionFound := false
	skipping := false
	sectionLevel := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "##") && !strings.HasPrefix(trimmed, "###") {
			headerName := strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
			if headerName == sectionName || headerName == fmt.Sprintf("# %s", sectionName) {
				sectionFound = true
				sectionLevel = strings.Count(trimmed, "#")
				result = append(result, line)
				skipping = true
				continue
			}
		}

		if skipping {
			if strings.HasPrefix(trimmed, "#") {
				currentLevel := strings.Count(trimmed, "#")
				if currentLevel <= sectionLevel {
					skipping = false
				}
			}
			if !skipping {
				result = append(result, line)
			}
			continue
		}

		result = append(result, line)
	}

	if !sectionFound {
		return "", fmt.Errorf("section %q not found in page", sectionName)
	}

	newContent := strings.Join(result, "\n")
	if !strings.HasSuffix(strings.TrimSpace(newContent), "\n") {
		newContent += "\n"
	}
	newContent += newText

	return newContent, nil
}

// replaceLine replaces a specific line (1-indexed) in the content.
func replaceLine(content string, lineNum int, newText string) (string, error) {
	lines := strings.Split(content, "\n")
	if lineNum < 1 || lineNum > len(lines) {
		return "", fmt.Errorf("line number %d out of range (1-%d)", lineNum, len(lines))
	}
	lines[lineNum-1] = newText
	return strings.Join(lines, "\n"), nil
}

// findAndReplace replaces the first line matching the given pattern.
func findAndReplace(content, pattern, replacement string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}

	lines := strings.Split(content, "\n")
	replaced := false
	var result []string

	for _, line := range lines {
		if !replaced && re.MatchString(line) {
			result = append(result, re.ReplaceAllString(line, replacement))
			replaced = true
		} else {
			result = append(result, line)
		}
	}

	if !replaced {
		return "", fmt.Errorf("no line matched pattern %q", pattern)
	}

	return strings.Join(result, "\n"), nil
}

// GetPageContent retrieves just the content of a wiki page for editing purposes.
func GetPageContent(projectName, slug string) (string, error) {
	page, err := GetPage(projectName, slug)
	if err != nil {
		return "", err
	}
	return page.Content, nil
}

// LineCount returns the number of lines in a wiki page.
func LineCount(projectName, slug string) (int, error) {
	content, err := GetPageContent(projectName, slug)
	if err != nil {
		return 0, err
	}
	return strings.Count(content, "\n") + 1, nil
}

// SearchPages searches for text across all wiki pages in a project.
func SearchPages(projectName string, query string) ([]WikiSearchResult, error) {
	pages, err := ListPages(projectName)
	if err != nil {
		return nil, err
	}

	re, err := regexp.Compile(`(?i)` + query)
	if err != nil {
		return nil, fmt.Errorf("invalid search pattern: %w", err)
	}

	var results []WikiSearchResult
	for _, page := range pages {
		lines := strings.Split(page.Content, "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				results = append(results, WikiSearchResult{
					Slug:    page.Slug,
					Title:   page.Title,
					LineNum: i + 1,
					Line:    strings.TrimSpace(line),
				})
			}
		}
	}

	return results, nil
}

// WikiSearchResult represents a single search result.
type WikiSearchResult struct {
	Slug    string
	Title   string
	LineNum int
	Line    string
}

// DiffContent compares old and new content, returning a simple unified diff.
func DiffContent(old, new string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	var diff []string
	diff = append(diff, "--- original")
	diff = append(diff, "+++ edited")

	i, j := 0, 0
	for i < len(oldLines) || j < len(newLines) {
		if i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j] {
			diff = append(diff, " "+oldLines[i])
			i++
			j++
		} else if j < len(newLines) && i < len(oldLines) {
			if differs(oldLines[i:], newLines[j:]) {
				diff = append(diff, "-"+oldLines[i])
				diff = append(diff, "+"+newLines[j])
				i++
				j++
			} else {
				diff = append(diff, "-"+oldLines[i])
				i++
			}
		} else if i < len(oldLines) {
			diff = append(diff, "-"+oldLines[i])
			i++
		} else {
			diff = append(diff, "+"+newLines[j])
			j++
		}
	}

	return strings.Join(diff, "\n")
}

func differs(old, new []string) bool {
	if len(old) == 0 || len(new) == 0 {
		return false
	}
	return old[0] != new[0]
}
