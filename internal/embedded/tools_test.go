package embedded

import (
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
	out, err := tr.readfile(map[string]interface{}{"path": "photo.jpg"})
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
	out, err = tr.readfile(map[string]interface{}{"path": "note.txt"})
	if err != nil {
		t.Fatalf("readfile text returned error: %v", err)
	}
	if out != "just text" {
		t.Errorf("expected text content, got %q", out)
	}
}
