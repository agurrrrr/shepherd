package embedded

import (
	"bytes"
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
	// Large Korean text (> 8 KB sample threshold) — the #6624 false-positive
	// case. The old isBinary trimmed only continuation bytes, leaving a dangling
	// leading byte that made utf8.Valid return false.
	var largeKorean []byte
	for i := 0; i < 1000; i++ {
		largeKorean = append(largeKorean, []byte("안녕하세요 양들아 이것은 테스트입니다.\n")...)
	}
	// Pure multi-byte text that lands exactly on the sample boundary.
	pureKoreanBoundary := bytes.Repeat([]byte("안"), 3000) // 9000 bytes
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
		{"large korean >8KB", largeKorean, false},
		{"pure korean at boundary", pureKoreanBoundary, false},
		{"all continuation bytes", bytes.Repeat([]byte{0x80}, 10000), true},
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

	// A normal text file still reads through (with line-number prefixes).
	txtPath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(txtPath, []byte("just text"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err = tr.readfile(context.Background(), map[string]interface{}{"path": "note.txt"})
	if err != nil {
		t.Fatalf("readfile text returned error: %v", err)
	}
	if out != "1→just text" {
		t.Errorf("expected prefixed text content, got %q", out)
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

// TestDispatchMCPImageSurfacedToVision is the regression test for task #6684:
// an MCP tool (e.g. mobile_take_screenshot) that returns an image must have that
// image buffered as a pending image so the vision model can see it — previously
// the image was dropped and the model worked blind.
func TestDispatchMCPImageSurfacedToVision(t *testing.T) {
	disp := func(name string, args map[string]interface{}) (string, []MCPImage, error) {
		return "", []MCPImage{{MIMEType: "image/png", Data: "QUJD"}}, nil
	}
	tr := NewToolRegistry(t.TempDir(), "test-sheep", []MCPToolDef{{Name: "mobile_take_screenshot"}}, disp)
	tr.SetVision(true)

	out, err := tr.Dispatch(context.Background(), "mobile_take_screenshot", nil)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if !strings.Contains(out, "attached below") {
		t.Errorf("expected attached-image note in result, got %q", out)
	}

	imgs := tr.DrainPendingImages()
	if len(imgs) != 1 {
		t.Fatalf("expected 1 pending image, got %d", len(imgs))
	}
	if imgs[0].dataURL != "data:image/png;base64,QUJD" {
		t.Errorf("unexpected data URL: %q", imgs[0].dataURL)
	}
}

// TestDispatchMCPImageVisionDisabled verifies that when vision is off the image
// is NOT buffered (the model can't view it) but the result still tells the model
// an image came back, rather than looking like an empty/no-op tool call.
func TestDispatchMCPImageVisionDisabled(t *testing.T) {
	disp := func(name string, args map[string]interface{}) (string, []MCPImage, error) {
		return "", []MCPImage{{MIMEType: "image/png", Data: "QUJD"}}, nil
	}
	tr := NewToolRegistry(t.TempDir(), "test-sheep", []MCPToolDef{{Name: "mobile_take_screenshot"}}, disp) // vision off

	out, err := tr.Dispatch(context.Background(), "mobile_take_screenshot", nil)
	if err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if !strings.Contains(out, "vision is not enabled") {
		t.Errorf("expected vision-disabled note, got %q", out)
	}
	if len(tr.DrainPendingImages()) != 0 {
		t.Error("no images should be buffered when vision is off")
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

// TestReadfileLargeKoreanText regression test for #6624: a non-ASCII text
// file > 8 KB must be read as text, not misclassified as binary by isBinary.
// The old isBinary trimmed only trailing continuation bytes, leaving a dangling
// leading byte that made utf8.Valid return false.
func TestReadfileLargeKoreanText(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// Build a ~56 KB Korean text file (well above the 8 KB sample threshold).
	var content strings.Builder
	for i := 0; i < 1000; i++ {
		content.WriteString("안녕하세요 양들아 이것은 테스트입니다.\n")
	}
	mdPath := filepath.Join(dir, "large_korean.md")
	if err := os.WriteFile(mdPath, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "large_korean.md"})
	if err != nil {
		t.Fatalf("readfile returned error: %v", err)
	}
	if strings.Contains(out, "binary file") {
		t.Errorf("large Korean text file was misclassified as binary:\n%s", out)
	}
	if !strings.Contains(out, "안녕하세요") {
		t.Errorf("expected Korean text content in output, got (first 200 chars): %q", out[:min(200, len(out))])
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

// A small file is returned with line-number prefixes — no paging footer added.
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
	want := "1→line1\n2→line2\n3→line3"
	if out != want {
		t.Errorf("small file should return line-prefixed content, got %q want %q", out, want)
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

// The #6505 regression: weak local models name the next offset in their
// thinking but omit it from the tool-call arguments JSON, so every follow-up
// read_file on the same path returned page 1 again and the model looped until
// the stuck guard killed the task. A repeat read with NO offset must now
// auto-advance to the next page, and once the file is exhausted it must return
// a stable message (not page 1) so genuine spinning can still be caught.
func TestReadfileAutoAdvanceNoOffset6505(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	const total = 500
	var sb strings.Builder
	for i := 1; i <= total; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	// Page through the whole file calling read_file with NO offset every time,
	// exactly as a model that keeps dropping the offset would. Each call must
	// move strictly forward instead of repeating page 1.
	prevEnd := 0
	for page := 0; page < 50; page++ {
		out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "big.txt"})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if page > 0 {
			if !strings.Contains(out, "Auto-paged") {
				t.Fatalf("page %d: repeat no-offset read must be flagged as auto-paged; got %q", page, lastChars(out))
			}
			if strings.Contains(out, "line 1\n") {
				t.Fatalf("page %d: auto-paged read returned page 1 again (#6505 loop)", page)
			}
		}
		if tr.lastReadEndLine <= prevEnd {
			t.Fatalf("page %d: lastReadEndLine %d did not advance past %d", page, tr.lastReadEndLine, prevEnd)
		}
		prevEnd = tr.lastReadEndLine
		if tr.lastReadEndLine >= total {
			break
		}
	}
	if prevEnd < total {
		t.Fatalf("never reached EOF through auto-paging; stopped at %d/%d", prevEnd, total)
	}

	// File fully read: another no-offset read must return the stable
	// "already read" message (not page 1) so the stuck guard can catch a
	// spinning model, and lastReadEndLine must stay put.
	endBefore := tr.lastReadEndLine
	out, err := tr.readfile(context.Background(), map[string]interface{}{"path": "big.txt"})
	if err != nil {
		t.Fatalf("post-EOF read: %v", err)
	}
	if !strings.Contains(out, "already read this entire file") {
		t.Errorf("post-EOF read should return the already-read message, got %q", lastChars(out))
	}
	if strings.Contains(out, "line 1\n") {
		t.Error("post-EOF read must not wrap back to page 1")
	}
	if tr.lastReadEndLine != endBefore {
		t.Errorf("post-EOF read changed lastReadEndLine %d -> %d", endBefore, tr.lastReadEndLine)
	}

	// The offset=1 escape hatch must still allow a deliberate re-read from top.
	out, err = tr.readfile(context.Background(), map[string]interface{}{"path": "big.txt", "offset": 1})
	if err != nil {
		t.Fatalf("offset=1 re-read: %v", err)
	}
	if !strings.Contains(out, "line 1\n") {
		t.Error("offset=1 must re-read from the top of the file")
	}

	// An explicit offset on a tracked path must NOT auto-advance.
	out, err = tr.readfile(context.Background(), map[string]interface{}{"path": "big.txt", "offset": 50})
	if err != nil {
		t.Fatalf("explicit offset read: %v", err)
	}
	if !strings.Contains(out, "line 50\n") || strings.Contains(out, "Auto-paged") {
		t.Error("explicit offset must be honored verbatim, not auto-advanced")
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
	if !strings.Contains(out, "10→L10\n") || !strings.Contains(out, "14→L14\n") {
		t.Errorf("expected lines L10-L14 with prefixes, got: %q", out)
	}
	if strings.Contains(out, "15→L15") {
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

// ─────────────────────────────────────────────
// edit_file hardening (Phase 1-1 / task #7549)
// ─────────────────────────────────────────────

func TestEditfileExactMatchStillWorks(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("hello world\n"), 0644)

	out, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "a.txt",
		"oldText": "world",
		"newText": "shepherd",
	})
	if err != nil {
		t.Fatalf("editfile: %v", err)
	}
	if !strings.Contains(out, "Snippet:") {
		t.Errorf("success should include snippet, got %q", out)
	}
	if !strings.Contains(out, "→") {
		t.Errorf("snippet should use line prefixes, got %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello shepherd\n" {
		t.Errorf("content = %q", data)
	}
}

func TestEditfileReplaceAll(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "b.txt")
	os.WriteFile(path, []byte("foo bar foo baz foo"), 0644)

	// Without replace_all: multi-match must fail.
	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "b.txt",
		"oldText": "foo",
		"newText": "X",
	})
	if err == nil {
		t.Fatal("expected uniqueness error")
	}
	if !strings.Contains(err.Error(), "appears 3 times") {
		t.Errorf("error = %q", err)
	}

	// With replace_all: all replaced.
	out, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":        "b.txt",
		"oldText":     "foo",
		"newText":     "X",
		"replace_all": true,
	})
	if err != nil {
		t.Fatalf("replace_all: %v", err)
	}
	if !strings.Contains(out, "3 occurrence") {
		t.Errorf("should report 3 occurrences, got %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "X bar X baz X" {
		t.Errorf("content = %q", data)
	}
}

func TestEditfileFieldAliases(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "c.txt")
	os.WriteFile(path, []byte("alpha beta gamma"), 0644)

	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":       "c.txt",
		"old_string": "beta",
		"new_string": "BETA",
	})
	if err != nil {
		t.Fatalf("alias edit: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha BETA gamma" {
		t.Errorf("content = %q", data)
	}
}

func TestEditfileConfusableFallbackSmartQuotes(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "d.txt")
	// File has smart quotes; model supplies ASCII quotes.
	os.WriteFile(path, []byte("say \u201Chello\u201D world\n"), 0644)

	out, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "d.txt",
		"oldText": `"hello"`,
		"newText": `"goodbye"`,
	})
	if err != nil {
		t.Fatalf("confusable fallback: %v", err)
	}
	if !strings.Contains(out, "confusable-normalized") {
		t.Errorf("should note confusable match, got %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "say \"goodbye\" world\n" {
		t.Errorf("content = %q", data)
	}
}

func TestEditfileConfusableFallbackEmDash(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "e.txt")
	os.WriteFile(path, []byte("foo\u2014bar"), 0644)

	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "e.txt",
		"oldText": "foo--bar",
		"newText": "foo-bar",
	})
	if err != nil {
		t.Fatalf("em-dash fallback: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "foo-bar" {
		t.Errorf("content = %q", data)
	}
}

