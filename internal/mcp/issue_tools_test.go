package mcp

import (
	"strings"
	"testing"
)

// DB-free validation paths only (STEP 7b). Success paths need a live DB and are out of P0.

func TestHandleIssueUpsert_CreateRequiresTitle(t *testing.T) {
	_, err := handleIssueUpsert(map[string]interface{}{
		"project_name": "p",
		// no id, no title
	})
	if err == nil {
		t.Fatal("expected error when creating without title")
	}
	if !strings.Contains(err.Error(), "title") {
		t.Fatalf("expected title-related error, got: %v", err)
	}
}

func TestHandleIssueList_RequiresProjectName(t *testing.T) {
	_, err := handleIssueList(map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for empty project_name")
	}
	if !strings.Contains(err.Error(), "project_name") {
		t.Fatalf("expected project_name error, got: %v", err)
	}
}

func TestHandleIssueGet_RequiresProjectName(t *testing.T) {
	_, err := handleIssueGet(map[string]interface{}{
		"id": float64(1),
	})
	if err == nil {
		t.Fatal("expected error for empty project_name")
	}
	if !strings.Contains(err.Error(), "project_name") {
		t.Fatalf("expected project_name error, got: %v", err)
	}
}

func TestHandleIssueGet_RequiresPositiveID(t *testing.T) {
	_, err := handleIssueGet(map[string]interface{}{
		"project_name": "p",
		"id":           float64(0),
	})
	if err == nil {
		t.Fatal("expected error for id <= 0")
	}
}

func TestHandleIssueExecute_RequiresPositiveID(t *testing.T) {
	_, err := handleIssueExecute(map[string]interface{}{
		"project_name": "p",
		"id":           float64(0),
	})
	if err == nil {
		t.Fatal("expected error for id <= 0")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Fatalf("expected id-related error, got: %v", err)
	}
}

func TestToInt(t *testing.T) {
	if got := toInt(float64(42)); got != 42 {
		t.Fatalf("toInt(float64(42)) = %d; want 42", got)
	}
	if got := toInt(7); got != 7 {
		t.Fatalf("toInt(7) = %d; want 7", got)
	}
	if got := toInt("42"); got != 42 {
		t.Fatalf("toInt(\"42\") = %d; want 42", got)
	}
	if got := toInt("nope"); got != 0 {
		t.Fatalf("toInt(string) = %d; want 0", got)
	}
	if got := toInt(nil); got != 0 {
		t.Fatalf("toInt(nil) = %d; want 0", got)
	}
}

func TestGetIssueToolsList_NoDelete(t *testing.T) {
	for _, tool := range getIssueToolsList() {
		if tool.Name == "issue_delete" {
			t.Fatal("issue_delete must not be registered")
		}
		if strings.Contains(tool.Name, "delete") {
			t.Fatalf("unexpected delete tool: %s", tool.Name)
		}
	}
	// Enum fields present on status/type for list + upsert
	tools := getIssueToolsList()
	var list, upsert, execute *Tool
	for i := range tools {
		tool := &tools[i]
		switch tool.Name {
		case "issue_list":
			list = tool
		case "issue_upsert":
			upsert = tool
		case "issue_execute":
			execute = tool
		}
	}
	if list == nil || upsert == nil {
		t.Fatal("issue_list and issue_upsert must be defined")
	}
	if len(list.InputSchema.Properties["status"].Enum) != 5 {
		t.Fatalf("issue_list status enum: got %v", list.InputSchema.Properties["status"].Enum)
	}
	if len(list.InputSchema.Properties["type"].Enum) != 3 {
		t.Fatalf("issue_list type enum: got %v", list.InputSchema.Properties["type"].Enum)
	}
	if !strings.Contains(upsert.Description, "id 없으면") {
		t.Fatalf("issue_upsert description missing create/update contract: %s", upsert.Description)
	}
	if execute == nil {
		t.Fatal("issue_execute must be defined")
	}
	for _, needle := range []string{"큐", "in_progress", "중복"} {
		if !strings.Contains(execute.Description, needle) {
			t.Fatalf("issue_execute description missing %q: %s", needle, execute.Description)
		}
	}
}

func TestListCoreToolDefs_IncludesIssueTools(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range ListCoreToolDefs() {
		names[tool.Name] = true
	}
	for _, want := range []string{"issue_list", "issue_get", "issue_upsert", "issue_execute"} {
		if !names[want] {
			t.Errorf("ListCoreToolDefs missing %q", want)
		}
	}
	if names["issue_delete"] {
		t.Error("ListCoreToolDefs must not include issue_delete")
	}
}
