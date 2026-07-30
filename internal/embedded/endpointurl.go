package embedded

import (
	"net/url"
	"strings"
)

// Endpoint URL resolution.
//
// shepherd used to treat a configured endpoint URL as an OpenAI *base* URL: it
// forced a "/v1" suffix and appended "/chat/completions" at call time. That
// locked the embedded provider to servers laid out exactly like the OpenAI API
// and made gateways with any other path (or a query string, e.g. Azure's
// ?api-version=) unusable.
//
// The configured URL is now the full chat-completions URL and is sent verbatim.
// The only exception is the legacy shape shepherd itself used to produce — a
// URL whose path is empty or ends in "/v1" — which is expanded so existing
// configs keep working without an edit.

// ResolveChatURL returns the exact URL to POST chat completions to for the
// given configured endpoint URL. Anything that is not a legacy OpenAI base is
// returned untouched (trailing slash, query and fragment included).
func ResolveChatURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		// Not parseable — hand it to the HTTP layer as-is so the error the
		// user sees names their actual URL.
		return s
	}

	p := strings.TrimRight(u.Path, "/")
	switch {
	case p == "":
		// Legacy: bare host. Old code appended "/v1" and then the call path.
		u.Path = "/v1/chat/completions"
	case strings.EqualFold(lastSegment(p), "v1"):
		// Legacy: OpenAI base URL.
		u.Path = p + "/chat/completions"
	default:
		return s
	}
	return u.String()
}

// ResolveModelsURL returns the "/models" URL matching a chat-completions URL,
// or "" when the URL does not follow the OpenAI layout and no sibling
// "/models" endpoint can be assumed. Used for health checks only.
func ResolveModelsURL(chatURL string) string {
	u, err := url.Parse(strings.TrimSpace(chatURL))
	if err != nil {
		return ""
	}
	p := strings.TrimRight(u.Path, "/")
	const suffix = "/chat/completions"
	if len(p) < len(suffix) || !strings.EqualFold(p[len(p)-len(suffix):], suffix) {
		return ""
	}
	u.Path = p[:len(p)-len(suffix)] + "/models"
	return u.String()
}

// lastSegment returns the final path segment of a slash-separated path.
func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
