package magi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
)

// callEndpoint sends one no-tools chat request and returns the final message
// content, usage, and any error. It is the single seam for tests — override
// via the package variable to inject fakes.
//
// Design §8 Phase 1: advisory deliberation is tool-free. The wiring layer
// handles tool-augmented execution separately.
var callEndpoint = func(ctx context.Context, ep EndpointRef, systemPrompt, userPrompt string, temperature float32, maxTokens int) (string, embedded.ChatUsage, error) {
	client := embedded.NewClient(ep.BaseURL, ep.APIKey, ep.Model)

	req := &embedded.ChatRequest{
		Model: ep.Model,
		Messages: []embedded.ChatMessage{
			{Role: embedded.ChatRoleSystem, Content: systemPrompt},
			{Role: embedded.ChatRoleUser, Content: userPrompt},
		},
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Stream:      true,
		// No Tools — advisory mode (design §8 Phase 1).
	}

	msg, _, usage, err := client.AccumulateStreamWithRetry(ctx, req, nil)
	if err != nil {
		return "", embedded.ChatUsage{}, err
	}

	// Guard: nil message or empty content is a failure.
	if msg == nil || msg.Content == "" {
		return "", embedded.ChatUsage{}, fmt.Errorf("empty response from %s", ep.ID)
	}

	content := msg.Content

	// usage may be nil when the server omits it — return zero-value.
	var u embedded.ChatUsage
	if usage != nil {
		u = *usage
	}

	return content, u, nil
}

// RunProposersOptions bundles inputs for one blind parallel round.
type RunProposersOptions struct {
	Proposers   []ProposerSpec
	BaseSystem  string        // base system prompt from the wiring layer
	UserPrompts []string      // per-slot user prompt (round 1: all identical; debate round: per-slot)
	Timeout     time.Duration // per-proposer timeout (design: default 120s, set by caller)
	Temperature float32       // 0 → default 0.7 (diversity)
	OnOutput    func(string)  // live output sink, may be nil
}

// RunProposers calls every proposer in parallel and returns one result per
// slot, in slot order. Individual failures are recorded in Result.Err —
// callers decide whether enough succeeded (design §5.1).
//
// Uses sync.WaitGroup (not errgroup) so that one failure does not cancel
// the context for the others. Each proposer gets its own per-call timeout
// so the slowest model cannot hold the round hostage.
func RunProposers(ctx context.Context, opts RunProposersOptions) []ProposerResult {
	results := make([]ProposerResult, len(opts.Proposers))

	temp := opts.Temperature
	if temp == 0 {
		temp = 0.7 // diversity default (design §5.1)
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, spec := range opts.Proposers {
		wg.Add(1)
		go func(slot int, sp ProposerSpec) {
			defer wg.Done()

			result := ProposerResult{Spec: sp}

			// Per-proposer timeout (design §5.1).
			callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()

			// Build the persona-augmented system prompt.
			systemPrompt := BuildProposerSystemPrompt(opts.BaseSystem, sp, slot)

			// Select the user prompt for this slot.
			userPrompt := ""
			if slot < len(opts.UserPrompts) {
				userPrompt = opts.UserPrompts[slot]
			}

			// Compute max tokens: ContextTokens / 4 (same rule as the
			// embedded agent loop). Fall back to DefaultContextTokens.
			ctxTokens := sp.Endpoint.ContextTokens
			if ctxTokens == 0 {
				ctxTokens = embedded.DefaultContextTokens
			}
			maxTokens := ctxTokens / 4

			content, usage, err := callEndpoint(callCtx, sp.Endpoint, systemPrompt, userPrompt, temp, maxTokens)
			if err != nil {
				result.Err = err
				emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, false, 0, err))
				results[slot] = result
				return
			}

			// Separate the self-reported confidence from the answer body.
			cleaned, conf := ExtractConfidence(content)

			// Content gate: tool-call text or empty prose is a failure, not
			// an answer — record it like a transport error so the wiring
			// fallback can engage (lesson from task #7031).
			if gateErr := CheckAnswerContent(cleaned); gateErr != nil {
				result.Err = gateErr
				emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, false, 0, gateErr))
				results[slot] = result
				return
			}

			result.Answer = cleaned
			result.Confidence = conf
			result.Usage = usage

			emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, true, conf, nil))
			results[slot] = result
		}(i, spec)
	}

	wg.Wait()
	return results
}

// SuccessfulResults filters out failed slots, preserving order.
func SuccessfulResults(results []ProposerResult) []ProposerResult {
	out := make([]ProposerResult, 0, len(results))
	for _, r := range results {
		if r.Err == nil {
			out = append(out, r)
		}
	}
	return out
}

// emitOutput safely calls OnOutput under mutex. No-op when OnOutput is nil.
func emitOutput(mu *sync.Mutex, onOutput func(string), line string) {
	if onOutput == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	onOutput(line)
}

// formatProposerLine builds the live-output line for one proposer's completion.
// Format (design §5.2):
//   success: "  🔬 MELCHIOR-1 (qwen3-27b) 응답 완료 — 신뢰도 8/10\n"
//   failure: "  🔬 MELCHIOR-1 (qwen3-27b) 응답 실패 — <err>\n"
// When confidence is -1 (not reported), shows "신뢰도 미보고".
func formatProposerLine(spec ProposerSpec, slot int, success bool, confidence int, err error) string {
	emoji := PersonaEmoji(spec)
	displayName := PersonaDisplayName(spec, slot)
	model := spec.Endpoint.Model

	if success {
		confStr := "신뢰도 미보고"
		if confidence >= 0 {
			confStr = fmt.Sprintf("신뢰도 %d/10", confidence)
		}
		return fmt.Sprintf("  %s %s (%s) 응답 완료 — %s\n", emoji, displayName, model, confStr)
	}

	return fmt.Sprintf("  %s %s (%s) 응답 실패 — %v\n", emoji, displayName, model, err)
}
