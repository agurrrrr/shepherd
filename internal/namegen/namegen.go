// Package namegen asks an LLM for fun, project-related sheep names (#638).
//
// It is deliberately separate from internal/names: names is imported by worker
// and project, and pulling LLM-call dependencies (embedded, llmcli) in there
// would risk import cycles. namegen depends on names for the shared rule book
// (ValidateCustom) but nothing depends on namegen except project registration
// and the suggest endpoint.
//
// One LLM call per Suggest, no tools, no session. Callers must treat failure as
// non-fatal and fall back to the name pool — project registration never fails
// because of naming.
package namegen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/llmcli"
	"github.com/agurrrrr/shepherd/internal/llmslots"
	"github.com/agurrrrr/shepherd/internal/names"
)

// Timeout bounds one Suggest call end to end (issue #638 §3: 30초).
const Timeout = 30 * time.Second

// Request describes what to name and which provider to ask.
type Request struct {
	ProjectName string
	Description string
	// UsedNames are sheep names already taken; candidates matching them are dropped.
	UsedNames []string
	// Provider overrides the stored setting. Empty = sheep_name_llm_provider.
	Provider string
	// Model overrides the stored setting. Empty = sheep_name_llm_model, then
	// the provider's own default (model_<provider> / embedded_active_id).
	Model string
}

// callProvider is a package variable so tests can fake LLM responses without
// spawning real CLI processes or hitting endpoints (#638 §9).
var callProvider = func(ctx context.Context, provider, model, prompt string) (string, error) {
	return callRealProvider(ctx, provider, model, prompt)
}

// Suggest returns validated candidate names, best first. It errors when the
// provider call fails, the answer cannot be parsed, or every candidate is
// invalid — callers fall back to the pool in all those cases.
func Suggest(ctx context.Context, req Request) ([]string, error) {
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = strings.TrimSpace(config.GetString("sheep_name_llm_provider"))
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(config.GetString("sheep_name_llm_model"))
	}
	if provider == "" {
		return nil, fmt.Errorf("no sheep-naming provider configured")
	}
	if provider == "magi" || provider == "auto" {
		return nil, fmt.Errorf("provider '%s' is not valid for sheep naming", provider)
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	prompt := buildPrompt(req)

	raw, err := callProvider(ctx, provider, model, prompt)
	if err != nil {
		return nil, fmt.Errorf("%s call failed: %w", provider, err)
	}

	return parseCandidates(raw, req.UsedNames)
}

// Enabled reports whether LLM sheep naming is switched on.
func Enabled() bool {
	return config.GetBool("sheep_name_llm_enabled")
}

// buildPrompt renders the naming instruction in the configured language.
func buildPrompt(req Request) string {
	language := strings.TrimSpace(config.GetString("language"))
	if language == "" {
		language = "ko"
	}

	var b strings.Builder
	if language == "ko" {
		b.WriteString("너는 프로젝트 담당 양(worker 에이전트)에게 지어주는 작명가다.\n")
		b.WriteString("아래 프로젝트와 관련된 재미있는 양 이름 후보 5개를 골라라.\n\n")
		b.WriteString("[프로젝트]\n")
		b.WriteString("이름: " + req.ProjectName + "\n")
		if req.Description != "" {
			b.WriteString("설명: " + req.Description + "\n")
		}
		b.WriteString("\n[이름 규칙]\n")
		b.WriteString("- 프로젝트 이름이나 설명과 뚜렷한 관계가 있어야 한다. 말장난, 줄임말, 비슷한 소리를 활용하라.\n")
		b.WriteString("- 양과 관련된 느낌을 넣는다. 예: 양, 메에, 뭉치, 털, 몽실\n")
		b.WriteString("- 2~10자를 권장한다. 32자를 넘기면 버려진다.\n")
		b.WriteString("- 공백, 제어 문자, 이모지, / \\ : * ? \" < > | 를 쓰지 않는다. '.'으로 시작하지 않는다.\n")
		b.WriteString("  (이름은 디렉터리 이름과 CLI 인자로 쓰인다.)\n")
		b.WriteString("- 예약어(shepherd, 목자)와 '양' 뒤에 숫자가 붙는 형식(양13 같은 자동 이름)은 금지다.\n")
		b.WriteString("\n[쓰면 안 되는 이름]\n")
		b.WriteString(formatUsedNames(req.UsedNames))
		b.WriteString("\n[형식]\n")
		b.WriteString("답은 JSON 하나로만 한다. 설명·코드펜스 없이 정확히 이렇게:\n")
		b.WriteString(`{"names":["이름1","이름2","이름3","이름4","이름5"]}`)
		b.WriteString("\n\n예시: 프로젝트 tangbisil → 탕비양, dotforge → 도트뭉치, nagar-mcp → 나가메에\n")
	} else {
		b.WriteString("You are the naming master for a project's sheep (an agent worker).\n")
		b.WriteString("Pick 5 fun sheep-name candidates related to the project below.\n\n")
		b.WriteString("[project]\n")
		b.WriteString("name: " + req.ProjectName + "\n")
		if req.Description != "" {
			b.WriteString("description: " + req.Description + "\n")
		}
		b.WriteString("\n[naming rules]\n")
		b.WriteString("- clearly related to the project name or description; use wordplay, abbreviations, similar sounds.\n")
		b.WriteString("- keep a sheepy feel (examples: sheep, baa, fluff, wool, cotton).\n")
		b.WriteString("- 2-10 characters preferred; anything over 32 is discarded.\n")
		b.WriteString("- no spaces, control characters, emoji, or / \\ : * ? \" < > | ; must not start with '.'\n")
		b.WriteString("  (the name becomes a directory name and a CLI argument.)\n")
		b.WriteString("- reserved words (shepherd) and auto-numbered forms like 'sheep13' are forbidden.\n")
		b.WriteString("\n[unusable names]\n")
		b.WriteString(formatUsedNames(req.UsedNames))
		b.WriteString("\n[format]\n")
		b.WriteString("Answer with exactly one JSON object, no prose, no code fences:\n")
		b.WriteString(`{"names":["name1","name2","name3","name4","name5"]}`)
	}
	return b.String()
}