func TestEditfileConfusableAmbiguousFailClosed(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "f.txt")
	// Partial expansion of "-" inside em-dash must not succeed.
	os.WriteFile(path, []byte("a\u2014b"), 0644)

	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "f.txt",
		"oldText": "-",
		"newText": "x",
	})
	if err == nil {
		t.Fatal("partial confusable match must fail closed")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got %q", err)
	}
	// File untouched.
	data, _ := os.ReadFile(path)
	if string(data) != "a\u2014b" {
		t.Errorf("file should be unchanged, got %q", data)
	}
}

func TestEditfileNotFoundNearestMatchHint(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "g.txt")
	os.WriteFile(path, []byte("line one\noCollMode_set, value\nline three\n"), 0644)

	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "g.txt",
		"oldText": "oCollMode_set, wrong",
		"newText": "x",
	})
	if err == nil {
		t.Fatal("expected not found")
	}
	msg := err.Error()
	if !strings.Contains(msg, "exact occurrences: 0") {
		t.Errorf("should report occurrence count, got %q", msg)
	}
	if !strings.Contains(msg, "Nearest match: line 2") {
		t.Errorf("should include nearest-match hint, got %q", msg)
	}
	if !strings.Contains(msg, "read_file") {
		t.Errorf("should nudge re-read, got %q", msg)
	}
	if !strings.Contains(msg, "N→") {
		t.Errorf("should mention line-number prefix defense, got %q", msg)
	}
}

