package push

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/internal/config"
)

const (
	maxSubscriptions = 20
	storeFileName    = "push-subscriptions.json"
)

// Subscription is a Web Push subscription (PushSubscription.toJSON()).
type Subscription struct {
	Endpoint  string    `json:"endpoint"`
	Keys      Keys      `json:"keys"`
	UserAgent string    `json:"user_agent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Keys holds the client auth material from PushManager.subscribe.
type Keys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type storeFile struct {
	Subscriptions []Subscription `json:"subscriptions"`
}

var (
	storeMu           sync.Mutex
	storePathOverride string
)

// SetStorePath overrides the JSON file location. Empty restores the default
// (~/.shepherd/push-subscriptions.json).
func SetStorePath(path string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	storePathOverride = path
}

func storePath() string {
	if storePathOverride != "" {
		return storePathOverride
	}
	return filepath.Join(config.GetConfigDir(), storeFileName)
}

// List returns a copy of stored subscriptions.
func List() ([]Subscription, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	s, err := loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]Subscription, len(s.Subscriptions))
	copy(out, s.Subscriptions)
	return out, nil
}

// Count returns the number of stored subscriptions.
func Count() (int, error) {
	list, err := List()
	if err != nil {
		return 0, err
	}
	return len(list), nil
}

// Upsert stores or replaces a subscription keyed by endpoint.
func Upsert(sub Subscription) error {
	if err := validateSubscription(sub); err != nil {
		return err
	}
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = time.Now().UTC()
	}

	storeMu.Lock()
	defer storeMu.Unlock()
	s, err := loadLocked()
	if err != nil {
		return err
	}

	replaced := false
	for i, existing := range s.Subscriptions {
		if existing.Endpoint == sub.Endpoint {
			sub.CreatedAt = existing.CreatedAt
			s.Subscriptions[i] = sub
			replaced = true
			break
		}
	}
	if !replaced {
		if len(s.Subscriptions) >= maxSubscriptions {
			return fmt.Errorf("too many push subscriptions (max %d)", maxSubscriptions)
		}
		s.Subscriptions = append(s.Subscriptions, sub)
	}
	return saveLocked(s)
}

// Remove deletes the subscription with the given endpoint. Missing is a no-op.
func Remove(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}

	storeMu.Lock()
	defer storeMu.Unlock()
	s, err := loadLocked()
	if err != nil {
		return err
	}
	out := s.Subscriptions[:0]
	for _, existing := range s.Subscriptions {
		if existing.Endpoint != endpoint {
			out = append(out, existing)
		}
	}
	s.Subscriptions = out
	return saveLocked(s)
}

func removeLocked(endpoint string) error {
	s, err := loadLocked()
	if err != nil {
		return err
	}
	out := s.Subscriptions[:0]
	for _, existing := range s.Subscriptions {
		if existing.Endpoint != endpoint {
			out = append(out, existing)
		}
	}
	s.Subscriptions = out
	return saveLocked(s)
}

func loadLocked() (*storeFile, error) {
	path := storePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &storeFile{Subscriptions: []Subscription{}}, nil
		}
		return nil, err
	}
	var s storeFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("invalid push subscription store: %w", err)
	}
	if s.Subscriptions == nil {
		s.Subscriptions = []Subscription{}
	}
	return &s, nil
}

func saveLocked(s *storeFile) error {
	path := storePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validateSubscription(sub Subscription) error {
	endpoint := strings.TrimSpace(sub.Endpoint)
	if endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("endpoint must be a valid URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
			return fmt.Errorf("endpoint must be https")
		}
	default:
		return fmt.Errorf("endpoint must be https")
	}
	if strings.TrimSpace(sub.Keys.P256dh) == "" || strings.TrimSpace(sub.Keys.Auth) == "" {
		return fmt.Errorf("keys.p256dh and keys.auth are required")
	}
	return nil
}
