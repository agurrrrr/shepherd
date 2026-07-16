package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRule is a test helper: create a file with content under dir.
func writeRule(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// initGitRepo marks dir as a git repo root via a .git directory.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
}

func TestCollectProjectRuleFiles_EmptyWhenNone(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp)
	got := collectProjectRuleFiles(tmp)
	if len(got) != 0 {
		t.Fatalf("expected no rules, got %+v", got)
	}
	if s := buildProjectRulesSection(tmp); s != "" {
		t.Fatalf("expected empty section, got %q", s)
	}
}

func TestCollectProjectRuleFiles_EmptyProjectPath(t *testing.T) {
	if got := collectProjectRuleFiles(""); len(got) != 0 {
		t.Fatalf("expected nil/empty for empty path, got %+v", got)
	}
	if s := buildProjectRulesSection(""); s != "" {
		t.Fatalf("expected empty section for empty path, got %q", s)
	}
}

func TestCollectProjectRuleFiles_HierarchyRootToLeaf(t *testing.T) {
	// repo/
	//   AGENTS.md          (root)
	//   CLAUDE.md
	//   sub/
	//     PROJECT.md       (leaf)
	//     deep/            (cwd)
	root := t.TempDir()
	initGitRepo(t, root)
	writeRule(t, root, "AGENTS.md", "ROOT_AGENTS_MARKER")
	writeRule(t, root, "CLAUDE.md", "ROOT_CLAUDE_MARKER")
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRule(t, sub, "PROJECT.md", "LEAF_PROJECT_MARKER")
	deep := filepath.Join(sub, "deep")
	if err := os.Mkdir(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	files := collectProjectRuleFiles(deep)
	if len(files) != 3 {
		t.Fatalf("want 3 files, got %d: %+v", len(files), files)
	}
	// Root → leaf order: AGENTS, CLAUDE (same dir, fixed filename order), then PROJECT
	wantMarkers := []string{"ROOT_AGENTS_MARKER", "ROOT_CLAUDE_MARKER", "LEAF_PROJECT_MARKER"}
	for i, m := range wantMarkers {
		if !strings.Contains(files[i].Content, m) {
			t.Errorf("files[%d] missing %q; content=%q path=%s", i, m, files[i].Content, files[i].Path)
		}
	}
	// Filenames order within root dir
	if files[0].Name != "AGENTS.md" || files[1].Name != "CLAUDE.md" || files[2].Name != "PROJECT.md" {
		t.Errorf("unexpected name order: %s, %s, %s", files[0].Name, files[1].Name, files[2].Name)
	}
}

func TestCollectProjectRuleFiles_OnlyNamedRuleFiles(t *testing.T) {
	// Must NOT pick up random docs or .claude/rules dumps.
	root := t.TempDir()
	initGitRepo(t, root)
	writeRule(t, root, "AGENTS.md", "KEEP_ME")
	writeRule(t, root, "README.md", "SKIP_README")
	writeRule(t, root, "NOTES.md", "SKIP_NOTES")
	rulesDir := filepath.Join(root, ".claude", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRule(t, rulesDir, "style.md", "SKIP_RULES_DIR")

	files := collectProjectRuleFiles(root)
	if len(files) != 1 {
		t.Fatalf("want only AGENTS.md, got %d: %+v", len(files), files)
	}
	if files[0].Name != "AGENTS.md" || !strings.Contains(files[0].Content, "KEEP_ME") {
		t.Fatalf("unexpected file: %+v", files[0])
	}
}

func TestCollectProjectRuleFiles_DedupSamePath(t *testing.T) {
	// Same absolute path must appear once. Symlink case is best-effort via EvalSymlinks.
	root := t.TempDir()
	initGitRepo(t, root)
	writeRule(t, root, "AGENTS.md", "ONCE")
	// Collecting from root should yield one entry even if filenames list is stable.
	files := collectProjectRuleFiles(root)
	count := 0
	for _, f := range files {
		if f.Name == "AGENTS.md" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("AGENTS.md appeared %d times", count)
	}
}

func TestCollectProjectRuleFiles_NoGitScansOnlyStart(t *testing.T) {
	// Outside a git repo: only projectPath itself is scanned (not parent dirs).
	parent := t.TempDir()
	writeRule(t, parent, "AGENTS.md", "PARENT_SHOULD_NOT_APPEAR")
	child := filepath.Join(parent, "proj")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRule(t, child, "PROJECT.md", "CHILD_ONLY")

	files := collectProjectRuleFiles(child)
	// Without .git, walk stops at filesystem climb... actually our code walks up
	// until isRepoRoot OR filesystem root. Without .git it walks ALL the way to /.
	// That could pick up parent AGENTS.md. For "no git" case, grok only scans cwd.
	// Re-check requirement: "repo root 판정은 .git 디렉토리 기준"
	// When no .git found, walking to FS root is dangerous (leaks host home rules).
	// We should only scan start when no .git is found.
	//
	// This test documents expected behavior: only child PROJECT.md, not parent.
	// If implementation walks to FS root, fix it.
	var names []string
	for _, f := range files {
		names = append(names, f.Name+":"+f.Content)
	}
	for _, f := range files {
		if strings.Contains(f.Content, "PARENT_SHOULD_NOT_APPEAR") {
			t.Fatalf("without .git, must not walk into parent dirs; got %v", names)
		}
	}
	if len(files) != 1 || !strings.Contains(files[0].Content, "CHILD_ONLY") {
		t.Fatalf("want only child PROJECT.md, got %v", names)
	}
}

func TestFormatProjectRulesSection_FullUnderCap(t *testing.T) {
	files := []projectRuleFile{
		{Path: "/repo/AGENTS.md", Name: "AGENTS.md", Content: "do X"},
		{Path: "/repo/sub/CLAUDE.md", Name: "CLAUDE.md", Content: "do Y"},
	}
	s := formatProjectRulesSection(files, projectRulesCapBytes)
	if !strings.Contains(s, "do X") || !strings.Contains(s, "do Y") {
		t.Fatalf("full content missing: %s", s)
	}
	if !strings.Contains(s, "## From: /repo/AGENTS.md") {
		t.Fatalf("missing path header: %s", s)
	}
	if strings.Contains(s, "full content not injected") {
		t.Fatalf("should not use summary under cap: %s", s)
	}
}

func TestFormatProjectRulesSection_CapFallbackSummary(t *testing.T) {
	// One huge file forces summary fallback.
	huge := strings.Repeat("RULE_LINE\n", 2000) // ~20KB
	files := []projectRuleFile{
		{Path: "/repo/AGENTS.md", Name: "AGENTS.md", Content: huge},
		{Path: "/repo/CLAUDE.md", Name: "CLAUDE.md", Content: "small"},
	}
	s := formatProjectRulesSection(files, projectRulesCapBytes)
	if len(s) > projectRulesCapBytes {
		t.Fatalf("section exceeds hard cap: %d > %d", len(s), projectRulesCapBytes)
	}
	if !strings.Contains(s, "hard cap") {
		t.Fatalf("expected cap notice: %s", s)
	}
	if !strings.Contains(s, "read_file") {
		t.Fatalf("expected on-demand read guidance: %s", s)
	}
	if !strings.Contains(s, "AGENTS.md") || !strings.Contains(s, "CLAUDE.md") {
		t.Fatalf("summary must list both files: %s", s)
	}
	// Must NOT dump the huge body.
	if strings.Count(s, "RULE_LINE") > 5 {
		t.Fatalf("huge body leaked into capped section (count=%d)", strings.Count(s, "RULE_LINE"))
	}
}

func TestFormatProjectRulesSection_Empty(t *testing.T) {
	if s := formatProjectRulesSection(nil, projectRulesCapBytes); s != "" {
		t.Fatalf("want empty, got %q", s)
	}
}

func TestBuildProjectRulesSection_Integration(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	writeRule(t, root, "AGENTS.md", "INT_ROOT")
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRule(t, sub, "CLAUDE.md", "INT_LEAF")

	s := buildProjectRulesSection(sub)
	if s == "" {
		t.Fatal("expected non-empty section")
	}
	// Root content before leaf content.
	ri := strings.Index(s, "INT_ROOT")
	li := strings.Index(s, "INT_LEAF")
	if ri < 0 || li < 0 || ri > li {
		t.Fatalf("expected root before leaf; root=%d leaf=%d\n%s", ri, li, s)
	}
	if len(s) > projectRulesCapBytes {
		t.Fatalf("integration section over cap: %d", len(s))
	}
}

func TestBuildProjectRulesSection_SkipsWhitespaceOnly(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	writeRule(t, root, "AGENTS.md", "   \n\t\n  ")
	writeRule(t, root, "PROJECT.md", "real rules")
	files := collectProjectRuleFiles(root)
	if len(files) != 1 || files[0].Name != "PROJECT.md" {
		t.Fatalf("want only PROJECT.md, got %+v", files)
	}
}
