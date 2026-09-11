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
		want        string
	}{
		{
			name:        "simple match",
			content:     "hello world\nfoo bar\nbaz",
			pattern:     "world",
			replacement: "there",
			wantErr:     false,
			want:        "hello there\nfoo bar\nbaz",
		},
		{
			name:        "regex match",
			content:     "user: alice\nuser: bob",
			pattern:     "alice",
			replacement: "charlie",
			wantErr:     false,
			want:        "user: charlie\nuser: bob",
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
			want:        "bar\nfoo\nfoo",
		},
		{
			name:        "multiline pattern matches across lines",
			content:     "keep one\nold line a\nold line b\nkeep two",
			pattern:     "old line a\nold line b",
			replacement: "new line",
			wantErr:     false,
			want:        "keep one\nnew line\nkeep two",
		},
		{
			name:        "multiline replacement adds lines",
			content:     "a\nb",
			pattern:     "b",
			replacement: "b1\nb2\nb3",
			wantErr:     false,
			want:        "a\nb1\nb2\nb3",
		},
		{
			name:        "empty replacement deletes the match",
			content:     "a\njunk line\nc",
			pattern:     "\njunk line",
			replacement: "",
			wantErr:     false,
			want:        "a\nc",
		},
		{
			name:        "backreference keeps the rest of the line",
			content:     "prefix=old;suffix=1",
			pattern:     "prefix=([^;]*);",
			replacement: "prefix=new;",
			wantErr:     false,
			want:        "prefix=new;suffix=1",
		},
		{
			name:        "capture group backreference in replacement",
			content:     "date: 2026-01-02",
			pattern:     "date: (\\d{4})-(\\d{2})-(\\d{2})",
			replacement: "date: $3/$2/$1",
			wantErr:     false,
			want:        "date: 02/01/2026",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findAndReplace(tt.content, tt.pattern, tt.replacement)
			if (err != nil) != tt.wantErr {
				t.Errorf("findAndReplace() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("findAndReplace() = %q, want %q", got, tt.want)
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

	subContent := `# Title

## Section A
body A

### Sub A1
sub body

## Section B
body B
`

	tests := []struct {
		name    string
		content string
		section string
		newText string
		want    string
		wantErr string
	}{
		{
			name:    "replace middle section keeps adjacent sections",
			content: content,
			section: "Details",
			newText: "Updated details.",
			want: `# Title

## Introduction
This is the intro.

## Details

Updated details.

## Conclusion
That's it.
`,
		},
		{
			name:    "replace last section",
			content: content,
			section: "Conclusion",
			newText: "New conclusion.",
			want:    "# Title\n\n## Introduction\nThis is the intro.\n\n## Details\nSome details here.\nMore details.\n\n## Conclusion\n\nNew conclusion.",
		},
		{
			name:    "heading prefix in section name is accepted",
			content: content,
			section: "## Details",
			newText: "Updated details.",
			want: `# Title

## Introduction
This is the intro.

## Details

Updated details.

## Conclusion
That's it.
`,
		},
		{
			name:    "empty line text clears the section body",
			content: content,
			section: "Details",
			newText: "",
			want: `# Title

## Introduction
This is the intro.

## Details

## Conclusion
That's it.
`,
		},
		{
			name:    "deeper subsections are part of the replaced range",
			content: subContent,
			section: "Section A",
			newText: "new A",
			want:    "# Title\n\n## Section A\n\nnew A\n\n## Section B\nbody B\n",
		},
		{
			name:    "section not found lists available sections",
			content: content,
			section: "NonExistent",
			newText: "x",
			wantErr: `section "NonExistent" not found in page (available sections: Title, Introduction, Details, Conclusion)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceSection(tt.content, tt.section, tt.newText)
			if (err != nil) != (tt.wantErr != "") {
				t.Fatalf("replaceSection() error = %v, wantErr %q", err, tt.wantErr)
			}
			if tt.wantErr != "" {
				if err.Error() != tt.wantErr {
					t.Errorf("replaceSection() error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if got != tt.want {
				t.Errorf("replaceSection() = %q, want %q", got, tt.want)
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
