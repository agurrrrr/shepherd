package wiki

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessTemplate(t *testing.T) {
	tests := []struct {
		name       string
		template   string
		data       map[string]string
		wantResult string
	}{
		{
			name:       "all variables replaced",
			template:   "# {{title}}\n\n{{content}}",
			data:       map[string]string{"title": "My Page", "content": "Hello"},
			wantResult: "# My Page\n\nHello",
		},
		{
			name:       "slug variable",
			template:   "slug: {{slug}}",
			data:       map[string]string{"slug": "my-page"},
			wantResult: "slug: my-page",
		},
		{
			name:       "date variable",
			template:   "created: {{date}}",
			data:       map[string]string{"date": "2025-01-15"},
			wantResult: "created: 2025-01-15",
		},
		{
			name:       "unknown variable preserved",
			template:   "{{title}} {{unknown}}",
			data:       map[string]string{"title": "Hello"},
			wantResult: "Hello {{unknown}}",
		},
		{
			name:       "empty data preserves variables",
			template:   "# {{title}}",
			data:       map[string]string{},
			wantResult: "# {{title}}",
		},
		{
			name:       "multiple occurrences",
			template:   "{{title}} - {{title}}",
			data:       map[string]string{"title": "A"},
			wantResult: "A - A",
		},
		{
			name:       "no variables",
			template:   "plain text",
			data:       map[string]string{"title": "ignored"},
			wantResult: "plain text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProcessTemplate(tt.template, tt.data)
			if got != tt.wantResult {
				t.Errorf("ProcessTemplate() = %q, want %q", got, tt.wantResult)
			}
		})
	}
}

func TestNewTemplateData(t *testing.T) {
	data := NewTemplateData("Test Title", "test-slug")

	if data["title"] != "Test Title" {
		t.Errorf("title = %q, want %q", data["title"], "Test Title")
	}
	if data["slug"] != "test-slug" {
		t.Errorf("slug = %q, want %q", data["slug"], "test-slug")
	}
	if data["content"] != "" {
		t.Errorf("content should be empty, got %q", data["content"])
	}
	if !strings.HasPrefix(data["date"], "20") {
		t.Errorf("date should start with year, got %q", data["date"])
	}
}

func TestNewTemplateDataWithContent(t *testing.T) {
	data := NewTemplateDataWithContent("Title", "slug", "Some content")

	if data["content"] != "Some content" {
		t.Errorf("content = %q, want %q", data["content"], "Some content")
	}
	if data["title"] != "Title" {
		t.Errorf("title = %q, want %q", data["title"], "Title")
	}
}

func TestResolveTemplateFallsBackToBuiltin(t *testing.T) {
	tpl := ResolveTemplate("nonexistent-project", "default")
	if !strings.Contains(tpl, "{{title}}") {
		t.Errorf("expected default template to contain {{title}}, got %q", tpl)
	}
}

func TestResolveTemplateEmptyNameUsesDefault(t *testing.T) {
	tpl := ResolveTemplate("nonexistent-project", "")
	if !strings.Contains(tpl, "{{title}}") {
		t.Errorf("expected default template for empty name, got %q", tpl)
	}
}

func TestResolveTemplateProjectFile(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tplDir, 0755); err != nil {
		t.Fatal(err)
	}

	customTpl := "# Custom: {{title}}\nOverride!"
	if err := os.WriteFile(filepath.Join(tplDir, "custom.md"), []byte(customTpl), 0644); err != nil {
		t.Fatal(err)
	}

	origFn := ProjectTemplatesDir
	ProjectTemplatesDir = func(projectName string) string {
		return tplDir
	}
	defer func() { ProjectTemplatesDir = origFn }()

	tpl := ResolveTemplate("test-project", "custom")
	if tpl != customTpl {
		t.Errorf("ResolveTemplate() = %q, want %q", tpl, customTpl)
	}
}

func TestResolveTemplateProjectFileTmplExtension(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tplDir, 0755); err != nil {
		t.Fatal(err)
	}

	customTpl := ".tmpl content: {{title}}"
	if err := os.WriteFile(filepath.Join(tplDir, "tmpl-test.tmpl"), []byte(customTpl), 0644); err != nil {
		t.Fatal(err)
	}

	origFn := ProjectTemplatesDir
	ProjectTemplatesDir = func(projectName string) string {
		return tplDir
	}
	defer func() { ProjectTemplatesDir = origFn }()

	tpl := ResolveTemplate("test-project", "tmpl-test")
	if tpl != customTpl {
		t.Errorf("ResolveTemplate() = %q, want %q", tpl, customTpl)
	}
}

func TestLoadProjectTemplates(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tplDir, 0755); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(tplDir, "my-template.md"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tplDir, "another.tmpl"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tplDir, "readme.txt"), []byte("test"), 0644)

	origFn := ProjectTemplatesDir
	ProjectTemplatesDir = func(projectName string) string {
		return tplDir
	}
	defer func() { ProjectTemplatesDir = origFn }()

	names := LoadProjectTemplates("test-project")
	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}

	if !found["my-template"] {
		t.Error("expected my-template in loaded templates")
	}
	if !found["another"] {
		t.Error("expected another in loaded templates")
	}
	if found["readme"] {
		t.Error("readme.txt should not be listed as a template")
	}
}

func TestLoadProjectTemplatesNonExistentDir(t *testing.T) {
	origFn := ProjectTemplatesDir
	ProjectTemplatesDir = func(projectName string) string {
		return "/nonexistent/path/templates"
	}
	defer func() { ProjectTemplatesDir = origFn }()

	names := LoadProjectTemplates("test-project")
	if names != nil {
		t.Errorf("expected nil for non-existent dir, got %v", names)
	}
}

func TestBuiltinTemplateNames(t *testing.T) {
	names := BuiltinTemplateNames()
	if !names["default"] {
		t.Error("expected 'default' in builtin template names")
	}
	if !names["new_page"] {
		t.Error("expected 'new_page' in builtin template names")
	}
	if !names["architecture"] {
		t.Error("expected 'architecture' in builtin template names")
	}
	if len(names) < 4 {
		t.Errorf("expected at least 4 builtin templates, got %d", len(names))
	}
}

func TestProcessTemplateWithNewPageTemplate(t *testing.T) {
	tpl := ResolveTemplate("nonexistent", "new_page")
	data := NewTemplateDataWithContent("My New Page", "my-new-page", "Body text here")
	result := ProcessTemplate(tpl, data)

	if !strings.Contains(result, "title: My New Page") {
		t.Error("expected title in output")
	}
	if !strings.Contains(result, "Body text here") {
		t.Error("expected content in output")
	}
	if !strings.HasPrefix(result, "---") {
		t.Error("expected frontmatter in output")
	}
}
