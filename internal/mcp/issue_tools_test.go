package mcp

import (
	"context"
	"strings"
	"testing"

	entIssue "github.com/agurrrrr/shepherd/ent/issue"
)

// The DB-free validation paths (STEP 7b) stay below; #596 added in-memory DB
// coverage for the two issue_list shapes (single project vs all projects).

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

// TestHandleIssueList_NoProjectNameListsAllProjects replaces the old
// "project_name is required" guard: #596 made project_name optional and an
// omitted value now means "every visible project", with a [project] prefix.
func TestHandleIssueList_NoProjectNameListsAllProjects(t *testing.T) {
	client := withMCPTestDB(t)
	ctx := context.Background()

	visible := client.Project.Create().SetName("tangbisil").SetPath("/ws/tangbisil").SaveX(ctx)
	hidden := client.Project.Create().SetName("vault").SetPath("/ws/vault").SetHidden(true).SaveX(ctx)

	client.Issue.Create().SetTitle("visible bug").SetType(entIssue.TypeBug).
		SetProject(visible).SaveX(ctx)
	client.Issue.Create().SetTitle("hidden secret").SetProject(hidden).SaveX(ctx)

	out, err := handleIssueList(map[string]interface{}{})
	if err != nil {
		t.Fatalf("issue_list without project_name: %v", err)
	}
	if !strings.Contains(out, "[tangbisil] #1 [bug/todo] visible bug") {
		t.Fatalf("missing [project] prefixed row:\n%s", out)
	}
	if strings.Contains(out, "hidden secret") {
		t.Fatalf("hidden project must be excluded by default:\n%s", out)
	}

	out, err = handleIssueList(map[string]interface{}{"include_hidden": true})
	if err != nil {
		t.Fatalf("issue_list with include_hidden: %v", err)
	}
	if !strings.Contains(out, "[vault] #2 [feature/todo] hidden secret") {
		t.Fatalf("include_hidden must bring hidden projects in:\n%s", out)
	}
}

// TestHandleIssueList_SingleProjectShapeUnchanged pins the original per-project
// output format: no [project] prefix, same header wording.
func TestHandleIssueList_SingleProjectShapeUnchanged(t *testing.T) {
	client := withMCPTestDB(t)
	ctx := context.Background()

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	other := client.Project.Create().SetName("other").SetPath("/ws/other").SaveX(ctx)
	client.Issue.Create().SetTitle("bori task").SetProject(p).SaveX(ctx)
	client.Issue.Create().SetTitle("foreign task").SetProject(other).SaveX(ctx)

	out, err := handleIssueList(map[string]interface{}{"project_name": "bori"})
	if err != nil {
		t.Fatalf("issue_list with project_name: %v", err)
	}
	if !strings.Contains(out, `이슈 목록 "bori" (1/1, page 1/1):`) {
		t.Fatalf("header changed:\n%s", out)
	}
	if !strings.Contains(out, "#1 [feature/todo] bori task (task 0개)") {
		t.Fatalf("row format changed:\n%s", out)
	}
	if strings.Contains(out, "foreign task") || strings.Contains(out, "[bori]") {
		t.Fatalf("single-project output must stay scoped and unprefixed:\n%s", out)
	}

	if _, err := handleIssueList(map[string]interface{}{"project_name": "missing"}); err == nil ||
		!strings.Contains(err.Error(), `project "missing" not found`) {
		t.Fatalf("unknown project error wording changed: %v", err)
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
	// #596: project_name must stop being required for issue_list, and the tool
	// description has to say what omitting it means.
	for _, req := range list.InputSchema.Required {
		if req == "project_name" {
			t.Fatal("issue_list must not require project_name anymore")
		}
	}
	if !strings.Contains(list.Description, "omit project_name to list issues across all projects") {
		t.Fatalf("issue_list description missing the all-projects contract: %s", list.Description)
	}
	// The mutating tools keep project_name required on purpose: issue IDs are
	// global, so requiring the project is what stops cross-project edits.
	for _, tool := range []*Tool{upsert, execute} {
		found := false
		for _, req := range tool.InputSchema.Required {
			if req == "project_name" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s must still require project_name", tool.Name)
		}
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
