package wiki

import (
	"strings"
	"testing"
)

func TestValidateOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    PartialEditOptions
		wantErr bool
	}{
		{
			name:    "no flags",
			opts:    PartialEditOptions{},
			wantErr: true,
		},
		{
			name:    "append only",
			opts:    PartialEditOptions{Append: "new content"},
			wantErr: false,
		},
		{
			name:    "section without line-text",
			opts:    PartialEditOptions{Section: "My Section"},
			wantErr: true,
		},
		{
			name:    "section with line-text",
			opts:    PartialEditOptions{Section: "My Section", LineText: "new section content"},
			wantErr: false,
		},
		{
			name:    "line without line-text",
			opts:    PartialEditOptions{LineNum: 5},
			wantErr: true,
		},
		{
			name:    "line with line-text",
			opts:    PartialEditOptions{LineNum: 5, LineText: "replaced line"},
			wantErr: false,
		},
		{
			name:    "find without replace",
			opts:    PartialEditOptions{Find: "pattern"},
			wantErr: true,
		},
		{
			name:    "replace without find",
			opts:    PartialEditOptions{Replace: "text"},
			wantErr: true,
		},
		{
			name:    "find and replace",
			opts:    PartialEditOptions{Find: "old", Replace: "new"},
			wantErr: false,
		},
		{
			name:    "multiple modes",
			opts:    PartialEditOptions{Append: "text", Section: "Sec"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAppendContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		text    string
		want    string
	}{
		{
			name:    "empty content",
			content: "",
			text:    "new content",
			want:    "new content",
		},
		{
			name:    "content without trailing newline",
			content: "line1",
			text:    "line2",
			want:    "line1\nline2",
		},
		{
			name:    "content with trailing newline",
			content: "line1\n",
			text:    "line2",
			want:    "line1\nline2",
		},
		{
			name:    "multiline content",
			content: "line1\nline2",
			text:    "line3",
			want:    "line1\nline2\nline3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendContent(tt.content, tt.text)
			if got != tt.want {
				t.Errorf("appendContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReplaceLine(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		lineNum  int
		newText  string
		wantErr  bool
		wantLine string
	}{
		{
			name:     "replace first line",
			content:  "line1\nline2\nline3",
			lineNum:  1,
			newText:  "replaced",
			wantErr:  false,
			wantLine: "replaced",
		},
		{
			name:     "replace middle line",
			content:  "line1\nline2\nline3",
			lineNum:  2,
			newText:  "replaced",
			wantErr:  false,
			wantLine: "replaced",
		},
		{
			name:     "replace last line",
			content:  "line1\nline2\nline3",
			lineNum:  3,
			newText:  "replaced",
			wantErr:  false,
			wantLine: "replaced",
		},
		{
			name:    "line out of range low",
			content: "line1\nline2",
			lineNum: 0,
			newText: "x",
			wantErr: true,
		},
		{
			name:    "line out of range high",
			content: "line1\nline2",
			lineNum: 5,
			newText: "x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceLine(tt.content, tt.lineNum, tt.newText)
			if (err != nil) != tt.wantErr {
				t.Errorf("replaceLine() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				lines := strings.Split(got, "\n")
				if lines[tt.lineNum-1] != tt.wantLine {
					t.Errorf("replaceLine() line[%d] = %q, want %q", tt.lineNum-1, lines[tt.lineNum-1], tt.wantLine)
				}
			}
		})
	}
}

func TestFindAndReplace(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		pattern     string
		replacement string
		wantErr     bool
		wantLine    string
	}{
		{
			name:        "simple match",
			content:     "hello world\nfoo bar\nbaz",
			pattern:     "world",
			replacement: "there",
			wantErr:     false,
			wantLine:    "hello there",
		},
		{
			name:        "regex match",
			content:     "user: alice\nuser: bob",
			pattern:     "alice",
			replacement: "charlie",
			wantErr:     false,
			wantLine:    "user: charlie",
		},
		{
			name:        "no match",
			content:     "hello\nworld",
			pattern:     "notfound",
			replacement: "x",
			wantErr:     true,
		},
		{
			name:        "invalid regex",
			content:     "hello",
			pattern:     "[invalid",
			replacement: "x",
			wantErr:     true,
		},
		{
			name:        "only first match replaced",
			content:     "foo\nfoo\nfoo",
			pattern:     "foo",
			replacement: "bar",
			wantErr:     false,
			wantLine:    "bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findAndReplace(tt.content, tt.pattern, tt.replacement)
			if (err != nil) != tt.wantErr {
				t.Errorf("findAndReplace() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.wantLine != "" {
				lines := strings.Split(got, "\n")
				found := false
				for _, line := range lines {
					if line == tt.wantLine {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("findAndReplace() result does not contain %q, got %q", tt.wantLine, got)
				}
			}
		})
	}
}

func TestReplaceSection(t *testing.T) {
	content := `# Title

## Introduction
This is the intro.

## Details
Some details here.
More details.

## Conclusion
That's it.
`

	tests := []struct {
		name        string
		section     string
		newText     string
		wantErr     bool
		wantContain string
	}{
		{
			name:        "replace intro section",
			section:     "Introduction",
			newText:     "New introduction content.",
			wantErr:     false,
			wantContain: "New introduction content.",
		},
		{
			name:        "replace details section",
			section:     "Details",
			newText:     "Updated details.",
			wantErr:     false,
			wantContain: "Updated details.",
		},
		{
			name:    "section not found",
			section: "NonExistent",
			newText: "x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceSection(content, tt.section, tt.newText)
			if (err != nil) != tt.wantErr {
				t.Errorf("replaceSection() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.wantContain != "" {
				if !strings.Contains(got, tt.wantContain) {
					t.Errorf("replaceSection() result does not contain %q, got\n%s", tt.wantContain, got)
				}
			}
		})
	}
}

func TestLineCount(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name:    "empty",
			content: "",
			want:    1,
		},
		{
			name:    "single line",
			content: "hello",
			want:    1,
		},
		{
			name:    "multiple lines",
			content: "line1\nline2\nline3",
			want:    3,
		},
		{
			name:    "trailing newline",
			content: "line1\nline2\n",
			want:    3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Count(tt.content, "\n") + 1
			if got != tt.want {
				t.Errorf("lineCount(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}