func TestEditfileSuccessSnippetContext(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "h.txt")
	// 7 lines; edit line 4; expect lines 1-7 with 3-line context.
	content := "L1\nL2\nL3\nTARGET\nL5\nL6\nL7\n"
	os.WriteFile(path, []byte(content), 0644)

	out, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "h.txt",
		"oldText": "TARGET",
		"newText": "DONE",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	// Context of 3 around line 4 → lines 1..7
	for _, want := range []string{"1→L1", "4→DONE", "7→L7"} {
		if !strings.Contains(out, want) {
			t.Errorf("snippet missing %q in %q", want, out)
		}
	}
}

func TestEditfileReplaceAllConfusable(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "i.txt")
	os.WriteFile(path, []byte("\u201Ca\u201D and \u201Ca\u201D"), 0644)

	// Unique requirement without replace_all.
	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "i.txt",
		"oldText": `"a"`,
		"newText": `"b"`,
	})
	if err == nil {
		t.Fatal("expected multi-match error after normalization")
	}

	out, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":        "i.txt",
		"oldText":     `"a"`,
		"newText":     `"b"`,
		"replace_all": true,
	})
	if err != nil {
		t.Fatalf("replace_all confusable: %v", err)
	}
	if !strings.Contains(out, "2 occurrence") {
		t.Errorf("got %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != `"b" and "b"` {
		t.Errorf("content = %q", data)
	}
}

