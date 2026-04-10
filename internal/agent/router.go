package agent

import (
	"strings"
)

// Router AI provider router
type Router struct {
	claude   *ClaudeProvider
	opencode *OpenCodeProvider
}

// NewRouter creates a new Router
func NewRouter() *Router {
	return &Router{
		claude:   NewClaudeProvider(),
		opencode: NewOpenCodeProvider(),
	}
}

// GetProvider returns a provider by type
func (r *Router) GetProvider(providerType ProviderType) Provider {
	switch providerType {
	case ProviderOpencode:
		if r.opencode.IsAvailable() {
			return r.opencode
		}
		return r.claude
	case ProviderClaude:
		return r.claude
	case ProviderAuto:
		return r.claude
	default:
		return r.claude
	}
}

// Route analyzes prompt and returns appropriate provider
func (r *Router) Route(prompt string, preferredProvider ProviderType) Provider {
	if preferredProvider == ProviderOpencode {
		if r.opencode.IsAvailable() {
			return r.opencode
		}
		return r.claude
	}
	if preferredProvider == ProviderClaude {
		return r.claude
	}

	if preferredProvider == ProviderAuto {
		if r.shouldUseOpenCode(prompt) && r.opencode.IsAvailable() {
			return r.opencode
		}
	}

	return r.claude
}

// shouldUseOpenCode analyzes prompt to decide if OpenCode should be used
func (r *Router) shouldUseOpenCode(prompt string) bool {
	lower := strings.ToLower(prompt)

	if strings.Contains(lower, "opencode") || strings.Contains(lower, "로컬") {
		return true
	}

	if strings.Contains(lower, "검토") || strings.Contains(lower, "리뷰") || strings.Contains(lower, "review") {
		return true
	}

	if strings.Contains(lower, "설명해") || strings.Contains(lower, "explain") {
		return true
	}

	return false
}

// Claude returns the Claude provider
func (r *Router) Claude() *ClaudeProvider {
	return r.claude
}

// OpenCode returns the OpenCode provider
func (r *Router) OpenCode() *OpenCodeProvider {
	return r.opencode
}

// IsOpenCodeAvailable checks if OpenCode is available
func (r *Router) IsOpenCodeAvailable() bool {
	return r.opencode.IsAvailable()
}

// IsClaudeAvailable checks if Claude is available
func (r *Router) IsClaudeAvailable() bool {
	return r.claude.IsAvailable()
}

// Backward compatibility
func (r *Router) Local() *OpenCodeProvider    { return r.opencode }
func (r *Router) IsLocalAvailable() bool      { return r.IsOpenCodeAvailable() }
