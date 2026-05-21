package wiki

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
)

// SearchOptions holds configuration for advanced wiki page search.
type SearchOptions struct {
	Query           string // Search query (text or regex)
	Regex           bool   // Interpret query as a regular expression
	Tag             string // Filter by tag
	Category        string // Filter by category
	TitleOnly       bool   // Search title only (exclude body content)
	CaseInsensitive bool   // Case insensitive search (default true)
}

// SearchMatch represents a single matching line within a page.
type SearchMatch struct {
	LineNum int    // 1-indexed line number
	Line    string // The matching line content
}

// SearchPageResult represents all matches within a single wiki page.
type SearchPageResult struct {
	Slug     string
	Title    string
	Category string
	Tags     []string
	Matches  []SearchMatch
}

// SearchPagesAdvanced performs an advanced search across all wiki pages in a project.
func SearchPagesAdvanced(projectName string, opts SearchOptions) ([]SearchPageResult, error) {
	if opts.Query == "" {
		return nil, fmt.Errorf("search query is required")
	}

	if opts.Regex {
		return searchWithRegex(projectName, opts)
	}
	return searchWithText(projectName, opts)
}

// searchWithText performs a plain-text search.
func searchWithText(projectName string, opts SearchOptions) ([]SearchPageResult, error) {
	pages, err := ListPages(projectName)
	if err != nil {
		return nil, err
	}

	caseInsensitive := opts.CaseInsensitive
	query := opts.Query
	if caseInsensitive {
		query = strings.ToLower(query)
	}

	var results []SearchPageResult
	for _, page := range pages {
		if !pageMatchesFilters(page, opts) {
			continue
		}

		var matches []SearchMatch
		if opts.TitleOnly {
			title := page.Title
			searchTitle := title
			if caseInsensitive {
				searchTitle = strings.ToLower(title)
			}
			if strings.Contains(searchTitle, query) {
				matches = []SearchMatch{{LineNum: 0, Line: title}}
			}
		} else {
			matches = findPlainTextMatches(page.Title, page.Content, query, caseInsensitive)
		}

		if len(matches) > 0 {
			results = append(results, SearchPageResult{
				Slug:     page.Slug,
				Title:    page.Title,
				Category: string(page.Category),
				Tags:     page.Tags,
				Matches:  matches,
			})
		}
	}

	return results, nil
}

// searchWithRegex performs a regex-based search.
func searchWithRegex(projectName string, opts SearchOptions) ([]SearchPageResult, error) {
	pages, err := ListPages(projectName)
	if err != nil {
		return nil, err
	}

	caseInsensitive := opts.CaseInsensitive
	pattern := opts.Query
	if caseInsensitive {
		pattern = "(?i)" + pattern
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern %q: %w", opts.Query, err)
	}

	var results []SearchPageResult
	for _, page := range pages {
		if !pageMatchesFilters(page, opts) {
			continue
		}

		var matches []SearchMatch
		if opts.TitleOnly {
			if re.MatchString(page.Title) {
				matches = []SearchMatch{{LineNum: 0, Line: page.Title}}
			}
		} else {
			matches = findRegexMatches(page.Title, page.Content, re)
		}

		if len(matches) > 0 {
			results = append(results, SearchPageResult{
				Slug:     page.Slug,
				Title:    page.Title,
				Category: string(page.Category),
				Tags:     page.Tags,
				Matches:  matches,
			})
		}
	}

	return results, nil
}

// pageMatchesFilters checks if a page matches the tag and category filters.
func pageMatchesFilters(page *ent.WikiPage, opts SearchOptions) bool {
	if opts.Category != "" {
		if string(page.Category) != opts.Category {
			return false
		}
	}
	if opts.Tag != "" {
		found := false
		for _, t := range page.Tags {
			if strings.EqualFold(t, opts.Tag) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// findPlainTextMatches finds all lines matching a plain text query.
// Searches both title and content lines.
func findPlainTextMatches(title, content, query string, caseInsensitive bool) []SearchMatch {
	var matches []SearchMatch

	// Check title
	titleToSearch := title
	if caseInsensitive {
		titleToSearch = strings.ToLower(title)
	}
	if strings.Contains(titleToSearch, query) {
		matches = append(matches, SearchMatch{LineNum: 0, Line: title})
	}

	// Check content lines
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lineToSearch := line
		if caseInsensitive {
			lineToSearch = strings.ToLower(line)
		}
		if strings.Contains(lineToSearch, query) {
			matches = append(matches, SearchMatch{
				LineNum: i + 1,
				Line:    strings.TrimSpace(line),
			})
		}
	}

	return matches
}

// findRegexMatches finds all lines matching a regex pattern.
// Searches both title and content lines.
func findRegexMatches(title, content string, re *regexp.Regexp) []SearchMatch {
	var matches []SearchMatch

	// Check title
	if re.MatchString(title) {
		matches = append(matches, SearchMatch{LineNum: 0, Line: title})
	}

	// Check content lines
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if re.MatchString(line) {
			matches = append(matches, SearchMatch{
				LineNum: i + 1,
				Line:    strings.TrimSpace(line),
			})
		}
	}

	return matches
}
