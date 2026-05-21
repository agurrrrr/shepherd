package wiki

import (
	"testing"
)

func TestDiffContent(t *testing.T) {
	old := "line1\nline2\nline3"
	new := "line1\nmodified\nline3\nline4"

	diff := DiffContent(old, new)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
}

func TestPageChangeOptionsDefaults(t *testing.T) {
	opts := PageChangeOptions{}
	if opts.Summary != "" {
		t.Error("expected empty default summary")
	}
	if opts.Author != "" {
		t.Error("expected empty default author")
	}
}
