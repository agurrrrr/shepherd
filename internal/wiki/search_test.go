package wiki

import (
	"regexp"
	"testing"
)

func TestFindPlainTextMatches(t *testing.T) {
	tests := []struct {
		name            string
		title           string
		content         string
		query           string
		caseInsensitive bool
		wantCount       int
	}{
		{
			name:            "simple match in content",
			title:           "Test Page",
			content:         "Line 1\nGo is great\nLine 3",
			query:           "Go",
			caseInsensitive: false,
			wantCount:       2, // title "Test Page" doesn't match, line 2 matches, total 1 match in content + 0 in title
		},
		{
			name:            "case insensitive match",
			title:           "Test Page",
			content:         "Line 1\ngo is great\nLine 3",
			query:           "GO",
			caseInsensitive: true,
			wantCount:       1,
		},
		{
			name:            "case sensitive no match",
			title:           "Test Page",
			content:         "Line 1\ngo is great\nLine 3",
			query:           "GO",
			caseInsensitive: false,
			wantCount:       0,
		},
		{
			name:            "title match",
			title:           "Go Programming",
			content:         "Some content without Go",
			query:           "Go",
			caseInsensitive: false,
			wantCount:       1, // only title matches
		},
		{
			name:            "title and content match",
			title:           "Go Programming",
			content:         "Go is great\nOther line",
			query:           "Go",
			caseInsensitive: false,
			wantCount:       2, // title + line 1
		},
		{
			name:            "no match",
			title:           "Test Page",
			content:         "Line 1\nLine 2\nLine 3",
			query:           "Go",
			caseInsensitive: true,
			wantCount:       0,
		},
		{
			name:            "empty content",
			title:           "Go Page",
			content:         "",
			query:           "Go",
			caseInsensitive: true,
			wantCount:       1, // title only
		},
		{
			name:            "multiple matches on same line",
			title:           "Test",
			content:         "Go Go Go\nOther line",
			query:           "Go",
			caseInsensitive: false,
			wantCount:       1, // counts as 1 line match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := findPlainTextMatches(tt.title, tt.content, tt.query, tt.caseInsensitive)
			if len(matches) != tt.wantCount {
				t.Errorf("got %d matches, want %d", len(matches), tt.wantCount)
				for _, m := range matches {
					t.Logf("  match: line=%d, text=%s", m.LineNum, m.Line)
				}
			}
		})
	}
}

func TestFindRegexMatches(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		content string
		pattern string
		want    int
	}{
		{
			name:    "simple regex",
			title:   "Test Page",
			content: "Line 1\ngolang is great\nLine 3",
			pattern: "(?i)go\\w+",
			want:    1,
		},
		{
			name:    "regex with alternation",
			title:   "Test Page",
			content: "Go is here\nK8s is here too\nNo match",
			pattern: "(?i)(go|k8s)",
			want:    2,
		},
		{
			name:    "title regex match",
			title:   "Go Programming",
			content: "No go keyword here",
			pattern: "^Go\\s+\\w+",
			want:    1,
		},
		{
			name:    "no regex match",
			title:   "Test Page",
			content: "Line 1\nLine 2\nLine 3",
			pattern: "xyz123",
			want:    0,
		},
		{
			name:    "case insensitive regex flag",
			title:   "Test Page",
			content: "Go is great\ngo is also great",
			pattern: "(?i)^go",
			want:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re := regexp.MustCompile(tt.pattern)
			matches := findRegexMatches(tt.title, tt.content, re)
			if len(matches) != tt.want {
				t.Errorf("got %d matches, want %d", len(matches), tt.want)
				for _, m := range matches {
					t.Logf("  match: line=%d, text=%s", m.LineNum, m.Line)
				}
			}
		})
	}
}

func TestSearchOptionsEmptyQuery(t *testing.T) {
	_, err := SearchPagesAdvanced("nonexistent_project", SearchOptions{
		Query: "",
	})
	if err == nil {
		t.Error("expected error for empty query, got nil")
	}
}

func TestSearchOptionsInvalidRegex(t *testing.T) {
	_, err := SearchPagesAdvanced("nonexistent_project", SearchOptions{
		Query: "[invalid",
		Regex: true,
	})
	if err == nil {
		t.Error("expected error for invalid regex, got nil")
	}
}

func TestTrimSpaceInMatches(t *testing.T) {
	matches := findPlainTextMatches("Title", "  spaced line  \nnormal line", "spaced", true)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Line != "spaced line" {
		t.Errorf("expected trimmed line, got %q", matches[0].Line)
	}
}

func TestLineNumZeroForTitle(t *testing.T) {
	matches := findPlainTextMatches("Matching Title", "no match here", "Matching", false)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].LineNum != 0 {
		t.Errorf("expected LineNum=0 for title match, got %d", matches[0].LineNum)
	}
}

func TestLineNumOneIndexedForContent(t *testing.T) {
	matches := findPlainTextMatches("No Match", "first\nsecond\nthird", "second", true)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].LineNum != 2 {
		t.Errorf("expected LineNum=2 for second line, got %d", matches[0].LineNum)
	}
}
