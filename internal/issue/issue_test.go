package issue

import (
	"strings"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
)

func TestCreateValidation(t *testing.T) {
	if _, err := Create(CreateInput{Title: "x"}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected project required, got %v", err)
	}
	if _, err := Create(CreateInput{Project: "p"}); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("expected title required, got %v", err)
	}
	if _, err := Create(CreateInput{Project: "p", Title: "t", Type: "nope"}); err == nil || !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("expected invalid type, got %v", err)
	}
}

func TestUpdateValidation(t *testing.T) {
	// Empty project fails before DB lookup.
	if _, err := Update("", 1, UpdateInput{}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected project required, got %v", err)
	}
	if _, err := Update("p", 0, UpdateInput{}); err == nil || !strings.Contains(err.Error(), "invalid issue id") {
		t.Fatalf("expected invalid id, got %v", err)
	}
}

func TestListValidation(t *testing.T) {
	if _, err := List(ListFilter{}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected project required, got %v", err)
	}
	// Invalid status/type fail before DB when project is set — but project existence
	// is checked first via DB. Invalid enums are checked after project exists, so
	// validate helpers directly.
	if err := validateType("nope"); err == nil {
		t.Fatal("expected invalid type")
	}
	if err := validateStatus("nope"); err == nil {
		t.Fatal("expected invalid status")
	}
	if err := validateType("feature"); err != nil {
		t.Fatal(err)
	}
	if err := validateStatus("in_progress"); err != nil {
		t.Fatal(err)
	}
}

func TestFormatTime(t *testing.T) {
	if FormatTime(time.Time{}) != "" {
		t.Fatal("zero time should format empty")
	}
	ts := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	if got := FormatTime(ts); got != "2026-07-22 13:00:00" {
		t.Fatalf("got %q", got)
	}
	if FormatTimePtr(nil) != "" {
		t.Fatal("nil ptr should be empty")
	}
	if got := FormatTimePtr(&ts); got != "2026-07-22 13:00:00" {
		t.Fatalf("got %q", got)
	}
}

func TestValidEnumsDocumented(t *testing.T) {
	// Keep CLI help and package constants in sync.
	if len(ValidTypes) != 3 {
		t.Fatalf("types: %v", ValidTypes)
	}
	if len(ValidStatuses) != 5 {
		t.Fatalf("statuses: %v", ValidStatuses)
	}
}

func TestChildrenChecklist(t *testing.T) {
	if ChildrenChecklist(nil) != "" {
		t.Fatal("nil issue should yield empty checklist")
	}
	root := &ent.Issue{ID: 3, Title: "parent"}
	if ChildrenChecklist(root) != "" {
		t.Fatal("no children should yield empty")
	}

	root.Edges.Children = []*ent.Issue{
		{ID: 5, Title: "[API-1] setup", Status: entIssue.StatusDone},
		{ID: 6, Title: "[API-2] schema", Status: entIssue.StatusTodo},
		{ID: 7, Title: "[API-3] auth", Status: entIssue.StatusInProgress},
	}
	got := ChildrenChecklist(root)
	if !strings.Contains(got, "## 하위 이슈") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "- [x] #5 [API-1] setup (성공)") {
		t.Fatalf("done child missing: %q", got)
	}
	if !strings.Contains(got, "- [ ] #6 [API-2] schema (작업전)") {
		t.Fatalf("todo child missing: %q", got)
	}
	if !strings.Contains(got, "- [ ] #7 [API-3] auth (작업중)") {
		t.Fatalf("in_progress child missing: %q", got)
	}
}

func TestStatusLabel(t *testing.T) {
	cases := map[string]string{
		"todo":        "작업전",
		"in_progress": "작업중",
		"testing":     "테스트",
		"failed":      "실패",
		"done":        "성공",
		"other":       "other",
	}
	for in, want := range cases {
		if got := statusLabel(in); got != want {
			t.Fatalf("statusLabel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCascadeEnqueueDelayPositive(t *testing.T) {
	// Guard against accidental zero/negative that reintroduces cascade races.
	if cascadeEnqueueDelay < time.Second {
		t.Fatalf("cascadeEnqueueDelay=%v; want at least 1s to space bulk enqueue", cascadeEnqueueDelay)
	}
}
