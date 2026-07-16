package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// projectRulesCapBytes is the hard upper bound on injected project-rule content.
// Without this, large AGENTS.md/CLAUDE.md files consume the local context window
// and trigger early handoff. Do not raise without measuring context impact.
const projectRulesCapBytes = 8 * 1024

// Only these exact basenames are loaded (no doc-folder dumps, no .claude/rules).
var projectRuleFilenames = []string{"AGENTS.md", "CLAUDE.md", "PROJECT.md"}

// projectRuleFile is one discovered rule file ready for injection.
type projectRuleFile struct {
	Path    string // absolute path (dedup key)
	Name    string // basename
	Content string
}

// buildProjectRulesSection walks projectPath → repo root, collects
// AGENTS.md / CLAUDE.md / PROJECT.md (root → leaf order), and formats them
// for the system prompt with a hard byte cap.
// Returns "" when nothing is found or projectPath is empty.
func buildProjectRulesSection(projectPath string) string {
	if projectPath == "" {
		return ""
	}
	files := collectProjectRuleFiles(projectPath)
	return formatProjectRulesSection(files, projectRulesCapBytes)
}

// collectProjectRuleFiles finds rule files from projectPath up to the repo root
// (directory containing .git), ordered root → leaf. Paths are deduplicated by
// cleaned absolute path. When no .git is found, only projectPath is scanned
// (never walk to filesystem root — that would leak unrelated host rules).
func collectProjectRuleFiles(projectPath string) []projectRuleFile {
	absStart, err := filepath.Abs(projectPath)
	if err != nil {
		return nil
	}
	info, err := os.Stat(absStart)
	if err != nil || !info.IsDir() {
		return nil
	}

	// Build directory chain root → leaf.
	var chain []string
	if root, ok := findRepoRoot(absStart); ok {
		// leaf → root, then reverse.
		cur := absStart
		for {
			chain = append(chain, cur)
			if cur == root {
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
		for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
			chain[i], chain[j] = chain[j], chain[i]
		}
	} else {
		// No git repo: only the project path itself.
		chain = []string{absStart}
	}

	seen := make(map[string]struct{})
	var out []projectRuleFile
	for _, dir := range chain {
		for _, name := range projectRuleFilenames {
			p := filepath.Join(dir, name)
			// Resolve to absolute clean path for dedup (symlink-safe best-effort).
			abs, err := filepath.Abs(p)
			if err != nil {
				continue
			}
			if canon, err := filepath.EvalSymlinks(abs); err == nil {
				abs = canon
			}
			abs = filepath.Clean(abs)
			if _, ok := seen[abs]; ok {
				continue
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				continue // missing or unreadable — skip
			}
			// Skip empty / whitespace-only files (no value, still cost headers).
			content := string(data)
			if strings.TrimSpace(content) == "" {
				seen[abs] = struct{}{}
				continue
			}
			seen[abs] = struct{}{}
			out = append(out, projectRuleFile{
				Path:    abs,
				Name:    name,
				Content: content,
			})
		}
	}
	return out
}

// findRepoRoot walks start upward until a directory containing .git is found.
// Returns ("", false) when no git root exists above start.
func findRepoRoot(start string) (string, bool) {
	cur := start
	for {
		if isRepoRoot(cur) {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

// isRepoRoot reports whether dir is a git repository root (.git dir or file).
func isRepoRoot(dir string) bool {
	gitPath := filepath.Join(dir, ".git")
	st, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	// Normal repo: .git is a directory. Worktree/submodule: .git is a file.
	return st.IsDir() || st.Mode().IsRegular()
}

// formatProjectRulesSection builds the prompt section. When the full body would
// exceed capBytes, falls back to a path summary + on-demand read_file guidance
// (and hard-truncates that fallback so the cap is never violated).
func formatProjectRulesSection(files []projectRuleFile, capBytes int) string {
	if len(files) == 0 {
		return ""
	}
	if capBytes <= 0 {
		capBytes = projectRulesCapBytes
	}

	full := formatProjectRulesFull(files)
	if len(full) <= capBytes {
		return full
	}
	return formatProjectRulesCapped(files, capBytes)
}

func formatProjectRulesFull(files []projectRuleFile) string {
	var b strings.Builder
	b.WriteString("[Project Rules — ordered from repo root to current directory; deeper files take precedence on conflicts]\n")
	for _, f := range files {
		b.WriteString("\n## From: ")
		b.WriteString(f.Path)
		b.WriteByte('\n')
		b.WriteString(strings.TrimRight(f.Content, "\n"))
		b.WriteByte('\n')
	}
	b.WriteString("\nFollow these instructions exactly. When working in subdirectories not listed above, check for additional project instruction files (AGENTS.md, CLAUDE.md, PROJECT.md).")
	return b.String()
}

// formatProjectRulesCapped is used when full injection would exceed the hard
// cap. Prefer a compact inventory + on-demand read guidance over mid-document
// truncation noise. The returned string is always ≤ capBytes.
func formatProjectRulesCapped(files []projectRuleFile, capBytes int) string {
	var list strings.Builder
	list.WriteString("[Project Rules — hard cap ")
	list.WriteString(fmt.Sprintf("%d bytes", capBytes))
	list.WriteString("; full content not injected to protect local context]\n")
	list.WriteString("Found rule files (use read_file for full content when needed):\n")
	for _, f := range files {
		list.WriteString(fmt.Sprintf("- %s (%d bytes) — %s\n", f.Name, len(f.Content), f.Path))
	}
	list.WriteString("Do not assume rules beyond this inventory; read the file(s) you need with read_file.")

	s := list.String()
	if len(s) <= capBytes {
		return s
	}
	// Extreme case: even the summary exceeds cap (huge paths). Hard truncate.
	if capBytes < 32 {
		return s[:capBytes]
	}
	truncNote := "\n…[truncated]"
	keep := capBytes - len(truncNote)
	if keep < 0 {
		return s[:capBytes]
	}
	return s[:keep] + truncNote
}
