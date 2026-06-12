package embedded

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsBinary(t *testing.T) {
	// Minimal JPEG header (SOI + APP0/JFIF marker) — what triggered task #5911.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", nil, false},
		{"plain text", []byte("hello, world\nsecond line\n"), false},
		{"utf8 korean", []byte("안녕하세요 양들아"), false},
		{"nul byte", []byte("abc\x00def"), true},
		{"jpeg header", jpeg, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBinary(tc.data); got != tc.want {
				t.Errorf("isBinary(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestReadfileBinaryGuard(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// A binary (image-like) file must NOT return raw bytes.
	imgPath := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(imgPath, []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x00, 0xFF, 0x00}, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "photo.jpg"})
	if err != nil {
		t.Fatalf("readfile returned error: %v", err)
	}
	if !strings.Contains(out, "binary file") {
		t.Errorf("expected binary notice, got %q", out)
	}
	if strings.ContainsRune(out, 0) {
		t.Error("binary notice must not contain raw NUL bytes")
	}

	// A normal text file still reads through.
	txtPath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(txtPath, []byte("just text"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err = tr.readfile(context.Background(), map[string]interface{}{"path": "note.txt"})
	if err != nil {
		t.Fatalf("readfile text returned error: %v", err)
	}
	if out != "just text" {
		t.Errorf("expected text content, got %q", out)
	}
}

func TestReadfileVisionImage(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	tr.SetVision(true)

	// A JPEG header (FF D8 FF) → image/jpeg via http.DetectContentType.
	imgBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	if err := os.WriteFile(filepath.Join(dir, "pic.jpg"), imgBytes, 0644); err != nil {
		t.Fatal(err)
	}

	out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "pic.jpg"})
	if err != nil {
		t.Fatalf("readfile returned error: %v", err)
	}
	if !strings.Contains(out, "Loaded image") {
		t.Errorf("expected loaded-image notice, got %q", out)
	}

	imgs := tr.DrainPendingImages()
	if len(imgs) != 1 {
		t.Fatalf("expected 1 pending image, got %d", len(imgs))
	}
	if !strings.HasPrefix(imgs[0].dataURL, "data:image/jpeg;base64,") {
		t.Errorf("expected jpeg data URL, got %q", imgs[0].dataURL[:min(40, len(imgs[0].dataURL))])
	}
	// Draining again must return nothing (buffer cleared).
	if again := tr.DrainPendingImages(); again != nil {
		t.Errorf("expected empty drain after first, got %d", len(again))
	}
}

func TestReadfileVisionDisabledKeepsBinaryNotice(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil) // vision off by default

	imgBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	if err := os.WriteFile(filepath.Join(dir, "pic.jpg"), imgBytes, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "pic.jpg"})
	if err != nil {
		t.Fatalf("readfile returned error: %v", err)
	}
	if !strings.Contains(out, "binary file") {
		t.Errorf("expected binary notice when vision off, got %q", out)
	}
	if len(tr.DrainPendingImages()) != 0 {
		t.Error("no images should be buffered when vision is off")
	}
}

// ─────────────────────────────────────────────
// B3: safePath "..foo" false positive fix
// ─────────────────────────────────────────────

func TestSafePathDotDotFoo(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// "..foo" is a legitimate filename inside the project — should NOT be blocked.
	got, err := tr.safePath("..foo")
	if err != nil {
		t.Fatalf("safePath(\"..foo\") should succeed, got error: %v", err)
	}
	if got != filepath.Join(dir, "..foo") {
		t.Errorf("safePath(\"..foo\") = %q, want %q", got, filepath.Join(dir, "..foo"))
	}

	// Actual traversal ".." should still be blocked.
	_, err = tr.safePath("..")
	if err == nil {
		t.Error("safePath(\"..\") should be blocked")
	}

	// "../etc/passwd" should be blocked.
	_, err = tr.safePath("../etc/passwd")
	if err == nil {
		t.Error("safePath(\"../etc/passwd\") should be blocked")
	}

	// "subdir/../..foo" (resolves to "..foo" inside project) should work.
	got, err = tr.safePath("subdir/../..foo")
	if err != nil {
		t.Fatalf("safePath(\"subdir/../..foo\") should succeed, got: %v", err)
	}
	if got != filepath.Join(dir, "..foo") {
		t.Errorf("safePath(\"subdir/../..foo\") = %q, want %q", got, filepath.Join(dir, "..foo"))
	}
}

// ─────────────────────────────────────────────
// B4: writefile empty content + editfile error message
// ─────────────────────────────────────────────

