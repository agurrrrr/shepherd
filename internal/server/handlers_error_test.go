package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/internal/config"
)

// The Web UI parses every /api reply as JSON. Failures raised before a handler
// runs — an oversized body that fasthttp refuses (413) — used to be answered
// with Fiber's plain-text default, and a reverse proxy in front of the daemon
// answers them with HTML. Either one reaches the browser as
// `Unexpected token '<', "<html> <h"... is not valid JSON`, which hides the limit
// the user has to raise. These tests pin the daemon side of that contract: every
// request-level failure is a JSON envelope naming the current limit.

// serveOnEphemeralPort starts app on 127.0.0.1:<free port> and returns the base
// URL. app.Test cannot be used for an oversized body: fasthttp writes the 413
// response and then reports the size error to Fiber, which surfaces it as a test
// error instead of a response. A real listener is what actually exercises the
// transport ceiling plus the ErrorHandler, which is the pair under test here.
func serveOnEphemeralPort(t *testing.T, app *fiber.App) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.ShutdownWithTimeout(2 * time.Second) })

	base := fmt.Sprintf("http://%s", ln.Addr().String())

	// Wait for the listener to accept, so the first POST is not a race.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return base
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not come up")
	return ""
}

// uploadAppWithBodyLimit enables the file browser and builds an app whose
// transport ceiling is 1 KB, so any real body is refused before
// handleUploadFile can see it. That is exactly the shape of the bug: the answer
// must still be JSON, because the browser cannot tell a transport refusal from a
// handler one.
func uploadAppWithBodyLimit(t *testing.T) *fiber.App {
	t.Helper()
	viper.Set("enable_file_browser", true)
	viper.Set("max_upload_mb", 7)
	t.Cleanup(func() {
		viper.Set("enable_file_browser", false)
		viper.Set("max_upload_mb", config.DefaultMaxUploadMB)
	})

	cfg := fiberConfig()
	cfg.BodyLimit = 1024 // smaller than the body posted below

	s := &Server{hub: NewSSEHub()}
	app := fiber.New(cfg)
	app.Post("/api/projects/demo/files/upload", s.handleUploadFile)
	return app
}

// multipartBytes lives in handlers_files_test.go (same package) and is reused here.

// TestTransport413IsJSONAnswered is the regression test for the reported bug: an
// upload the transport refuses must answer with the daemon's JSON envelope, not
// plain text or HTML, and must name max_upload_mb so the message points at a knob.
func TestTransport413IsJSONAnswered(t *testing.T) {
	base := serveOnEphemeralPort(t, uploadAppWithBodyLimit(t))

	body, contentType := multipartBytes(t, "big.bin", bytes.Repeat([]byte("x"), 4096))
	req, err := http.NewRequest(http.MethodPost, base+"/api/projects/demo/files/upload?path=", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", resp.StatusCode, raw)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("content-type = %q, want application/json — text/plain or text/html is what the browser could not parse",
			resp.Header.Get("Content-Type"))
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("body is not JSON, so the browser throws: %v (body: %s)", err, raw)
	}
	if payload["success"] != false {
		t.Errorf("success = %v, want false", payload["success"])
	}
	msg, _ := payload["message"].(string)
	if !strings.Contains(msg, "7 MB") {
		t.Errorf("message = %q, want it to name the current 7 MB limit", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "proxy") {
		t.Errorf("message = %q, want the proxy/ingress hint — in practice the proxy's body-size limit is the one that fires", msg)
	}
}

// TestHandlerLevelErrorsAreJSONAnswered covers failures a handler returns rather
// than the transport. Fiber's default writes these as plain text too, so an API
// 404 would reach the UI as unparsable text.
func TestHandlerLevelErrorsAreJSONAnswered(t *testing.T) {
	app := fiber.New(fiberConfig())
	app.Get("/api/known", func(c *fiber.Ctx) error { return fiber.ErrNotFound })

	base := serveOnEphemeralPort(t, app)

	resp, err := http.Get(base + "/api/known")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", resp.StatusCode, raw)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("content-type = %q, want application/json", resp.Header.Get("Content-Type"))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("body is not JSON: %v (body: %s)", err, raw)
	}
	if payload["success"] != false {
		t.Errorf("success = %v, want false", payload["success"])
	}
}
