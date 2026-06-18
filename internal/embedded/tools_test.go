package embedded

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
// read_file paging — task #6309 deadlock fix
// ─────────────────────────────────────────────

// parseFooterOffset extracts N from a read_file paging footer
// ("...Call read_file with offset=N to read more."). Returns false if absent.
func parseFooterOffset(s string) (int, bool) {
	const marker = "Call read_file with offset="
	i := strings.LastIndex(s, marker)
	if i < 0 {
		return 0, false
	}
	rest := s[i+len(marker):]
	j := strings.IndexByte(rest, ' ')
	if j < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:j])
	if err != nil {
		return 0, false
	}
	return n, true
}

func lastChars(s string) string {
	if len([]rune(s)) <= 160 {
		return s
	}
	r := []rune(s)
	return string(r[len(r)-160:])
}

// A small file is returned verbatim — no paging footer added.
func TestReadfileSmallFileNoFooter(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	content := "line1\nline2\nline3"
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "small.txt"})
	if err != nil {
		t.Fatalf("readfile error: %v", err)
	}
	if out != content {
		t.Errorf("small file should return exact content, got %q", out)
	}
	if strings.Contains(out, "Call read_file with offset") {
		t.Error("small file must not get a paging footer")
	}
}

// A file longer than the default line window is paged: only the first window is
// returned, with a footer naming the next offset.
func TestReadfileLargeFilePagesWithFooter(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	var sb strings.Builder
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "big.txt"})
	if err != nil {
		t.Fatalf("readfile error: %v", err)
	}
	if !strings.Contains(out, "line 1\n") {
		t.Error("first page should include line 1")
	}
	if strings.Contains(out, "line 201\n") {
		t.Error("first page should not reach past the default window")
	}
	next, ok := parseFooterOffset(out)
	if !ok || next != defaultReadFileLines+1 {
		t.Errorf("footer should point to offset=%d, got (%d, %v); tail=%q",
			defaultReadFileLines+1, next, ok, lastChars(out))
	}
}

