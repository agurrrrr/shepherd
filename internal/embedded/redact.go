package embedded

import (
	"regexp"
	"strings"
	"sync"

	"github.com/agurrrrr/shepherd/internal/config"
)

// Tool/bash output secret redaction.
//
// Applied at the common path every tool result takes before history storage
// (truncateToolResult) and SSE live preview (indentResult). High-confidence
// patterns only — avoid broad assignment-name matching that would mangle
// ordinary code the model is intentionally reading/writing.
//
// Pattern inspiration: grok-build xai-grok-secrets sanitizer (Rust), ported as
// thin Go regexes — no crate link. Replacements use [REDACTED:<type>] so the
// model still knows *what kind* of secret was scrubbed.

// secretsRedactionOverride, when non-nil, forces enable/disable (unit tests).
// nil means "read from config / default on".
var (
	secretsRedactionOverride *bool
	secretsRedactionMu       sync.RWMutex
)

// SetSecretsRedactionEnabled toggles tool-result secret redaction.
// Default is enabled. Primarily for tests and optional config wiring.
func SetSecretsRedactionEnabled(enabled bool) {
	secretsRedactionMu.Lock()
	defer secretsRedactionMu.Unlock()
	v := enabled
	secretsRedactionOverride = &v
}

// ResetSecretsRedactionEnabled clears a test override so config/default applies.
func ResetSecretsRedactionEnabled() {
	secretsRedactionMu.Lock()
	defer secretsRedactionMu.Unlock()
	secretsRedactionOverride = nil
}

func secretsRedactionEnabled() bool {
	secretsRedactionMu.RLock()
	override := secretsRedactionOverride
	secretsRedactionMu.RUnlock()
	if override != nil {
		return *override
	}
	// Unset (config.Init not called, or key absent) → default ON.
	// After Init, SetDefault("embedded_redact_secrets", true) makes Get return true.
	v := config.Get("embedded_redact_secrets")
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "false", "0", "off", "no":
			return false
		default:
			return true
		}
	default:
		return true
	}
}

// Pre-compiled high-confidence patterns. Order in redactSecrets matters for
// nested cases (e.g. PEM body before JWT-looking base64 fragments inside it).
var (
	// API keys: sk- / sk_ / sk-ant- / xai- with a long enough token body.
	// \b stops "task-"/"disk-"/"risk-" from matching a stray "sk-".
	reAPIKeyPrefix = regexp.MustCompile(`\b(?:sk-ant-|sk[-_]|xai-)[A-Za-z0-9_-]{20,}`)

	// AWS long-term (AKIA) and temporary (ASIA) access-key IDs.
	reAWSAccessKey = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)

	// GitHub classic PATs + fine-grained.
	reGitHubToken = regexp.MustCompile(`\b(?:gh[opusr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`)

	// GitLab PATs and Slack bot/user/app tokens.
	reVendorToken = regexp.MustCompile(`\b(?:glpat-|xox[abp]-|xapp-)[A-Za-z0-9-]{10,}`)

	// Google API keys (AIza + 35 chars).
	reGoogleAPIKey = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)

	// PEM private-key block (any key type), body included. (?s) so . spans newlines.
	rePEMPrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)

	// Bare JWT: eyJ header.payload.signature (3 base64url parts).
	reJWT = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
)

// redactSecrets replaces high-confidence secret shapes with [REDACTED:<type>].
// Returns s unchanged when redaction is disabled or nothing matches.
func redactSecrets(s string) string {
	if s == "" || !secretsRedactionEnabled() {
		return s
	}
	// Fast path: skip the replace chain when no pattern can match.
	if !secretsLikelyPresent(s) {
		return s
	}

	out := s
	out = rePEMPrivateKey.ReplaceAllString(out, "[REDACTED:pem]")
	out = reAPIKeyPrefix.ReplaceAllString(out, "[REDACTED:api_key]")
	out = reAWSAccessKey.ReplaceAllString(out, "[REDACTED:aws_key]")
	out = reGitHubToken.ReplaceAllString(out, "[REDACTED:github_token]")
	out = reVendorToken.ReplaceAllString(out, "[REDACTED:vendor_token]")
	out = reGoogleAPIKey.ReplaceAllString(out, "[REDACTED:google_api_key]")
	out = reJWT.ReplaceAllString(out, "[REDACTED:jwt]")
	return out
}

// secretsLikelyPresent is a cheap pre-filter so clean tool output does not pay
// for a full multi-regex replace chain on every call.
func secretsLikelyPresent(s string) bool {
	return strings.Contains(s, "sk-") ||
		strings.Contains(s, "sk_") ||
		strings.Contains(s, "xai-") ||
		strings.Contains(s, "AKIA") ||
		strings.Contains(s, "ASIA") ||
		strings.Contains(s, "ghp_") ||
		strings.Contains(s, "gho_") ||
		strings.Contains(s, "ghu_") ||
		strings.Contains(s, "ghs_") ||
		strings.Contains(s, "ghr_") ||
		strings.Contains(s, "github_pat_") ||
		strings.Contains(s, "glpat-") ||
		strings.Contains(s, "xoxa-") ||
		strings.Contains(s, "xoxb-") ||
		strings.Contains(s, "xoxp-") ||
		strings.Contains(s, "xapp-") ||
		strings.Contains(s, "AIza") ||
		strings.Contains(s, "BEGIN ") ||
		strings.Contains(s, "eyJ")
}
