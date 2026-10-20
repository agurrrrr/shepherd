package embedded

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GitHub Copilot direct integration.
//
// Copilot exposes an OpenAI-compatible chat-completions API, but it cannot be
// used with a static API key: the long-lived GitHub token must be exchanged for
// a short-lived Copilot API token, and requests need Copilot-specific headers.
// This file implements the OAuth device flow, the token exchange/cache, and the
// /models lookup used by the Settings UI to fill per-model context windows.

const (
	// AuthCopilot selects GitHub Copilot authentication on an endpoint.
	AuthCopilot = "copilot"
	// CopilotClientID is the public GitHub Copilot OAuth app client id used by
	// the official Copilot clients for the device flow.
	CopilotClientID = "Iv1.b507a08c87ecfe98"
	// CopilotDefaultAPIBase is the fallback Copilot API host. The token
	// exchange response usually carries the account-specific endpoint
	// (individual/business/enterprise) in endpoints.api.
	CopilotDefaultAPIBase = "https://api.githubcopilot.com"
	// copilotTokenRefreshMargin refreshes the short-lived token before expiry so
	// a long streaming request never starts with an almost-dead token.
	copilotTokenRefreshMargin = 5 * time.Minute
)

// Overridable for tests.
var (
	copilotDeviceCodeURL    = "https://github.com/login/device/code"
	copilotAccessTokenURL   = "https://github.com/login/oauth/access_token"
	copilotTokenExchangeURL = "https://api.github.com/copilot_internal/v2/token"
)

// copilotHTTPClient is used for authentication and model discovery calls, not
// for chat streams (those use Client.httpClient and have no overall timeout).
var copilotHTTPClient = &http.Client{Timeout: 30 * time.Second}

// CopilotDeviceCode is the response of the GitHub device-flow start call.
type CopilotDeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// Copilot poll statuses.
const (
	CopilotPollAuthorized = "authorized"
	CopilotPollPending    = "pending"
	CopilotPollSlowDown   = "slow_down"
	CopilotPollExpired    = "expired"
	CopilotPollDenied     = "denied"
	CopilotPollError      = "error"
)

// CopilotPollResult is the result of one device-flow poll.
type CopilotPollResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	// GitHubToken is only set when Status == "authorized".
	GitHubToken string `json:"-"`
}

