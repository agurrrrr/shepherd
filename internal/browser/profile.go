package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// prepareProfileDir makes a persistent UserDataDir safe to reuse across runs.
//
// Because each sheep keeps a long-lived Chromium profile (so 2FA cookies and
// logged-in sessions survive restarts), we must defend against two failure
// modes that otherwise break reuse:
//
//  1. Stale Singleton locks. If the previous Chromium was SIGKILL'd or the
//     daemon crashed, files like SingletonLock/SingletonSocket/SingletonCookie
//     remain and the next launch with the same UserDataDir aborts with
//     "profile appears to be in use". We clear them before launching since the
//     manager guarantees only one live session per profile.
//
//  2. The "restore session" / "Chrome didn't shut down correctly" bubble. After
//     an unclean exit Chromium marks the profile crashed and shows a restore
//     prompt that steals focus and can replay old tabs. We patch the profile
//     Preferences to report a clean exit so automation starts on a blank slate
//     while still reusing the stored cookies.
//
// All steps are best-effort: a fresh profile has none of these files yet, and a
// failure here should never block launching.
func prepareProfileDir(userDataDir string) {
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		_ = os.Remove(filepath.Join(userDataDir, name))
	}
	markProfileExitedCleanly(userDataDir)
}

// markProfileExitedCleanly rewrites the profile Preferences so Chromium does not
// treat the previous (possibly killed) session as a crash. Cookies live in a
// separate SQLite store, so this only suppresses the restore bubble and does not
// drop the persisted login state.
func markProfileExitedCleanly(userDataDir string) {
	prefsPath := filepath.Join(userDataDir, "Default", "Preferences")
	data, err := os.ReadFile(prefsPath)
	if err != nil {
		// No Preferences yet (first run) — nothing to patch.
		return
	}

	var prefs map[string]interface{}
	if err := json.Unmarshal(data, &prefs); err != nil {
		return
	}

	profile, ok := prefs["profile"].(map[string]interface{})
	if !ok {
		profile = map[string]interface{}{}
		prefs["profile"] = profile
	}
	profile["exit_type"] = "Normal"
	profile["exited_cleanly"] = true

	patched, err := json.Marshal(prefs)
	if err != nil {
		return
	}
	_ = os.WriteFile(prefsPath, patched, 0600)
}
