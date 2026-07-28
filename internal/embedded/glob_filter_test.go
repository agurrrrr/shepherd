package embedded

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompileGlobFilter(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		// paths that must match (slash-normalized relative form)
		match []string
		// paths that must not match
		noMatch []string
	}{
		{
			name:    "star go",
			pattern: "*.go",
			match:   []string{"main.go", "foo.go"},
			noMatch: []string{"main.md", "internal/main.go"}, // * does not cross /
		},
		{
			name:    "double-star go",
			pattern: "**/*.go",
			match:   []string{"main.go", "internal/main.go", "a/b/c.go"},
			noMatch: []string{"main.md", "internal/x.md"},
		},
		{
			name:    "nested test go",
			pattern: "internal/**/*_test.go",
			match:   []string{"internal/foo_test.go", "internal/pkg/bar_test.go"},
			noMatch: []string{"foo_test.go", "internal/foo.go", "cmd/foo_test.go"},
		},
		{
			name:    "md only",
			pattern: "*.md",
			match:   []string{"README.md"},
			noMatch: []string{"README.go", "docs/README.md"},
		},
		{
			name:    "empty matches all",
			pattern: "",
			match:   []string{"anything.go", "a/b/c"},
		},
		{
			name:    "backslash input normalized",
			pattern: `**\*.go`,
			match:   []string{"main.go", "internal/main.go"},
			noMatch: []string{"main.md"},
		},
		{
			name:    "dot is literal",
			pattern: "*.go",
			match:   []string{"x.go"},
			// Without QuoteMeta, `.` would match any char and "xXgo" would pass.
			noMatch: []string{"xXgo", "x/go"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			re := compileGlobFilter(tc.pattern)
			for _, p := range tc.match {
				if !re.MatchString(p) {
					t.Errorf("pattern %q should match %q (re=%s)", tc.pattern, p, re.String())
				}
			}
			for _, p := range tc.noMatch {
				if re.MatchString(p) {
					t.Errorf("pattern %q should NOT match %q (re=%s)", tc.pattern, p, re.String())
				}
			}
		})
	}
}

// Windows-specific: filepath.Clean turns **/*.go into **\*.go. The filter must
// still compile to a useful expression rather than falling back to .*.
func TestCompileGlobFilterWindowsClean(t *testing.T) {
	// Simulate what Clean produces on Windows regardless of the host OS.
	cleaned := filepath.FromSlash("**/*.go")
	if runtime.GOOS != "windows" {
		// Force the Windows-style separator form the bug depended on.
		cleaned = `**\*.go`
	}
	re := compileGlobFilter(cleaned)
	if re.String() == ".*" || re.String() == "^.*$" {
		t.Fatalf("glob filter silently fell back to match-all for %q; re=%s", cleaned, re.String())
	}
	if !re.MatchString("internal/foo.go") {
		t.Errorf("expected %q filter to match internal/foo.go; re=%s", cleaned, re.String())
	}
	if re.MatchString("internal/foo.md") {
		t.Errorf("expected %q filter NOT to match internal/foo.md; re=%s", cleaned, re.String())
	}
}

func TestGlobToRegexpAnchored(t *testing.T) {
	re, err := globToRegexp("*.md")
	if err != nil {
		t.Fatal(err)
	}
	if re.MatchString("README.mdx") {
		t.Error("*.md must not match README.mdx (anchor + literal dot)")
	}
	if !re.MatchString("README.md") {
		t.Error("*.md must match README.md")
	}
}
