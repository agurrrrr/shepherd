package server

import (
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/internal/config"
)

// The daemon reads max_upload_mb per request, so the HTTP transport ceiling has
// to move with it too. Otherwise a raised limit is invisible: the handler would
// accept the file, but fasthttp refuses the body first and answers 413 before
// any handler runs. watchBodyLimit installs fasthttp's HeaderReceived hook for
// that reason; these tests pin down that the hook reports the current setting.

func TestFiberConfigBodyLimitFollowsSetting(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	viper.SetDefault("max_upload_mb", config.DefaultMaxUploadMB)
	startup := fiberConfig().BodyLimit
	if int64(startup) <= config.GetMaxUploadBytes() {
		t.Fatalf("startup BodyLimit %d must exceed the per-file upload limit %d",
			startup, config.GetMaxUploadBytes())
	}

	viper.Set("max_upload_mb", 300)
	raised := fiberConfig().BodyLimit
	if int64(raised) <= 300*1024*1024 {
		t.Errorf("BodyLimit after raising the setting = %d, want it above 300 MB", raised)
	}
	if raised <= startup {
		t.Errorf("BodyLimit did not grow with the setting: %d -> %d", startup, raised)
	}
}

// TestWatchBodyLimitAppliesWithoutRestart is the hot-reload guarantee: one app,
// hook installed once, then the setting changes. The next request's ceiling must
// already be the new one, with no restart and no re-install.
func TestWatchBodyLimitAppliesWithoutRestart(t *testing.T) {
	viper.Reset()
	defer viper.Reset()
	viper.SetDefault("max_upload_mb", config.DefaultMaxUploadMB)

	app := fiber.New(fiberConfig())
	watchBodyLimit(app)

	hook := app.Server().HeaderReceived
	if hook == nil {
		t.Fatal("watchBodyLimit did not install a HeaderReceived hook")
	}

	first := hook(nil).MaxRequestBodySize
	if first != config.GetHTTPBodyLimitBytes() {
		t.Fatalf("first request ceiling = %d, want %d", first, config.GetHTTPBodyLimitBytes())
	}

	viper.Set("max_upload_mb", 200)
	second := hook(nil).MaxRequestBodySize
	if int64(second) <= 200*1024*1024 {
		t.Errorf("ceiling after the setting changed = %d, want it above 200 MB", second)
	}
	if second <= first {
		t.Errorf("ceiling did not follow the raised setting: %d -> %d", first, second)
	}

	// A zero MaxRequestBodySize would mean "fall back to the server default", so
	// the hook must never return one.
	viper.Set("max_upload_mb", "broken")
	if got := hook(nil).MaxRequestBodySize; got <= 0 {
		t.Errorf("ceiling with a broken setting = %d, want a positive fallback", got)
	}
}
