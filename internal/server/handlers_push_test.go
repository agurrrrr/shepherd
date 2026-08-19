package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/internal/push"
)

func withPushTestEnv(t *testing.T) {
	t.Helper()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	viper.Set("webpush_vapid_public", pub)
	viper.Set("webpush_vapid_private", priv)
	viper.Set("webpush_enabled", true)
	viper.Set("webpush_notify_on_complete", true)
	viper.Set("webpush_notify_on_fail", true)
	push.SetStorePath(filepath.Join(t.TempDir(), "subs.json"))
	t.Cleanup(func() { push.SetStorePath("") })
}

func newPushApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{}
	app := fiber.New()
	app.Get("/api/push/status", s.handlePushStatus)
	app.Post("/api/push/subscribe", s.handlePushSubscribe)
	app.Post("/api/push/unsubscribe", s.handlePushUnsubscribe)
	app.Post("/api/push/test", s.handlePushTest)
	return app
}

func pushJSON(t *testing.T, app *fiber.App, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return resp.StatusCode, payload
}

func TestHandlePushSubscribe_유효한구독_저장(t *testing.T) {
	withPushTestEnv(t)
	app := newPushApp(t)

	code, payload := pushJSON(t, app, http.MethodPost, "/api/push/subscribe", map[string]any{
		"endpoint": "https://push.example/device-1",
		"keys":     map[string]string{"p256dh": "pk", "auth": "ak"},
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	if payload["success"] != true {
		t.Fatalf("success = %v", payload["success"])
	}
	n, err := push.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count = %d", n)
	}
}

func TestHandlePushSubscribe_키없으면400(t *testing.T) {
	withPushTestEnv(t)
	app := newPushApp(t)

	code, payload := pushJSON(t, app, http.MethodPost, "/api/push/subscribe", map[string]any{
		"endpoint": "https://push.example/device-1",
		"keys":     map[string]string{"p256dh": "", "auth": ""},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d payload=%v, want 400", code, payload)
	}
}

func TestHandlePushUnsubscribe_구독해제(t *testing.T) {
	withPushTestEnv(t)
	app := newPushApp(t)

	pushJSON(t, app, http.MethodPost, "/api/push/subscribe", map[string]any{
		"endpoint": "https://push.example/device-1",
		"keys":     map[string]string{"p256dh": "pk", "auth": "ak"},
	})
	code, payload := pushJSON(t, app, http.MethodPost, "/api/push/unsubscribe", map[string]any{
		"endpoint": "https://push.example/device-1",
	})
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	n, _ := push.Count()
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestHandlePushStatus_공개키포함(t *testing.T) {
	withPushTestEnv(t)
	app := newPushApp(t)

	code, payload := pushJSON(t, app, http.MethodGet, "/api/push/status", nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d payload=%v", code, payload)
	}
	data, _ := payload["data"].(map[string]any)
	if data["vapid_public_key"] == "" || data["vapid_public_key"] == nil {
		t.Errorf("missing vapid_public_key: %v", data)
	}
	if data["vapid_private_key"] != nil {
		t.Errorf("private key leaked: %v", data)
	}
}
