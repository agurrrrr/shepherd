package wiki

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
)

type PartialEditOptions struct {
	// Mode explicitly selects the edit mode ("append"|"section"|"line"|"find_replace").
	// When empty it is inferred from the set fields (CLI backward compat).
	Mode    string
	Append  string
	Section string
	LineNum int
	Find    string
	Replace string
	// ReplaceSet reports whether the caller explicitly provided a Replace value
	// (even an empty one). An explicitly empty Replace in find_replace mode
	// means "delete the match"; an absent Replace is a validation error.
	ReplaceSet bool
	LineText   string
	// LineTextSet reports whether the caller explicitly provided LineText
	// (even an empty one). An explicitly empty LineText in section mode clears
	// the section body; an absent value is a validation error.
	LineTextSet bool
	Summary     string
	Author      string
}

func (o *PartialEditOptions) validate() error {
	mode := o.Mode
	if mode == "" {
		// Infer the mode from whichever field is set (CLI backward compat).
		set := 0
		if o.Append != "" {
			set++
		}
		if o.Section != "" {
			set++
		}
		if o.LineNum > 0 {
			set++
		}
		if o.Find != "" || o.Replace != "" || o.ReplaceSet {
			set++
		}
		if set == 0 {
			return fmt.Errorf("at least one edit flag is required: --append, --section, --line, or --find/--replace")
		}
		if set > 1 {
			return fmt.Errorf("only one edit mode can be used at a time")
		}
		switch {
		case o.Append != "":
			mode = "append"
		case o.Section != "":
			mode = "section"
		case o.LineNum > 0:
			mode = "line"
		default:
			mode = "find_replace"
		}
		o.Mode = mode // normalize so callers can dispatch on the resolved mode
	}

	switch mode {
	case "append":
		if o.Append == "" {
			return fmt.Errorf("append text is required in append mode")
		}
	case "section":
		if o.Section == "" {
			return fmt.Errorf("section is required in section mode")
		}
		// A non-empty line-text is by definition provided. The Set flag only
		// matters for an empty value: an explicitly empty line-text clears the
		// section body, while an absent one is an error (no silent wipe).
		lineTextSet := o.LineTextSet || o.LineText != ""
		if !lineTextSet {
			return fmt.Errorf("line-text is required in section mode (provide an empty value to clear the section body)")
		}
	case "line":
		if o.LineNum <= 0 {
			return fmt.Errorf("line number is required in line mode")
		}
		if o.LineText == "" {
			return fmt.Errorf("line-text is required in line mode")
		}
	case "find_replace":
		if o.Find == "" {
			return fmt.Errorf("find is required in find_replace mode")
		}
		// A non-empty replace is by definition provided. The Set flag only
		// matters for an empty value: an explicitly empty replace deletes the
		// match, while an absent one is an error.
		replaceSet := o.ReplaceSet || o.Replace != ""
		if !replaceSet {
			return fmt.Errorf("replace is required in find_replace mode (provide an empty value to delete the match)")
		}
	default:
		return fmt.Errorf("invalid mode %q: must be append|section|line|find_replace", mode)
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

	// opts.Mode is normalized by validate(); dispatch on it.
	switch opts.Mode {
	case "append":
		newContent = appendContent(content, opts.Append)
	case "section":
		newContent, err = replaceSection(content, opts.Section, opts.LineText)
		if err != nil {
			return nil, err
		}
	case "line":
		newContent, err = replaceLine(content, opts.LineNum, opts.LineText)
		if err != nil {
			return nil, err
		}
	case "find_replace":
		newContent, err = findAndReplace(content, opts.Find, opts.Replace)
		if err != nil {
			return nil, err
		}
	}

	// Preserve the page's existing tags. Passing nil here used to wipe them
	// (SetTags(nil) -> NULL column) on every partial edit.
	_, err = UpdatePageWithOptions(projectName, slug, "", newContent, page.Tags, PageChangeOptions{
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

// normalizeSectionName strips markdown heading markers and surrounding
// whitespace so callers can pass either "My Section" or "## My Section".
func normalizeSectionName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimLeft(name, "#")
	return strings.TrimSpace(name)
}

// headingLevel returns the number of leading '#' in a markdown heading line,
// or 0 if the line is not a heading. A heading requires a space/tab (or end of
// line) after the '#' run, so "##hashtag" is not a heading.
func headingLevel(line string) int {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return 0
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == len(trimmed) || trimmed[level] == ' ' || trimmed[level] == '\t' {
		return level
	}
	return 0
}

// headingText returns the text of a markdown heading line (without the '#' run).
func headingText(line string) string {
	level := headingLevel(line)
	if level == 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimSpace(line)[level:])
}

// availableHeadings lists the text of all markdown headings in content.
func availableHeadings(content string) []string {
	var names []string
	for _, line := range strings.Split(content, "\n") {
		if headingLevel(line) > 0 {
			if t := headingText(line); t != "" {
				names = append(names, t)
			}
		}
	}
	return names
}

// replaceSection replaces the body of the markdown section whose heading text
// matches sectionName, in place. The section may be any heading level; the
// replaced range runs until the next heading of the same or higher level
// (deeper subsections are included). Only the first matching section is
// replaced. An empty newText clears the section body (the heading is kept).
// The section name may be given with or without the "##" prefix.
func replaceSection(content, sectionName, newText string) (string, error) {
	target := normalizeSectionName(sectionName)
	if target == "" {
		return "", fmt.Errorf("section name is empty")
	}

	body := strings.Trim(newText, "\n")

	// Replacement blocks: a blank line after the heading, the new body, and a
	// trailing blank line when more content follows.
	blockMid := []string{""}
	if body != "" {
		blockMid = append(blockMid, body, "")
	}
	blockEnd := []string{""}
	if body != "" {
		blockEnd = append(blockEnd, body)
	}

	lines := strings.Split(content, "\n")
	var result []string
	sectionFound := false
	inSection := false
	sectionLevel := 0

	for _, line := range lines {
		level := headingLevel(line)

		if inSection {
			if level > 0 && level <= sectionLevel {
				// The section ends at the next same-or-higher-level heading.
				inSection = false
				result = append(result, blockMid...)
				result = append(result, line)
				continue
			}
			// Body lines (including deeper subsections) are replaced: dropped.
			continue
		}

		if !sectionFound && level > 0 && headingText(line) == target {
			sectionFound = true
			sectionLevel = level
			inSection = true
			result = append(result, line)
			continue
		}
		result = append(result, line)
	}

	if !sectionFound {
		return "", fmt.Errorf("section %q not found in page (available sections: %s)", sectionName, strings.Join(availableHeadings(content), ", "))
	}
	if inSection {
		result = append(result, blockEnd...)
	}

	return strings.Join(result, "\n"), nil
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

// findAndReplace replaces the first region of content matching the pattern.
//
// The pattern is a regular expression applied to the whole content, so it can
// match within a line or across multiple lines (e.g. "a\nb" or "[^\n]*old[^\n]*\n[^\n]*").
// The matched region is replaced with the replacement text, which may itself
// contain newlines (adding/removing lines) or "$1"-style backreferences to the
// pattern's capture groups. An empty replacement deletes the matched region.
//
// Only the first match is replaced.
func findAndReplace(content, pattern, replacement string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}

	loc := re.FindStringIndex(content)
	if loc == nil {
		return "", fmt.Errorf("no text matched pattern %q", pattern)
	}

	// Run the replacement on the matched region so capture-group
	// backreferences ($1, ...) resolve against the match's own groups.
	return content[:loc[0]] + re.ReplaceAllString(content[loc[0]:loc[1]], replacement) + content[loc[1]:], nil
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
// Deprecated: Use SearchPagesAdvanced instead for advanced options.
func SearchPages(projectName string, query string) ([]WikiSearchResult, error) {
	results, err := SearchPagesAdvanced(projectName, SearchOptions{
		Query:           query,
		CaseInsensitive: true,
	})
	if err != nil {
		return nil, err
	}

	var legacyResults []WikiSearchResult
	for _, r := range results {
		for _, m := range r.Matches {
			if m.LineNum > 0 {
				legacyResults = append(legacyResults, WikiSearchResult{
					Slug:    r.Slug,
					Title:   r.Title,
					LineNum: m.LineNum,
					Line:    m.Line,
				})
			}
		}
	}

	return legacyResults, nil
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
