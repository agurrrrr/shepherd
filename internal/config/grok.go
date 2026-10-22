package config

import (
	"context"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/internal/procutil"
)

// grok (Grok Build TUI / xAI) exposes a small set of models that depends on the
// logged-in account and the installed CLI version, so the selectable list is
// read from `grok models` instead of being hard-coded. The output looks like:
//
//	You are logged in with grok.com.
//
//	Default model: grok-4.7
//
//	Available models:
//	  * grok-4.7 (default)
//	  - grok-4.7-build-fast
//	  - grok-4.6
//	  - grok-4.5
//
// IDs are passed straight to `grok -m <id>`.

// GrokModelOption describes a single selectable grok model.
type GrokModelOption struct {
	ID        string `json:"id"`        // passed to `grok -m`
	Label     string `json:"label"`     // human-readable display name
	IsDefault bool   `json:"isDefault"` // grok's own default when no -m is given
}

// grokFallbackModels is used only when `grok models` cannot run (CLI missing,
// signed out, network down). It keeps the Settings selector usable; the UI shows
// why the fallback is in effect through the error string returned by the cache.
func grokFallbackModels() []GrokModelOption {
	return []GrokModelOption{
		{ID: "grok-4.6", Label: "grok-4.6 (fallback list)"},
		{ID: "grok-4.5", Label: "grok-4.5"},
	}
}

// grokModelsTimeout bounds `grok models`, which contacts the leader process and
// may hit the network for the account's model list.
const grokModelsTimeout = 20 * time.Second

var grokModelLineRe = regexp.MustCompile(`^[-*]\s+(\S+)`)

// ListGrokCLIModels runs `grok models` in dir and returns the models it prints,
// preserving CLI order. The account default is taken from the "* <id> (default)"
// bullet, falling back to the "Default model: <id>" line.
func ListGrokCLIModels(ctx context.Context, dir string) ([]GrokModelOption, error) {
	ctx, cancel := context.WithTimeout(ctx, grokModelsTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, GetGrokBinary(), "models")
	cmd.Dir = dir
	procutil.HideWindow(cmd)
	// grok writes the list to stdout, but a login/banner notice may land on
	// stderr; capture both so parsing never sees an empty stream.
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		combined := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
		if combined != "" {
			return nil, errText(err, combined)
		}
		return nil, err
	}
	return parseGrokModelsOutput(stdout.String()), nil
}

// errText wraps a CLI error together with the CLI's own message.
type cliError struct {
	err  error
	text string
}

func (e *cliError) Error() string { return e.err.Error() + ": " + e.text }

func errText(err error, text string) error { return &cliError{err: err, text: text} }

// parseGrokModelsOutput extracts model IDs from `grok models` output. Bullet
// lines ("* id (default)" / "- id") are the authoritative set; everything else
// (login notice, headers) is ignored.
func parseGrokModelsOutput(out string) []GrokModelOption {
	out = ansiEscapeRe.ReplaceAllString(out, "")

	var models []GrokModelOption
	seen := map[string]bool{}
	defaultID := ""

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "Default model:"); ok {
			defaultID = strings.TrimSpace(rest)
			continue
		}
		m := grokModelLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true

		isDefault := strings.Contains(line, "(default)") || id == defaultID
		label := id
		if isDefault {
			label = id + " (default)"
		}
		models = append(models, GrokModelOption{ID: id, Label: label, IsDefault: isDefault})
	}

	if defaultID == "" && len(models) > 0 {
		for i := range models {
			if models[i].IsDefault {
				defaultID = models[i].ID
				break
			}
		}
	}
	return models
}

var grokModelsCache struct {
	mu        sync.RWMutex
	models    []GrokModelOption
	defaultID string
	fetched   bool
	err       string
	fetchedAt time.Time
}

// InitGrokModelList queries `grok models` once at daemon startup and caches the
// result, mirroring InitOpenCodeModelList. The Settings selector reads only this
// cache, so the CLI is not spawned per request; the Grok settings tab can ask
// for a refresh explicitly.
func InitGrokModelList() {
	refreshGrokModels()
}

// CachedGrokModels returns the cached `grok models` list, fetching it on first
// use when the daemon has not initialized the cache yet. When the CLI fails the
// curated fallback list is returned instead of an empty selector.
func CachedGrokModels() []GrokModelOption {
	c := &grokModelsCache
	c.mu.RLock()
	if c.fetched {
		models := c.models
		c.mu.RUnlock()
		if len(models) == 0 {
			return grokFallbackModels()
		}
		return models
	}
	c.mu.RUnlock()

	models := refreshGrokModels()
	if len(models) == 0 {
		return grokFallbackModels()
	}
	return models
}

// CachedGrokDefaultModel returns the account default model reported by
// `grok models`, or "" when it is unknown.
func CachedGrokDefaultModel() string {
	c := &grokModelsCache
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.defaultID
}

// GrokModelsCacheInfo reports whether the cached list came from the CLI and, if
// not, why. The Settings tab shows this so a stale fallback list is visible.
func GrokModelsCacheInfo() (source string, errText string, fetchedAt time.Time) {
	c := &grokModelsCache
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.fetched {
		return "pending", "", time.Time{}
	}
	if len(c.models) == 0 {
		return "fallback", c.err, c.fetchedAt
	}
	return "cli", c.err, c.fetchedAt
}

// RefreshGrokModels re-runs `grok models` and replaces the cache. Used by the
// Settings refresh button so new xAI models appear without a daemon restart.
func RefreshGrokModels() []GrokModelOption {
	return refreshGrokModels()
}

func refreshGrokModels() []GrokModelOption {
	dir, _ := os.UserHomeDir()
	models, err := ListGrokCLIModels(context.Background(), dir)

	errText := ""
	if err != nil {
		errText = err.Error()
		log.Printf("[grok] `grok models` failed: %v", err)
	}

	defaultID := ""
	for _, m := range models {
		if m.IsDefault {
			defaultID = m.ID
			break
		}
	}

	c := &grokModelsCache
	c.mu.Lock()
	c.models = models
	c.defaultID = defaultID
	c.err = errText
	c.fetchedAt = time.Now()
	c.fetched = true
	c.mu.Unlock()

	return models
}
