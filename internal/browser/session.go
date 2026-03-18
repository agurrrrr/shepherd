package browser

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Session represents a browser instance assigned to a sheep.
type Session struct {
	mu          sync.RWMutex
	sheepName   string
	browser     *rod.Browser
	launcher    *launcher.Launcher
	pages       map[string]*rod.Page
	userDataDir string
	headless    bool
	proxy       string
	defaultPage string
	Debug       *DebugState
}

// GetBrowser returns the browser instance.
func (s *Session) GetBrowser() *rod.Browser {
	return s.browser
}

// GetPage returns a page by name (empty string returns the default page).
func (s *Session) GetPage(name string) *rod.Page {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if name == "" {
		name = s.defaultPage
	}
	return s.pages[name]
}

// GetOrCreatePage returns a page by name, creating it if it doesn't exist.
func (s *Session) GetOrCreatePage(name string) (*rod.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name == "" {
		name = "default"
	}

	if page, ok := s.pages[name]; ok {
		return page, nil
	}

	page, err := s.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("failed to create page: %w", err)
	}

	s.pages[name] = page
	if s.defaultPage == "" {
		s.defaultPage = name
	}

	return page, nil
}

// OpenPage opens a URL and returns the page.
func (s *Session) OpenPage(url string, name string) (*rod.Page, error) {
	page, err := s.GetOrCreatePage(name)
	if err != nil {
		return nil, err
	}

	if err := page.Navigate(url); err != nil {
		return nil, fmt.Errorf("failed to navigate: %w", err)
	}

	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("failed to wait for page load: %w", err)
	}

	return page, nil
}

// ClosePage closes a page by name.
func (s *Session) ClosePage(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if name == "" {
		name = s.defaultPage
	}

	page, ok := s.pages[name]
	if !ok {
		return nil
	}

	if err := page.Close(); err != nil {
		return fmt.Errorf("failed to close page: %w", err)
	}

	delete(s.pages, name)

	if name == s.defaultPage {
		s.defaultPage = ""
		for n := range s.pages {
			s.defaultPage = n
			break
		}
	}

	return nil
}

// ListPages returns info about all open pages.
func (s *Session) ListPages() []PageInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos := make([]PageInfo, 0, len(s.pages))
	for name, page := range s.pages {
		info := PageInfo{
			Name:      name,
			IsDefault: name == s.defaultPage,
		}
		if pageInfo, err := page.Info(); err == nil {
			info.URL = pageInfo.URL
			info.Title = pageInfo.Title
		}
		infos = append(infos, info)
	}
	return infos
}

// SetDefaultPage sets the default page.
func (s *Session) SetDefaultPage(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.pages[name]; !ok {
		return fmt.Errorf("page '%s' not found", name)
	}
	s.defaultPage = name
	return nil
}

// Close closes the session and all pages.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Debug != nil {
		s.Debug.Close()
	}

	for name, page := range s.pages {
		page.Close()
		delete(s.pages, name)
	}

	if s.browser != nil {
		if err := s.browser.Close(); err != nil {
			return fmt.Errorf("failed to close browser: %w", err)
		}
	}

	return nil
}

// Info returns session information.
func (s *Session) Info() SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return SessionInfo{
		SheepName:   s.sheepName,
		UserDataDir: s.userDataDir,
		Headless:    s.headless,
		Proxy:       s.proxy,
		PageCount:   len(s.pages),
	}
}

// PageInfo holds page metadata.
type PageInfo struct {
	Name      string
	URL       string
	Title     string
	IsDefault bool
}

// SessionInfo holds session metadata.
type SessionInfo struct {
	SheepName   string
	UserDataDir string
	Headless    bool
	Proxy       string
	PageCount   int
}

// DefaultTimeout is the default timeout for element operations.
const DefaultTimeout = 30 * time.Second