func formatUsedNames(used []string) string {
	all := append([]string{}, names.ReservedNames...)
	all = append(all, used...)
	if len(all) == 0 {
		return "(없음 / none)\n"
	}
	return strings.Join(all, ", ") + "\n"
}

// callRealProvider performs the single-shot provider call. CLI providers run in
// an empty temp directory so the CLI cannot pick up CLAUDE.md, project MCP
// servers, or skills from a real project path (issue #638 §3: slower and more
// expensive otherwise).
func callRealProvider(ctx context.Context, provider, model, prompt string) (string, error) {
	switch provider {
	case "claude", "opencode", "pi", "grok":
		workDir, err := os.MkdirTemp("", "sheep-namegen-")
		if err != nil {
			return "", fmt.Errorf("temp workdir: %w", err)
		}
		defer os.RemoveAll(workDir)

		opts := llmcli.CallOptions{ModelID: model, WorkDir: workDir, UserPrompt: prompt}
		switch provider {
		case "claude":
			return llmcli.ClaudeCLI(ctx, opts)
		case "opencode":
			return llmcli.OpenCodeCLI(ctx, opts)
		case "pi":
			return llmcli.PiCLI(ctx, opts)
		default:
			return llmcli.GrokCLI(ctx, opts)
		}
	case "embedded":
		return callEmbedded(ctx, model, prompt)
	default:
		return "", fmt.Errorf("unknown provider '%s'", provider)
	}
}

// callEmbedded resolves the endpoint reference (empty model → embedded_active_id),
// then performs one tools-off Chat request. The per-endpoint concurrency gate is
// applied the same way as the MAGI embedded path: reuse an existing llmslots
// semaphore via Lookup, never create one here — syncEndpointSemaphores at server
// startup owns creation.
func callEmbedded(ctx context.Context, modelRef, prompt string) (string, error) {
	ref := modelRef
	if ref == "" {
		ref = strings.TrimSpace(config.GetString("embedded_active_id"))
	}
	ep, err := config.ResolveEmbeddedEndpoint(ref)
	if err != nil {
		return "", fmt.Errorf("embedded endpoint: %w", err)
	}
	if ep == nil {
		return "", fmt.Errorf("embedded endpoint '%s' not found or disabled", ref)
	}

	if sem := llmslots.Global().Lookup(ep.ID); sem != nil {
		if err := sem.Acquire(ctx); err != nil {
			return "", fmt.Errorf("embedded slot: %w", err)
		}
		defer sem.Release()
	}

	client := embedded.NewClientWithAuth(ep.BaseURL, ep.APIKey, ep.Model, ep.Auth, ep.GitHubToken)
	resp, err := client.Chat(ctx, &embedded.ChatRequest{
		Model: ep.Model,
		Messages: []embedded.ChatMessage{
			{Role: embedded.ChatRoleUser, Content: prompt},
		},
		MaxTokens: 512,
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("embedded returned no choices")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// parseCandidates strips code fences, parses {"names":[...]}, and filters:
// invalid names (names.ValidateCustom), auto-numbered pool forms, duplicates
// inside the response, and names already in use. Returns an error when nothing
// survives. Same fence-stripping shape as wiki/ingest.go's callLLMForIngest.
func parseCandidates(raw string, usedNames []string) ([]string, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if end := strings.LastIndex(s, "```"); end > 0 {
			firstNewline := strings.Index(s, "\n")
			if firstNewline > 0 && firstNewline < end {
				s = strings.TrimSpace(s[firstNewline+1 : end])
			} else {
				s = strings.TrimSpace(strings.TrimPrefix(s, "```json"))
				s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
			}
		}
	}

	var payload struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal([]byte(s), &payload); err != nil {
		return nil, fmt.Errorf("parse LLM answer: %w", err)
	}

	used := make(map[string]bool, len(usedNames))
	for _, n := range usedNames {
		used[n] = true
	}

	var out []string
	seen := make(map[string]bool)
	for _, cand := range payload.Names {
		cand = strings.TrimSpace(cand)
		if err := names.ValidateCustom(cand); err != nil {
			continue
		}
		if names.IsNumberedPoolName(cand) {
			continue
		}
		if used[cand] || seen[cand] {
			continue
		}
		seen[cand] = true
		out = append(out, cand)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no valid candidate names in LLM answer")
	}
	return out, nil
}