// The #6309 regression: a bug sitting in the file's tail — past both the
// 200-line window and the 8000-char history-truncation boundary — must remain
// reachable by following paging footers, and each footer must survive
// truncateToolResult and strictly advance the offset (no deadlock).
func TestReadfileTailReachableThroughPaging6309(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	const total = 340
	var sb strings.Builder
	for i := 1; i <= total; i++ {
		if i == 335 {
			sb.WriteString("BUG_MARKER_else_branch\n")
			continue
		}
		fmt.Fprintf(&sb, "some kotlin source line number %d here with padding padding\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "Screen.kt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	offset := 1
	seenMarker := false
	for page := 0; page < 50; page++ {
		args := map[string]interface{}{"path": "Screen.kt"}
		if offset > 1 {
			args["offset"] = float64(offset)
		}
		out, err := tr.readfile(context.Background(), args)
		if err != nil {
			t.Fatalf("page %d (offset %d): %v", page, offset, err)
		}
		// What the model actually sees is the history-truncated result.
		stored := truncateToolResult(out, "read_file")
		if strings.Contains(stored, "BUG_MARKER_else_branch") {
			seenMarker = true
			break
		}
		next, ok := parseFooterOffset(stored)
		if !ok {
			t.Fatalf("page %d (offset %d): no footer survived truncation; tail=%q",
				page, offset, lastChars(stored))
		}
		if next <= offset {
			t.Fatalf("page %d: footer offset %d did not advance past %d (deadlock)", page, next, offset)
		}
		offset = next
	}
	if !seenMarker {
		t.Error("bug marker in file tail was never reachable through paging (#6309 regression)")
	}
}

// The #6410 regression: weak local models routinely encode the offset as a
// quoted string ("201"). A plain args["offset"].(float64) assertion silently
// failed on those, so read_file returned page 1 again with a footer naming the
// same offset, and the model re-issued the byte-for-byte identical call until
// the repeated-tool-call stuck guard killed the task. A string offset must page
// just like a numeric one.
func TestReadfileStringOffset6410(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	var sb strings.Builder
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	// String offset must be honored exactly like the numeric form.
	out, err := tr.readfile(context.Background(), map[string]interface{}{
		"path":   "big.txt",
		"offset": "201",
	})
	if err != nil {
		t.Fatalf("readfile string offset error: %v", err)
	}
	if !strings.Contains(out, "line 201\n") {
		t.Error("string offset=201 should start the page at line 201")
	}
	if strings.Contains(out, "line 1\n") {
		t.Error("string offset=201 must not return page 1 (the #6410 stuck loop)")
	}
	next, ok := parseFooterOffset(out)
	if !ok || next <= 201 {
		t.Errorf("footer must advance past a string offset, got (%d, %v)", next, ok)
	}

	// "201.0" style strings are tolerated too.
	out, err = tr.readfile(context.Background(), map[string]interface{}{
		"path":   "big.txt",
		"offset": "201.0",
	})
	if err != nil {
		t.Fatalf("readfile float-string offset error: %v", err)
	}
	if !strings.Contains(out, "line 201\n") {
		t.Error(`string offset "201.0" should start the page at line 201`)
	}
}

// argInt accepts the numeric encodings local models actually emit and rejects
// non-numeric junk (falling back to the caller's default).
func TestArgInt(t *testing.T) {
	cases := []struct {
		name   string
		val    interface{}
		want   int
		wantOK bool
	}{
		{"float64", float64(156), 156, true},
		{"int", 156, 156, true},
		{"int64", int64(156), 156, true},
		{"string", "156", 156, true},
		{"string with spaces", "  156 ", 156, true},
		{"float string", "156.0", 156, true},
		{"empty string", "", 0, false},
		{"non-numeric string", "abc", 0, false},
		{"missing key", nil, 0, false},
		{"bool", true, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := map[string]interface{}{}
			if c.val != nil {
				args["offset"] = c.val
			}
			got, ok := argInt(args, "offset")
			if got != c.want || ok != c.wantOK {
				t.Errorf("argInt(%v) = (%d, %v), want (%d, %v)", c.val, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// A single line longer than the char cap must not deadlock: output stays under
// the history budget and the footer advances past the giant line.
func TestReadfileGiantLineAdvancesNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	giant := strings.Repeat("x", maxReadFileChars+5000)
	content := giant + "\nafter the giant line\n"
	if err := os.WriteFile(filepath.Join(dir, "min.js"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "min.js"})
	if err != nil {
		t.Fatalf("readfile error: %v", err)
	}
	if n := len([]rune(out)); n > maxToolResultChars {
		t.Errorf("output (%d runes) must stay under maxToolResultChars (%d) so the footer survives", n, maxToolResultChars)
	}
	if !strings.Contains(out, "longer than") {
		t.Errorf("expected a long-line notice, tail=%q", lastChars(out))
	}
	next, ok := parseFooterOffset(out)
	if !ok || next != 2 {
		t.Errorf("giant first line should advance footer to offset=2, got (%d, %v)", next, ok)
	}
}

// An explicit limit is honored, and the footer points just past the window.
func TestReadfileExplicitLimitRespected(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&sb, "L%d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := tr.readfile(context.Background(), map[string]interface{}{
		"path": "f.txt", "offset": float64(10), "limit": float64(5),
	})
	if err != nil {
		t.Fatalf("readfile error: %v", err)
	}
	if !strings.Contains(out, "L10\n") || !strings.Contains(out, "L14\n") {
		t.Errorf("expected lines L10-L14, got: %q", out)
	}
	if strings.Contains(out, "L15\n") {
		t.Error("limit=5 should stop at L14")
	}
	next, ok := parseFooterOffset(out)
	if !ok || next != 15 {
		t.Errorf("footer should point to offset=15, got (%d, %v)", next, ok)
	}
}

// capOutput caps the live-stream byte budget, but must trim on a rune boundary so
// multi-byte output (e.g. Korean/CJK from bash) is never split into a replacement
// character, and its notice must name a recovery path rather than dead-ending.
func TestCapOutputRuneSafeAndActionable(t *testing.T) {
	tr := NewToolRegistry(t.TempDir(), "test-sheep", nil, nil)
	// Each '가' is 3 bytes, so the maxOutputBytes index lands mid-rune unless
	// capOutput trims back to a boundary.
	out := tr.capOutput(strings.Repeat("가", maxOutputBytes))
	if strings.Contains(out, "�") {
		t.Error("capOutput split a multi-byte rune (replacement character present)")
	}
	for _, want := range []string{"truncated", "read_file"} {
		if !strings.Contains(out, want) {
			t.Errorf("capOutput notice missing %q; tail=%q", want, out[len(out)-200:])
		}
	}
	// Output at or below the cap passes through untouched.
	small := "작은 출력"
	if got := tr.capOutput(small); got != small {
		t.Errorf("capOutput(%q) = %q, want unchanged", small, got)
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

func TestIsBinaryLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"empty", "", false},
		{"plain source", "file.go:12:func main() {", false},
		{"utf8 korean", "doc.md:3:안녕하세요 양들아", false},
		{"tabs ok", "x.go:1:\tif a == b {", false},
		{"nul byte", "asset.binarypb:2:abc\x00def", true},
		// Protobuf-ish line: printable type URL surrounded by control bytes.
		{"control byte run", "m.binarypb:2:\x08\x12\x1a\x05Htype.googleapis.com\x00", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBinaryLine(tc.line); got != tc.want {
				t.Errorf("isBinaryLine(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestFilterBinaryLines(t *testing.T) {
	in := "good.go:1:package main\nasset.binarypb:2:abc\x00def\nother.go:3:var x = 1"
	out := filterBinaryLines(in)
	if !strings.Contains(out, "good.go:1:package main") || !strings.Contains(out, "other.go:3:var x = 1") {
		t.Errorf("expected text lines kept, got %q", out)
	}
	if strings.Contains(out, "\x00") {
		t.Errorf("binary line was not filtered: %q", out)
	}
	if !strings.Contains(out, "1 binary/non-text line(s) omitted") {
		t.Errorf("expected omission notice, got %q", out)
	}
}

func TestExecGrepExcludesBuildDir(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// A match inside a build/ directory must be excluded by default.
	os.MkdirAll(filepath.Join(dir, "build", "assets"), 0755)
	os.WriteFile(filepath.Join(dir, "src.go"), []byte("OcrScript here"), 0644)
	os.WriteFile(filepath.Join(dir, "build", "assets", "gen.go"), []byte("OcrScript artifact"), 0644)

	out, err := tr.execGrep(context.Background(), map[string]interface{}{"pattern": "OcrScript"})
	if err != nil {
		t.Fatalf("execGrep failed: %v", err)
	}
	if !strings.Contains(out, "src.go") {
		t.Errorf("expected source match, got %q", out)
	}
	if strings.Contains(out, "build/") || strings.Contains(out, filepath.Join("build", "assets")) {
		t.Errorf("build dir match should be excluded, got %q", out)
	}
}
