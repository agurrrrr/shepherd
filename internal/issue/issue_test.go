package issue

import (
	"strings"
	"testing"
	"time"
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
