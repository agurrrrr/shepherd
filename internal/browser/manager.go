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
)

// Manager is the global browser session manager.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	baseDir  string
}

var (
	globalManager *Manager
	managerOnce   sync.Once
)

// GetManager returns the singleton manager.
func GetManager() *Manager {
	managerOnce.Do(func() {
		homeDir, _ := os.UserHomeDir()
		baseDir := filepath.Join(homeDir, ".shepherd", "browser")
		os.MkdirAll(baseDir, 0755)

		globalManager = &Manager{
			sessions: make(map[string]*Session),
			baseDir:  baseDir,
		}
	})
	return globalManager
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
		return sess, nil
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

	userDataDir := filepath.Join(m.baseDir, sheepName)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create UserDataDir: %w", err)
	}

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

	if err := sess.Close(); err != nil {
		return err
	}

	delete(m.sessions, sheepName)
	return nil
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
