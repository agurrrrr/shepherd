package mcp

import (
	"strings"
	"testing"
)

// DB-free validation paths only (STEP 7b wiki). Success paths need a live DB.

func TestHandleWikiEdit_InvalidMode(t *testing.T) {
	_, err := handleWikiEdit(map[string]interface{}{
		"project_name": "p",
		"slug":         "s",
		"mode":         "typo",
	})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("expected invalid mode error, got: %v", err)
	}
}

func TestHandleWikiEdit_AppendRequiresText(t *testing.T) {
	// empty text → PartialEditOptions.Append empty → validate fails before DB
	_, err := handleWikiEdit(map[string]interface{}{
		"project_name": "p",
		"slug":         "s",
		"mode":         "append",
		// no text
	})
	if err == nil {
		t.Fatal("expected error when append mode has no text")
	}

	_, err = handleWikiEdit(map[string]interface{}{
		"project_name": "p",
		"slug":         "s",
		"mode":         "append",
		"text":         "",
	})
	if err == nil {
		t.Fatal("expected error when append mode has empty text")
	}
}

func TestHandleWikiEdit_SectionRequiresLineText(t *testing.T) {
	_, err := handleWikiEdit(map[string]interface{}{
		"project_name": "p",
		"slug":         "s",
		"mode":         "section",
		"section":      "Background",
		// no line_text
	})
	if err == nil {
		t.Fatal("expected error when section mode has no line_text")
	}
	if !strings.Contains(err.Error(), "line-text") && !strings.Contains(err.Error(), "line_text") {
		// validate() uses CLI flag name "--line-text"
		if !strings.Contains(err.Error(), "required") {
			t.Fatalf("expected line_text-related error, got: %v", err)
		}
	}
}

func TestHandleWikiEdit_RequiresProjectSlugMode(t *testing.T) {
	cases := []map[string]interface{}{
		{"slug": "s", "mode": "append", "text": "x"},                     // no project
		{"project_name": "p", "mode": "append", "text": "x"},             // no slug
		{"project_name": "p", "slug": "s"},                               // no mode
		{"project_name": "", "slug": "s", "mode": "append", "text": "x"}, // empty project
		{"project_name": "p", "slug": "", "mode": "append", "text": "x"}, // empty slug
		{"project_name": "p", "slug": "s", "mode": ""},                   // empty mode
	}
	for i, args := range cases {
		_, err := handleWikiEdit(args)
		if err == nil {
			t.Errorf("case %d: expected error for missing project_name/slug/mode, args=%v", i, args)
		}
	}
}

func TestHandleWikiCreate_RequiresFields(t *testing.T) {
	_, err := handleWikiCreate(map[string]interface{}{
		"project_name": "p",
		"slug":         "s",
		"title":        "T",
		// empty content rejected by MCP handler
		"content": "",
	})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	if !strings.Contains(err.Error(), "content") && !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required-fields error, got: %v", err)
	}

	_, err = handleWikiCreate(map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestGetWikiToolsList_WriteToolsSchema(t *testing.T) {
	tools := getWikiToolsList()
	var create, edit *Tool
	for i := range tools {
		switch tools[i].Name {
		case "wiki_create":
			create = &tools[i]
		case "wiki_edit":
			edit = &tools[i]
		}
	}
	if create == nil || edit == nil {
		t.Fatal("wiki_create and wiki_edit must be in getWikiToolsList")
	}
	if !strings.Contains(create.Description, "wiki_edit") {
		t.Fatalf("wiki_create description must mention wiki_edit for overwrite ban: %s", create.Description)
	}
	if !strings.Contains(create.Description, "덮어쓰기") && !strings.Contains(create.Description, "생성 전용") {
		t.Fatalf("wiki_create description missing create-only contract: %s", create.Description)
	}
	modeProp := edit.InputSchema.Properties["mode"]
	if len(modeProp.Enum) != 4 {
		t.Fatalf("wiki_edit mode enum want 4 values, got %v", modeProp.Enum)
	}
	wantModes := map[string]bool{"append": true, "section": true, "line": true, "find_replace": true}
	for _, m := range modeProp.Enum {
		if !wantModes[m] {
			t.Fatalf("unexpected mode enum value %q", m)
		}
	}
	if !strings.Contains(edit.Description, "모드 하나만") && !strings.Contains(edit.Description, "한 호출") {
		t.Fatalf("wiki_edit description missing single-mode contract: %s", edit.Description)
	}
	// required fields
	req := map[string]bool{}
	for _, r := range create.InputSchema.Required {
		req[r] = true
	}
	for _, need := range []string{"project_name", "slug", "title", "content"} {
		if !req[need] {
			t.Errorf("wiki_create Required missing %q", need)
		}
	}
	reqEdit := map[string]bool{}
	for _, r := range edit.InputSchema.Required {
		reqEdit[r] = true
	}
	for _, need := range []string{"project_name", "slug", "mode"} {
		if !reqEdit[need] {
			t.Errorf("wiki_edit Required missing %q", need)
		}
	}
}

func TestListWikiToolDefs_IncludesWriteTools(t *testing.T) {
	// wiki tools are folded via getWikiToolsList in handleToolsList (server.go),
	// not ListCoreToolDefs — same surface for stdio + ListWikiToolDefs export.
	names := map[string]bool{}
	for _, tool := range ListWikiToolDefs() {
		names[tool.Name] = true
	}
	for _, want := range []string{"wiki_create", "wiki_edit", "wiki_read_page", "wiki_list_pages", "wiki_search"} {
		if !names[want] {
			t.Errorf("ListWikiToolDefs missing %q", want)
		}
	}
}
