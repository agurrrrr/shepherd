package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestValidateWorkspacePath(t *testing.T) {
	tmp := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}

	sub := filepath.Join(tmp, "code")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(tmp, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("empty is allowed and stays empty", func(t *testing.T) {
		got, err := ValidateWorkspacePath("")
		if err != nil || got != "" {
			t.Fatalf("got %q, err %v", got, err)
		}
	})

	t.Run("tilde expands to home", func(t *testing.T) {
		got, err := ValidateWorkspacePath("~")
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Clean(home) {
			t.Fatalf("got %q, want %q", got, home)
		}
	})

	t.Run("tilde subpath expands", func(t *testing.T) {
		// Point HOME (and USERPROFILE for Windows CI) at the temp dir so "~/code"
		// resolves to the directory we just created.
		t.Setenv("HOME", tmp)
		t.Setenv("USERPROFILE", tmp)
		got, err := ValidateWorkspacePath("~/code")
		if err != nil {
			t.Fatal(err)
		}
		if got != sub {
			t.Fatalf("got %q, want %q", got, sub)
		}
	})

	t.Run("relative path resolves against cwd", func(t *testing.T) {
		// Chdir into tmp instead of computing a path relative to the package
		// dir: on Windows CI the temp dir (C:) and the checkout (D:) sit on
		// different volumes, so no relative path between them exists.
		t.Chdir(tmp)
		// Build the expectation from Getwd, not tmp, so a symlinked temp dir
		// (macOS /var -> /private/var) or an 8.3 short name (RUNNER~1) still
		// matches what filepath.Abs produces.
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(wd, "code")
		got, err := ValidateWorkspacePath("code")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("trailing separators are normalized", func(t *testing.T) {
		got, err := ValidateWorkspacePath(sub + string(filepath.Separator))
		if err != nil {
			t.Fatal(err)
		}
		if got != sub {
			t.Fatalf("got %q, want %q", got, sub)
		}
	})

	t.Run("dot segments are cleaned", func(t *testing.T) {
		got, err := ValidateWorkspacePath(filepath.Join(tmp, ".", "code", "..", "code"))
		if err != nil {
			t.Fatal(err)
		}
		if got != sub {
			t.Fatalf("got %q, want %q", got, sub)
		}
	})

	t.Run("missing path is an error", func(t *testing.T) {
		if _, err := ValidateWorkspacePath(filepath.Join(tmp, "nope")); err == nil {
			t.Fatal("expected error for missing path")
		}
	})

	t.Run("file path is an error", func(t *testing.T) {
		if _, err := ValidateWorkspacePath(file); err == nil {
			t.Fatal("expected error for file path")
		}
	})

	t.Run("root is allowed", func(t *testing.T) {
		if _, err := ValidateWorkspacePath("/"); err != nil {
			t.Fatalf("root should be allowed: %v", err)
		}
	})

	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		got, err := ValidateWorkspacePath("  " + sub + "  ")
		if err != nil {
			t.Fatal(err)
		}
		if got != sub {
			t.Fatalf("got %q, want %q", got, sub)
		}
	})
}

// TestGetSheepScopeExcludeDropsBlanks pins the read path for the exclusion
// list: viper hands back []interface{} from YAML and []string from an
// in-process Set, and blank entries must never match a project name.
func TestGetSheepScopeExcludeDropsBlanks(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	viper.SetDefault("shepherd_scope_exclude", []string{})
	viper.Set("shepherd_scope_exclude", []interface{}{"home", "  ", "docs ", ""})

	got := GetSheepScopeExclude()
	want := []string{"home", "docs"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
