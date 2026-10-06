package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"
)

// withAuthTestEnv seeds in-memory credentials (admin / old-pass) and keeps
// credential writes away from the real config file.
func withAuthTestEnv(t *testing.T) (oldSecret string) {
	t.Helper()
	hash, err := HashPassword("old-pass")
	if err != nil {
		t.Fatal(err)
	}
	oldSecret = "old-secret"
	viper.Set("auth_username", "admin")
	viper.Set("auth_password_hash", hash)
	viper.Set("auth_jwt_secret", oldSecret)

	orig := persistConfig
	persistConfig = func(values map[string]interface{}) error {
		for k, v := range values {
			viper.Set(k, v)
		}
		return nil
	}
	t.Cleanup(func() {
		persistConfig = orig
		viper.Set("auth_username", "")
		viper.Set("auth_password_hash", "")
		viper.Set("auth_jwt_secret", "")
	})
	return oldSecret
}

func newAuthApp() *fiber.App {
	s := &Server{}
	app := fiber.New()
	app.Post("/api/auth/refresh", s.handleRefresh)
	api := app.Group("/api", AuthMiddleware(func() string {
		return viper.GetString("auth_jwt_secret")
	}))
	api.Put("/auth/credentials", s.handleChangeCredentials)
	api.Get("/ping", func(c *fiber.Ctx) error { return success(c, nil) })
	return app
}

func authRequest(t *testing.T, app *fiber.App, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestChangeCredentials_WrongCurrentPassword(t *testing.T) {
	oldSecret := withAuthTestEnv(t)
	app := newAuthApp()
	tokens, err := issueTokens("admin", oldSecret)
	if err != nil {
		t.Fatal(err)
	}

	status, body := authRequest(t, app, "PUT", "/api/auth/credentials", tokens.AccessToken, ChangeCredentialsRequest{
		CurrentPassword: "wrong",
		NewPassword:     "new-pass",
	})
	// Must not be 401, which the web client treats as an expired session.
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%v)", status, body)
	}
	if got := viper.GetString("auth_jwt_secret"); got != oldSecret {
		t.Fatalf("secret rotated on failed change: %q", got)
	}
	if err := ComparePassword(viper.GetString("auth_password_hash"), "old-pass"); err != nil {
		t.Fatal("password changed despite wrong current password")
	}
}

func TestChangeCredentials_UpdatesAndRevokesOldTokens(t *testing.T) {
	oldSecret := withAuthTestEnv(t)
	app := newAuthApp()
	old, err := issueTokens("admin", oldSecret)
	if err != nil {
		t.Fatal(err)
	}

	status, body := authRequest(t, app, "PUT", "/api/auth/credentials", old.AccessToken, ChangeCredentialsRequest{
		CurrentPassword: "old-pass",
		NewUsername:     "  shepherd  ",
		NewPassword:     "new-pass",
	})
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", status, body)
	}

	if got := viper.GetString("auth_username"); got != "shepherd" {
		t.Fatalf("username = %q, want trimmed %q", got, "shepherd")
	}
	if err := ComparePassword(viper.GetString("auth_password_hash"), "new-pass"); err != nil {
		t.Fatal("new password does not match stored hash")
	}
	if viper.GetString("auth_jwt_secret") == oldSecret {
		t.Fatal("JWT secret was not rotated")
	}

	data, _ := body["data"].(map[string]any)
	newAccess, _ := data["access_token"].(string)
	if data["username"] != "shepherd" || newAccess == "" || data["refresh_token"] == "" {
		t.Fatalf("unexpected response data: %v", data)
	}

	if status, _ := authRequest(t, app, "GET", "/api/ping", newAccess, nil); status != fiber.StatusOK {
		t.Fatalf("new access token rejected: %d", status)
	}
	if status, _ := authRequest(t, app, "GET", "/api/ping", old.AccessToken, nil); status != fiber.StatusUnauthorized {
		t.Fatalf("old access token still accepted: %d", status)
	}
	if status, _ := authRequest(t, app, "POST", "/api/auth/refresh", old.RefreshToken, nil); status != fiber.StatusUnauthorized {
		t.Fatalf("old refresh token still accepted: %d", status)
	}
}

func TestChangeCredentials_UsernameOnlyKeepsPassword(t *testing.T) {
	oldSecret := withAuthTestEnv(t)
	app := newAuthApp()
	tokens, err := issueTokens("admin", oldSecret)
	if err != nil {
		t.Fatal(err)
	}

	status, body := authRequest(t, app, "PUT", "/api/auth/credentials", tokens.AccessToken, ChangeCredentialsRequest{
		CurrentPassword: "old-pass",
		NewUsername:     "root",
	})
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (%v)", status, body)
	}
	if got := viper.GetString("auth_username"); got != "root" {
		t.Fatalf("username = %q, want root", got)
	}
	if err := ComparePassword(viper.GetString("auth_password_hash"), "old-pass"); err != nil {
		t.Fatal("password changed on a username-only update")
	}
}

func TestChangeCredentials_Validation(t *testing.T) {
	oldSecret := withAuthTestEnv(t)
	app := newAuthApp()
	tokens, err := issueTokens("admin", oldSecret)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		req  ChangeCredentialsRequest
	}{
		{"nothing to change", ChangeCredentialsRequest{CurrentPassword: "old-pass"}},
		{"same username only", ChangeCredentialsRequest{CurrentPassword: "old-pass", NewUsername: "admin"}},
		{"username with space", ChangeCredentialsRequest{CurrentPassword: "old-pass", NewUsername: "a b"}},
		{"username too long", ChangeCredentialsRequest{CurrentPassword: "old-pass", NewUsername: strings.Repeat("u", 65)}},
		{"password too long", ChangeCredentialsRequest{CurrentPassword: "old-pass", NewPassword: strings.Repeat("p", 73)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := authRequest(t, app, "PUT", "/api/auth/credentials", tokens.AccessToken, tc.req)
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%v)", status, body)
			}
			if got := viper.GetString("auth_jwt_secret"); got != oldSecret {
				t.Fatalf("secret rotated on rejected change: %q", got)
			}
		})
	}
}