// StartCopilotDeviceFlow asks GitHub for a user code the operator enters at
// https://github.com/login/device.
func StartCopilotDeviceFlow(ctx context.Context) (*CopilotDeviceCode, error) {
	body, _ := json.Marshal(map[string]string{
		"client_id": CopilotClientID,
		"scope":     "read:user",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotDeviceCodeURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("copilot device flow: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")

	resp, err := copilotHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot device flow: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("copilot device flow: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out CopilotDeviceCode
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("copilot device flow: decode response: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return nil, fmt.Errorf("copilot device flow: incomplete response")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 900
	}
	if out.VerificationURI == "" {
		out.VerificationURI = "https://github.com/login/device"
	}
	return &out, nil
}

// PollCopilotDeviceFlow performs one poll. Callers drive the interval loop; the
// statuses are mapped from GitHub's OAuth error codes.
func PollCopilotDeviceFlow(ctx context.Context, deviceCode string) (*CopilotPollResult, error) {
	if strings.TrimSpace(deviceCode) == "" {
		return nil, fmt.Errorf("copilot device flow: device_code is required")
	}
	body, _ := json.Marshal(map[string]string{
		"client_id":   CopilotClientID,
		"device_code": deviceCode,
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotAccessTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("copilot device poll: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")

	resp, err := copilotHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot device poll: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var tokenResp struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &tokenResp); err != nil {
		return nil, fmt.Errorf("copilot device poll: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if tokenResp.AccessToken != "" {
		return &CopilotPollResult{Status: CopilotPollAuthorized, GitHubToken: tokenResp.AccessToken}, nil
	}

	switch tokenResp.Error {
	case "authorization_pending":
		return &CopilotPollResult{Status: CopilotPollPending}, nil
	case "slow_down":
		return &CopilotPollResult{Status: CopilotPollSlowDown}, nil
	case "expired_token":
		return &CopilotPollResult{Status: CopilotPollExpired, Message: "device code expired; start again"}, nil
	case "access_denied":
		return &CopilotPollResult{Status: CopilotPollDenied, Message: "authorization was denied"}, nil
	case "":
		return &CopilotPollResult{Status: CopilotPollError, Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}, nil
	default:
		msg := tokenResp.ErrorDescription
		if msg == "" {
			msg = tokenResp.Error
		}
		return &CopilotPollResult{Status: CopilotPollError, Message: msg}, nil
	}
}

// CopilotAPIToken is the short-lived token returned by the exchange endpoint.
type CopilotAPIToken struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	RefreshIn int    `json:"refresh_in"`
	Endpoints struct {
		API string `json:"api"`
	} `json:"endpoints"`
}

// ExchangeCopilotToken exchanges a GitHub token for a Copilot API token.
func ExchangeCopilotToken(ctx context.Context, githubToken string) (*CopilotAPIToken, error) {
	if strings.TrimSpace(githubToken) == "" {
		return nil, fmt.Errorf("copilot: github token is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotTokenExchangeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("copilot token exchange: create request: %w", err)
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("Accept", "application/json")
	for k, v := range copilotHeaders() {
		req.Header.Set(k, v)
	}

	resp, err := copilotHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot token exchange: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("copilot token exchange: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var tok CopilotAPIToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("copilot token exchange: decode response: %w", err)
	}
	if tok.Token == "" {
		return nil, fmt.Errorf("copilot token exchange: empty token (is a Copilot subscription active?)")
	}
	if tok.Endpoints.API == "" {
		tok.Endpoints.API = CopilotDefaultAPIBase
	}
	return &tok, nil
}

type copilotCacheEntry struct {
	token     *CopilotAPIToken
	expiresAt time.Time
}

var copilotTokenCache = struct {
	sync.Mutex
	entries map[[32]byte]*copilotCacheEntry
}{entries: make(map[[32]byte]*copilotCacheEntry)}

func copilotCacheKey(githubToken string) [32]byte {
	return sha256.Sum256([]byte(githubToken))
}

func copilotTokenExpiry(tok *CopilotAPIToken, now time.Time) time.Time {
	if tok.ExpiresAt > 0 {
		return time.Unix(tok.ExpiresAt, 0)
	}
	if tok.RefreshIn > 0 {
		return now.Add(time.Duration(tok.RefreshIn) * time.Second)
	}
	return now.Add(25 * time.Minute)
}

// CopilotToken returns a cached Copilot API token, refreshing it when it is
// within the refresh margin of expiry.
func CopilotToken(ctx context.Context, githubToken string) (*CopilotAPIToken, error) {
	githubToken = strings.TrimSpace(githubToken)
	if githubToken == "" {
		return nil, fmt.Errorf("copilot: github token is required")
	}
	key := copilotCacheKey(githubToken)
	now := time.Now()

	copilotTokenCache.Lock()
	entry := copilotTokenCache.entries[key]
	copilotTokenCache.Unlock()
	if entry != nil && now.Add(copilotTokenRefreshMargin).Before(entry.expiresAt) {
		return entry.token, nil
	}

	tok, err := ExchangeCopilotToken(ctx, githubToken)
	if err != nil {
		return nil, err
	}
	copilotTokenCache.Lock()
	copilotTokenCache.entries[key] = &copilotCacheEntry{
		token:     tok,
		expiresAt: copilotTokenExpiry(tok, now),
	}
	copilotTokenCache.Unlock()
	return tok, nil
}

// InvalidateCopilotToken drops the cached token so the next call re-exchanges.
// Used when the API rejects the current token with 401.
func InvalidateCopilotToken(githubToken string) {
	key := copilotCacheKey(strings.TrimSpace(githubToken))
	copilotTokenCache.Lock()
	delete(copilotTokenCache.entries, key)
	copilotTokenCache.Unlock()
}

// copilotHeaders returns the headers the Copilot API requires on every request.
// copilot-developer-cli is accepted for both OAuth tokens and PATs; vscode-chat
// is rejected for PATs.
func copilotHeaders() map[string]string {
	return map[string]string{
		"User-Agent":             "GitHubCopilotChat/0.26.7",
		"Editor-Version":         "vscode/1.100.0",
		"Editor-Plugin-Version":  "copilot-chat/0.26.7",
		"Copilot-Integration-Id": "copilot-developer-cli",
		"Openai-Intent":          "conversation-panel",
		"X-Initiator":            "user",
	}
}

// CopilotModel is a chat model advertised by the Copilot /models endpoint,
// flattened to the fields shepherd cares about.
type CopilotModel struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	ContextWindowTokens int      `json:"context_window_tokens"`
	MaxPromptTokens     int      `json:"max_prompt_tokens"`
	MaxOutputTokens     int      `json:"max_output_tokens"`
	SupportsTools       bool     `json:"supports_tools"`
	SupportsVision      bool     `json:"supports_vision"`
	SupportedEndpoints  []string `json:"supported_endpoints,omitempty"`
}

// ContextTokens returns the input budget to use for trimming/handoff: the
// model's max_prompt_tokens when advertised, otherwise the full context window.
func (m CopilotModel) ContextTokens() int {
	if m.MaxPromptTokens > 0 {
		return m.MaxPromptTokens
	}
	return m.ContextWindowTokens
}

type copilotModelsResponse struct {
	Data []struct {
		ID                 string          `json:"id"`
		Name               string          `json:"name"`
		Capabilities       json.RawMessage `json:"capabilities"`
		SupportedEndpoints []string        `json:"supported_endpoints"`
	} `json:"data"`
}

type copilotCapabilities struct {
	Type   string `json:"type"`
	Limits struct {
		MaxContextWindowTokens int `json:"max_context_window_tokens"`
		MaxPromptTokens        int `json:"max_prompt_tokens"`
		MaxOutputTokens        int `json:"max_output_tokens"`
	} `json:"limits"`
	Supports struct {
		ToolCalls bool `json:"tool_calls"`
		Vision    bool `json:"vision"`
	} `json:"supports"`
}

func copilotModelsURL(apiBase string) string {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = CopilotDefaultAPIBase
	}
	return base + "/models"
}

// ListCopilotModels exchanges the GitHub token and returns chat models usable
// through /chat/completions, plus the account-specific API base URL.
func ListCopilotModels(ctx context.Context, githubToken string) ([]CopilotModel, string, error) {
	tok, err := CopilotToken(ctx, githubToken)
	if err != nil {
		return nil, "", err
	}
	apiBase := strings.TrimRight(tok.Endpoints.API, "/")
	if apiBase == "" {
		apiBase = CopilotDefaultAPIBase
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotModelsURL(apiBase), nil)
	if err != nil {
		return nil, apiBase, fmt.Errorf("copilot models: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Accept", "application/json")
	for k, v := range copilotHeaders() {
		req.Header.Set(k, v)
	}

	resp, err := copilotHTTPClient.Do(req)
	if err != nil {
		return nil, apiBase, fmt.Errorf("copilot models: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if resp.StatusCode == http.StatusUnauthorized {
		InvalidateCopilotToken(githubToken)
		return nil, apiBase, fmt.Errorf("copilot models: HTTP 401: %s", strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiBase, fmt.Errorf("copilot models: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed copilotModelsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, apiBase, fmt.Errorf("copilot models: decode response: %w", err)
	}

	out := make([]CopilotModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		var caps copilotCapabilities
		if len(m.Capabilities) > 0 {
			_ = json.Unmarshal(m.Capabilities, &caps)
		}
		if !copilotModelSupportsChat(m.SupportedEndpoints, caps.Type) {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		out = append(out, CopilotModel{
			ID:                  m.ID,
			Name:                name,
			Type:                caps.Type,
			ContextWindowTokens: caps.Limits.MaxContextWindowTokens,
			MaxPromptTokens:     caps.Limits.MaxPromptTokens,
			MaxOutputTokens:     caps.Limits.MaxOutputTokens,
			SupportsTools:       caps.Supports.ToolCalls,
			SupportsVision:      caps.Supports.Vision,
			SupportedEndpoints:  m.SupportedEndpoints,
		})
	}
	return out, apiBase, nil
}

func copilotModelSupportsChat(endpoints []string, capabilityType string) bool {
	if capabilityType != "" && capabilityType != "chat" {
		return false
	}
	if len(endpoints) == 0 {
		return true
	}
	for _, ep := range endpoints {
		if strings.TrimRight(ep, "/") == "/chat/completions" {
			return true
		}
	}
	return false
}
