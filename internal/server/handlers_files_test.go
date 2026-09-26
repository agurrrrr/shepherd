package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/internal/db"
)

// newUploadApp enables the file browser (off by default unless config.Load ran)
// and returns an app wired to the upload route.
func newUploadApp(t *testing.T) *fiber.App {
	t.Helper()
	viper.Set("enable_file_browser", true)
	t.Cleanup(func() { viper.Set("enable_file_browser", false) })

	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Post("/api/projects/:name/files/upload", s.handleUploadFile)
	return app
}

func multipartBody(t *testing.T, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("files", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

func postUpload(t *testing.T, app *fiber.App, path, contentType string, body *bytes.Buffer) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/demo/files/upload?path="+path, body)
	req.Header.Set("Content-Type", contentType)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return resp.StatusCode, payload
}

func TestHandleUploadFile_SavesIntoCurrentDir(t *testing.T) {
	withServerTestDB(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db.Client().Project.Create().SetName("demo").SetPath(root).SaveX(context.Background())

	body, ct := multipartBody(t, "hello.txt", "hello world")
	code, payload := postUpload(t, newUploadApp(t), "sub", ct, body)

	if code != http.StatusOK {
		t.Fatalf("status = %d, payload = %v", code, payload)
	}
	if payload["success"] != true {
		t.Fatalf("expected success, got %v", payload)
	}
	got, err := os.ReadFile(filepath.Join(root, "sub", "hello.txt"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("content = %q, want %q", got, "hello world")
	}
}

func TestHandleUploadFile_StripsFilenameTraversal(t *testing.T) {
	withServerTestDB(t)
	root := t.TempDir()
	db.Client().Project.Create().SetName("demo").SetPath(root).SaveX(context.Background())

	body, ct := multipartBody(t, "../evil.txt", "x")
	code, _ := postUpload(t, newUploadApp(t), "", ct, body)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if _, err := os.Stat(filepath.Join(root, "evil.txt")); err != nil {
		t.Errorf("expected file sanitized into root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.txt")); err == nil {
		t.Errorf("file escaped project root")
	}
}

func TestHandleUploadFile_RejectsPathEscape(t *testing.T) {
	withServerTestDB(t)
	root := t.TempDir()
	db.Client().Project.Create().SetName("demo").SetPath(root).SaveX(context.Background())

	body, ct := multipartBody(t, "a.txt", "x")
	code, _ := postUpload(t, newUploadApp(t), "%2E%2E%2Foutside", ct, body)

	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestHandleUploadFile_RejectsHiddenDir(t *testing.T) {
	withServerTestDB(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db.Client().Project.Create().SetName("demo").SetPath(root).SaveX(context.Background())

	body, ct := multipartBody(t, "a.txt", "x")
	code, _ := postUpload(t, newUploadApp(t), ".git", ct, body)

	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
}