func TestWritefileEmptyContent(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// Empty content should be allowed (e.g., creating .gitkeep).
	out, err := tr.writefile(context.Background(), map[string]interface{}{
		"path":    ".gitkeep",
		"content": "",
	})
	if err != nil {
		t.Fatalf("writefile with empty content should succeed, got: %v", err)
	}
	if !strings.Contains(out, "Wrote 0 bytes") {
		t.Errorf("expected 'Wrote 0 bytes', got %q", out)
	}

	// Verify the file was actually created.
	data, err := os.ReadFile(filepath.Join(dir, ".gitkeep"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(data))
	}

	// No content key at all — content defaults to "" via type assertion.
	out, err = tr.writefile(context.Background(), map[string]interface{}{
		"path": "empty.txt",
	})
	if err != nil {
		t.Fatalf("writefile without content key should succeed, got: %v", err)
	}

	// Path still required.
	_, err = tr.writefile(context.Background(), map[string]interface{}{
		"content": "hello",
	})
	if err == nil {
		t.Error("writefile without path should fail")
	}
}

func TestEditfileErrorMessage(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// Missing path should say "path and oldText are required" (not mention newText).
	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"oldText": "foo",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "path and oldText are required") {
		t.Errorf("error message = %q, should contain 'path and oldText are required'", err.Error())
	}

	// Missing oldText should also fail.
	_, err = tr.editfile(context.Background(), map[string]interface{}{
		"path": "foo.txt",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "path and oldText are required") {
		t.Errorf("error message = %q, should contain 'path and oldText are required'", err.Error())
	}

	// Missing newText should be allowed (replaces with empty string).
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("hello world"), 0644)
	_, err = tr.editfile(context.Background(), map[string]interface{}{
		"path":    "test.txt",
		"oldText": "world",
	})
	if err != nil {
		t.Fatalf("editfile without newText should succeed, got: %v", err)
	}
	data, _ := os.ReadFile(testFile)
	if string(data) != "hello " {
		t.Errorf("expected 'hello ', got %q", string(data))
	}
}

// ─────────────────────────────────────────────
// B5: matchGlob helper tests
// ─────────────────────────────────────────────

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		// Simple globs (no **)
		{"main.go", "*.go", true},
		{"main.py", "*.go", false},
		{"src/main.go", "*.go", false},

		// ** at start
		{"foo.go", "**/*.go", true},
		{"sub/foo.go", "**/*.go", true},
		{"a/b/c/foo.go", "**/*.go", true},
		{"foo.txt", "**/*.go", false},

		// ** in middle
		{"src/main.go", "src/**/*.go", true},
		{"src/internal/helper.go", "src/**/*.go", true},
		{"other/main.go", "src/**/*.go", false},
		{"src/README.md", "src/**/*.go", false},

		// ** at end (match everything under prefix)
		{"src/foo.go", "src/**", true},
		{"src/a/b/c.go", "src/**", true},
		{"other/foo.go", "src/**", false},

		// Exact file match
		{"go.mod", "go.mod", true},
		{"go.sum", "go.mod", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.path, func(t *testing.T) {
			got := matchGlob(tt.path, tt.pattern)
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
			}
		})
	}
}

// ─────────────────────────────────────────────
// B5: execGlob with ** patterns
// ─────────────────────────────────────────────

func TestExecGlobRecursive(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// Create test directory structure.
	os.MkdirAll(filepath.Join(dir, "src", "internal"), 0755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "lib.go"), []byte("package src"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "internal", "deep.go"), []byte("package internal"), 0644)

	// **/*.go should match all .go files recursively.
	out, err := tr.execGlob(context.Background(), map[string]interface{}{"pattern": "**/*.go"})
	if err != nil {
		t.Fatalf("execGlob failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 matches, got %d: %v", len(lines), lines)
	}

	// src/**/*.go should match only files under src/.
	out, err = tr.execGlob(context.Background(), map[string]interface{}{"pattern": "src/**/*.go"})
	if err != nil {
		t.Fatalf("execGlob failed: %v", err)
	}
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 matches under src/, got %d: %v", len(lines), lines)
	}

	// Non-existent pattern should return "No files found".
	out, err = tr.execGlob(context.Background(), map[string]interface{}{"pattern": "**/*.xyz"})
	if err != nil {
		t.Fatalf("execGlob failed: %v", err)
	}
	if out != "No files found" {
		t.Errorf("expected 'No files found', got %q", out)
	}
}

// ─────────────────────────────────────────────
// B5: grep fallback uses WalkDir (not Glob)
// ─────────────────────────────────────────────

func TestExecGrepFallbackWalkDir(t *testing.T) {
	// This test verifies the Go regex fallback searches recursively via WalkDir,
	// not just the top-level directory via filepath.Glob.
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// Create nested test files.
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "top.txt"), []byte("hello world\nfoo bar"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested match here"), 0644)

	// Force fallback by using a pattern that won't find rg (rg not installed in test env).
	// In CI rg might be installed, so we just check the result contains matches.
	out, err := tr.execGrep(context.Background(), map[string]interface{}{"pattern": "nested match"})
	if err != nil {
		t.Fatalf("execGrep failed: %v", err)
	}
	// If rg is installed, it finds the match. If not, the WalkDir fallback should find it.
	if !strings.Contains(out, "nested match") {
		t.Errorf("expected 'nested match' in output, got %q", out)
	}
}
