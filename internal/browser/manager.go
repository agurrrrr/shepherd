package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"

	"github.com/agurrrrr/shepherd/internal/config"
)

// Manager is the global browser session manager.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

var (
	globalManager *Manager
	managerOnce   sync.Once
)

// GetManager returns the singleton manager. The profile root lives under the
// config directory — config.GetConfigDir()/browser, i.e. ~/.shepherd/browser —
// rather than a hard-coded os.UserHomeDir() path (#638). Same location in
// production, but tests that call config.UseTempConfigDir get automatic
// isolation, which Rename's profile-move logic needs. ProfileDir resolves the
// base live on every call so a test that swaps the config dir mid-process
// still sees consistent paths.
func GetManager() *Manager {
	managerOnce.Do(func() {
		globalManager = &Manager{
			sessions: make(map[string]*Session),
		}
	})
	return globalManager
}

// ProfileDir returns the on-disk browser profile directory for a sheep.
func (m *Manager) ProfileDir(sheepName string) string {
	return filepath.Join(config.GetConfigDir(), "browser", sheepName)
}

// HasSession reports whether the sheep currently has an open browser session.
func (m *Manager) HasSession(sheepName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[sheepName]
	return ok
}

// PrepareProfileRename validates that a sheep's browser profile can move from
// oldName to newName. It fails when a profile directory already exists under
// the new name (its saved cookies would be lost) or when an open session is
// holding the old directory. Called before the DB write so a conflict aborts
// the rename without leaving DB and FS out of sync.
func PrepareProfileRename(oldName, newName string) error {
	m := GetManager()
	if m.HasSession(oldName) {
		return fmt.Errorf("sheep '%s' has an open browser session — close it before renaming", oldName)
	}
	if _, err := os.Stat(m.ProfileDir(newName)); err == nil {
		return fmt.Errorf("browser profile for '%s' already exists at %s", newName, m.ProfileDir(newName))
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to check browser profile: %w", err)
	}
	return nil
}

// CommitProfileRename moves the profile directory from oldName to newName.
// Missing source directory is a no-op (success). Run it after PrepareProfileRename
// passed and after the DB name change, so failures can be rolled back.
func CommitProfileRename(oldName, newName string) error {
	m := GetManager()
	oldDir := m.ProfileDir(oldName)
	newDir := m.ProfileDir(newName)
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to check browser profile: %w", err)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("failed to move browser profile from %s to %s: %w", oldDir, newDir, err)
	}
	return nil
}

// GetSession returns a session for the given sheep (nil if not found).
func (m *Manager) GetSession(sheepName string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[sheepName]
}

// GetOrCreateSession returns a session for the sheep, creating one if needed.
func (m *Manager) GetOrCreateSession(sheepName string, opts *SessionOptions) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sess, ok := m.sessions[sheepName]; ok {
		// A persistent-profile chromium can die while this entry lingers (crash,
		// SIGKILL, daemon restart). Returning the corpse makes session_start a
		// silent no-op: callers get "started" but every action fails on the dead
		// connection. Verify liveness; if dead, tear it down and relaunch.
		if sess.IsAlive() {
			return sess, nil
		}
		_ = sess.Close()
		delete(m.sessions, sheepName)
	}

	sess, err := m.createSession(sheepName, opts)
	if err != nil {
		return nil, err
	}

	m.sessions[sheepName] = sess
	return sess, nil
}

func (m *Manager) createSession(sheepName string, opts *SessionOptions) (*Session, error) {
	if opts == nil {
		opts = DefaultSessionOptions()
	}

	userDataDir := m.ProfileDir(sheepName)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create UserDataDir: %w", err)
	}

	// The profile is persistent and reused across runs so 2FA/login cookies
	// survive restarts. Clear stale locks and crash flags left by a previous
	// unclean exit so reopening the same profile doesn't fail or pop a
	// "restore session" prompt.
	prepareProfileDir(userDataDir)

	// Leakless(false) on Windows only: go-rod's default watchdog binary
	// extraction to temp dir is flagged by AV/EDR on Windows, failing
	// security audits. On Linux/Mac the default Leakless(true) is kept
	// for proper cleanup. On Windows we rely on the manager's shutdown
	// path to close sessions; if SIGKILL'd, chromium children may survive.
	l := launcher.New().
		Leakless(runtime.GOOS != "windows").
		UserDataDir(userDataDir).
		Headless(opts.Headless).
		Set(flags.Flag("no-sandbox")).
		Set(flags.Flag("disable-setuid-sandbox")).
		// Anti-automation-detection: without these, Chrome exposes
		// navigator.webdriver=true and the AutomationControlled blink
		// feature, which sites like Google's sign-in detect and respond to
		// with captcha + login-reset loops (task #5988). Removing the
		// enable-automation switch and disabling the blink feature makes the
		// browser indistinguishable enough to complete normal logins.
		Set(flags.Flag("disable-blink-features"), "AutomationControlled").
		Delete("enable-automation")

	if opts.Proxy != "" {
		l = l.Proxy(opts.Proxy)
	}

	url, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w", err)
	}

	browser := rod.New().ControlURL(url)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to browser: %w", err)
	}

	sess := &Session{
		sheepName:   sheepName,
		browser:     browser,
		launcher:    l,
		pages:       make(map[string]*rod.Page),
		userDataDir: userDataDir,
		headless:    opts.Headless,
		proxy:       opts.Proxy,
		Debug:       NewDebugState(),
	}

	return sess, nil
}

// CloseSession closes a specific sheep's browser session.
func (m *Manager) CloseSession(sheepName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sheepName]
	if !ok {
		return nil
	}

	// Always drop the map entry, even if Close() errors (e.g. the browser
	// already died and the CDP connection is closed). Otherwise a dead session
	// lingers and blocks a clean relaunch on the next session_start.
	err := sess.Close()
	delete(m.sessions, sheepName)
	return err
}

// CloseAll closes all browser sessions.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for name, sess := range m.sessions {
		if err := sess.Close(); err != nil {
			lastErr = err
		}
		delete(m.sessions, name)
	}
	return lastErr
}

// ListSessions returns the names of all active sessions.
func (m *Manager) ListSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.sessions))
	for name := range m.sessions {
		names = append(names, name)
	}
	return names
}

// SessionOptions holds options for creating a browser session.
type SessionOptions struct {
	Headless bool
	Proxy    string
}

// DefaultSessionOptions returns the default session options.
func DefaultSessionOptions() *SessionOptions {
	return &SessionOptions{
		Headless: true,
		Proxy:    "",
	}
}