func TestBuildNearestMatchHint(t *testing.T) {
	file := "alpha\noCollMode_set, foo\nbeta\n"
	hint := buildNearestMatchHint(file, "oCollMode_set, wrong")
	if !strings.Contains(hint, "Nearest match: line 2") {
		t.Errorf("hint = %q", hint)
	}
	if buildNearestMatchHint(file, "zzzz_not_present") != "" {
		t.Error("missing keyword should yield empty hint")
	}
	if buildNearestMatchHint(file, "   \n") != "" {
		t.Error("whitespace-only oldText should yield empty hint")
	}
}

func TestEditSnippet(t *testing.T) {
	text := "one\ntwo NEW here\nthree\nfour\nfive\n"
	start := strings.Index(text, "NEW here")
	snip := editSnippet(text, start, "NEW here", 3)
	if !strings.Contains(snip, "1→one") || !strings.Contains(snip, "2→two NEW here") {
		t.Errorf("snippet = %q", snip)
	}
	if !strings.Contains(snip, "5→five") {
		t.Errorf("should include 3 lines after, got %q", snip)
	}
}

// ─────────────────────────────────────────────
// read_file line prefixes (Phase 1-2 / task #7550)
// ─────────────────────────────────────────────

func TestFormatReadFilePage(t *testing.T) {
	got := formatReadFilePage([]string{"a", "b", "c"}, 10)
	want := "10→a\n11→b\n12→c"
	if got != want {
		t.Errorf("formatReadFilePage = %q, want %q", got, want)
	}
	if formatReadFilePage(nil, 1) != "" {
		t.Error("empty page should be empty string")
	}
}

func TestReadfileLinePrefixesAbsoluteAcrossPages(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&sb, "body-%d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "p.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := tr.readfile(context.Background(), map[string]interface{}{
		"path": "p.txt", "offset": float64(10), "limit": float64(3),
	})
	if err != nil {
		t.Fatalf("readfile: %v", err)
	}
	// Absolute line numbers, not page-relative.
	for _, want := range []string{"10→body-10", "11→body-11", "12→body-12"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "1→body-10") {
		t.Error("must not renumber from 1 within a page")
	}
	// File on disk must remain unprefixed.
	raw, _ := os.ReadFile(filepath.Join(dir, "p.txt"))
	if strings.Contains(string(raw), "→") {
		t.Error("line prefixes must not be written to the file")
	}
}

func TestStripCopiedLinePrefixes(t *testing.T) {
	in := "3→hello\n4→world"
	got := stripCopiedLinePrefixes(in)
	if got != "hello\nworld" {
		t.Errorf("strip = %q", got)
	}
	// No prefixes: unchanged.
	if stripCopiedLinePrefixes("plain") != "plain" {
		t.Error("plain text should pass through")
	}
}

