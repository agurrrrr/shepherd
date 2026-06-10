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

func TestReadfileVisionImage(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	tr.SetVision(true)

	// A JPEG header (FF D8 FF) → image/jpeg via http.DetectContentType.
	imgBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	if err := os.WriteFile(filepath.Join(dir, "pic.jpg"), imgBytes, 0644); err != nil {
		t.Fatal(err)
	}

	out, err := tr.readfile(map[string]interface{}{"path": "pic.jpg"})
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
	out, err := tr.readfile(map[string]interface{}{"path": "pic.jpg"})
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
