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

// ExecuteWithFallback executes with Claude, falls back to OpenCode on rate limit
func (r *Router) ExecuteWithFallback(workdir, prompt string, opts ExecuteOptions) (*Result, error) {
	result, err := r.claude.Execute(workdir, prompt, opts)
	if err != nil {
		if r.opencode.IsAvailable() && isRateLimitError(err) {
			return r.opencode.Execute(workdir, prompt, opts)
		}
		return nil, err
	}
	return result, nil
}

// ExecuteInteractiveWithFallback interactive execution with fallback
func (r *Router) ExecuteInteractiveWithFallback(workdir, sessionID, prompt string, opts InteractiveOptions) (*Result, error) {
	result, err := r.claude.ExecuteInteractive(workdir, sessionID, prompt, opts)
	if err != nil {
		if r.opencode.IsAvailable() && isRateLimitError(err) {
			return r.opencode.ExecuteInteractive(workdir, "", prompt, opts)
		}
		return nil, err
	}
	return result, nil
}

// isRateLimitError checks if the error is a rate limit error
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "too many requests") ||
		strings.Contains(errStr, "리미트") ||
		strings.Contains(errStr, "limit exceeded")
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