// Model pastes read_file display lines into oldText; edit_file must strip N→
// and still match the raw file bytes (without writing prefixes back).
func TestEditfileStripsReadFileLinePrefixes(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "m.txt")
	os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0644)

	out, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "m.txt",
		"oldText": "2→beta",
		"newText": "BETA",
	})
	if err != nil {
		t.Fatalf("edit with prefix: %v", err)
	}
	if !strings.Contains(out, "stripping read_file line-number prefixes") {
		t.Errorf("should note prefix strip recovery, got %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\nBETA\ngamma\n" {
		t.Errorf("file content = %q (prefixes must not be written)", data)
	}
}

// Multi-line oldText with per-line prefixes (typical copy from read_file page).
func TestEditfileStripsMultilineReadFilePrefixes(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "n.txt")
	os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0644)

	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "n.txt",
		"oldText": "2→two\n3→three",
		"newText": "TWO\nTHREE",
	})
	if err != nil {
		t.Fatalf("multiline prefix strip: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\nTWO\nTHREE\nfour\n" {
		t.Errorf("content = %q", data)
	}
}

// Full read → edit round-trip: model reads prefixed output, copies a line
// (with or without the prefix), and the edit lands on the real file bytes.
func TestReadfileEditfileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "rt.go")
	src := "package main\n\nfunc hello() {\n\treturn\n}\n"
	os.WriteFile(path, []byte(src), 0644)

	readOut, err := tr.readfile(context.Background(), map[string]interface{}{"path": "rt.go"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Prefixed display must include the target line.
	if !strings.Contains(readOut, "3→func hello() {") {
		t.Fatalf("read output missing target line prefix, got %q", readOut)
	}

	// Path A: model correctly strips the prefix (happy path).
	_, err = tr.editfile(context.Background(), map[string]interface{}{
		"path":    "rt.go",
		"oldText": "func hello() {",
		"newText": "func helloWorld() {",
	})
	if err != nil {
		t.Fatalf("edit without prefix: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "func helloWorld() {") {
		t.Fatalf("happy-path edit failed: %q", data)
	}
	if strings.Contains(string(data), "→") {
		t.Fatal("file must not contain display arrows")
	}

	// Reset and Path B: model pastes the prefixed line from read_file.
	os.WriteFile(path, []byte(src), 0644)
	// Re-read so state is clean (auto-page bookkeeping).
	tr2 := NewToolRegistry(dir, "test-sheep", nil, nil)
	readOut, err = tr2.readfile(context.Background(), map[string]interface{}{"path": "rt.go"})
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	// Extract the display line the model would copy.
	var prefixed string
	for _, line := range strings.Split(readOut, "\n") {
		if strings.Contains(line, "func hello()") {
			prefixed = line
			break
		}
	}
	if prefixed == "" || !strings.Contains(prefixed, "→") {
		t.Fatalf("could not find prefixed line in read output: %q", readOut)
	}
	_, err = tr2.editfile(context.Background(), map[string]interface{}{
		"path":    "rt.go",
		"oldText": prefixed,
		"newText": "func helloWorld() {",
	})
	if err != nil {
		t.Fatalf("edit with pasted prefix %q: %v", prefixed, err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "package main\n\nfunc helloWorld() {\n\treturn\n}\n" {
		t.Errorf("round-trip content = %q", data)
	}
}

func TestEditfileNotFoundHintWhenPrefixesPresent(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	path := filepath.Join(dir, "o.txt")
	os.WriteFile(path, []byte("only this line\n"), 0644)

	// Prefixed text that also doesn't exist after strip → not found + prefix hint.
	_, err := tr.editfile(context.Background(), map[string]interface{}{
		"path":    "o.txt",
		"oldText": "9→does not exist anywhere",
		"newText": "x",
	})
	if err == nil {
		t.Fatal("expected not found")
	}
	msg := err.Error()
	if !strings.Contains(msg, "line-number prefixes") && !strings.Contains(msg, "N→") {
		t.Errorf("expected prefix hint in not-found error, got %q", msg)
	}
}
