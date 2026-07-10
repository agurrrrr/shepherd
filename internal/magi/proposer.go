package magi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/envutil"
)

// minPerTurnFraction limits how much of the convergence reserve can be spent on
// a single exploration LLM call. Each turn gets at least reserve/minPerTurnFraction;
// if less than that remains before convergeAt, the loop breaks to forced convergence.
const minPerTurnFraction = 3

// maxIdenticalToolCalls is how many times the exact same (tool, args) call may
// execute before further repeats are refused. Slow local models can loop on a
// mis-parameterized read, ballooning the context until convergence starves
// (task #7178: the same file was re-read 4+ times with the same bad offset).
const maxIdenticalToolCalls = 2

// Context handoff thresholds (fraction of ep.ContextTokens). When the
// accumulated message history exceeds the soft threshold, an in-place
// context refresh (summarize + reset) is attempted — once at most. When
// it exceeds the hard threshold, exploration stops immediately and forced
// convergence takes over, preventing "scan SSE: context deadline exceeded"
// caused by the local LLM choking on an oversized prompt (task #7164).
const (
	ctxHandoffSoft = 65 // % of ContextTokens → trigger in-place refresh
	ctxHandoffHard = 85 // % of ContextTokens → stop exploration, force converge

	maxInPlaceHandoffs = 1 // safety: at most one refresh per proposer run
)

// handoffSummaryDirective is appended as a user turn when requesting an
// in-place context refresh. The model must produce a self-contained summary
// of its investigation so far — no tool calls — which then replaces the
// bloated message history.
const handoffSummaryDirective = `컨텍스트가 한계에 가까워졌다. 지금까지 조사한 내용을 간결하지만 완전하게 요약하라.
파일 경로, 주요 발견, 코드 구조, 결정사항을 빠짐없이 포함하라.
이 요약은 이후 조사를 이어가기 위한 유일한 맥락이 되므로, 다음 단계에서 무엇을 해야 하는지도 명시하라.
도구는 호출하지 마라.`

// minConvergenceReserve is the wall-clock time reserved at the tail of a
// proposer's budget to force a final answer once tool exploration must stop.
// Without a reserve, the forced (tools-off) request would race the hard
// deadline and be cut off before producing an answer.
const minConvergenceReserve = 20 * time.Second

// convergenceDirective is appended (as a user turn) when a proposer must stop
// exploring and produce its final answer — because the wall-clock budget is
// nearly spent. Tools are dropped from that request so the model cannot keep
// investigating.
const convergenceDirective = `이제 추가 조사(도구 사용)를 멈추고, 지금까지 확인한 내용만으로 최종 답변을 작성하라.
더 이상 도구를 호출할 수 없다. 완결된 최종 답변을 쓰고, 마지막 줄에 "CONFIDENCE: <0-10 정수>"를 추가하라.`

// finalAnswerNudge re-prompts a proposer whose last turn produced no usable
// answer — an empty response, or tool-call markup emitted as text (which the
// content gate rejects; task #7077 CASPER). Exactly one nudge is allowed before
// the proposer is declared failed, so a single unusable turn must not discard
// the whole deliberation (lesson from task #7066).
const finalAnswerNudge = `직전 응답에는 실질적인 최종 답변이 없다(빈 응답이거나 도구 호출 형식만 반환됨).
더 이상 도구를 호출하지 말고, 지금까지 확인한 맥락만으로 완결된 최종 답변을 산문으로 작성하라.
마지막 줄에 "CONFIDENCE: <0-10 정수>"를 포함하라.`

// salvageMarker is appended to a partial answer adopted after a convergence
// timeout. It tells the judge (and step-09's confidence nudge, which must NOT
// re-ask an endpoint that just proved too slow) that the answer was cut off.
const salvageMarker = "[부분 응답 — 수렴 시간 초과로 중단됨]"

// Reask budget (task #7205): one tools-off reask request gets a slice of the
// slot's effective proposer timeout — effTimeout/reaskBudgetFraction clamped
// to [reaskBudgetMin, reaskBudgetMax]. The reask prompt is small (capped task
// + one previous answer), so it needs far less than a full exploration budget.
const (
	reaskBudgetFraction = 3
	reaskBudgetMin      = 30 * time.Second
	reaskBudgetMax      = 120 * time.Second
)

// confidenceReaskDirective asks a proposer whose gate-passing answer lacked
// the "CONFIDENCE:" self-report to restate its final answer with one. An
// unreported confidence weakens the judge's weighting (task #7182).
const confidenceReaskDirective = `너의 답변에 "CONFIDENCE: <0-10 정수>" 신뢰도 보고가 빠져 있다.
위의 이전 답변을 검토하고, 필요한 수정만 반영하여 완결된 최종 답변을 다시 작성하라.
도구는 호출할 수 없다. 마지막 줄에 반드시 "CONFIDENCE: <0-10 정수>"를 추가하라.`

// convergenceDietTokens is the absolute prompt-size threshold (estimated
// tokens) above which a summarization handoff runs right before forced
// convergence. The existing handoff thresholds are percentages of the context
// window, but prompt-evaluation throughput on a slow local model is a function
// of absolute size: ~40K tokens cannot be re-evaluated plus answered within a
// 150s reserve regardless of how large the window is (task #7205).
const convergenceDietTokens = 20000

// chatTurn performs one streaming chat request and returns the assistant
// message plus token usage. It is a package variable so tests can drive the
// proposer mini agent loop (callEndpoint) — including the tool-exploration,
// forced-convergence, and empty-response-nudge paths — without a live LLM.
var chatTurn = func(ctx context.Context, client *embedded.Client, req *embedded.ChatRequest, onToken func(string)) (*embedded.ChatMessage, embedded.ChatUsage, error) {
	// Proposer retry budget (task #7077): short, ctx-bounded — a dead endpoint
	// fails fast instead of exhausting the main-agent's patient retry policy and
	// consuming the whole per-proposer timeout.
	msg, _, usage, err := client.AccumulateStreamProposer(ctx, req, nil, onToken)
	var u embedded.ChatUsage
	if usage != nil {
		u = *usage
	}
	return msg, u, err
}

// callEndpoint sends a chat request — with optional read-only tools — and runs
// a mini agent loop until the model produces a final text answer. It is a
// package variable so higher-level tests can inject a fake wholesale.
//
// Boundary (design: 라운드 카운트 제거 — 타임아웃만으로 경계): there is no
// tool-round count. Tool exploration runs until the per-proposer wall-clock
// budget (the ctx deadline set in RunProposers) is nearly spent. A tail of that
// budget (see minConvergenceReserve) is reserved so that, just before the
// deadline, a forced convergence request — tools removed — can demand a final
// answer from whatever context has accumulated. This replaces the old
// "exceeded max tool rounds → hard failure → discard all work" behavior: the
// boundary is now a soft convergence trigger, never a discard (task #7066).
//
// Empty answers get exactly one nudge before the proposer is declared failed,
// so a single empty turn does not collapse the deliberation.
//
// onToken (may be nil) receives live content deltas for streaming UI.
var callEndpoint = func(
	ctx context.Context,
	ep EndpointRef,
	systemPrompt, userPrompt string,
	temperature float32,
	maxTokens int,
	onToken func(string),
	tools []embedded.OpenAIToolDef,
	dispatch embedded.MCPDispatcher,
	projectPath, sheepName string,
) (string, embedded.ChatUsage, error) {
	client := embedded.NewClient(ep.BaseURL, ep.APIKey, ep.Model)

	messages := []embedded.ChatMessage{
		{Role: embedded.ChatRoleSystem, Content: systemPrompt},
		{Role: embedded.ChatRoleUser, Content: userPrompt},
	}

	var totalUsage embedded.ChatUsage

	// No tools → single-shot request; nudge once if the answer comes back empty.
	if len(tools) == 0 {
		content, u, err := forceFinalAnswer(ctx, client, ep, messages, temperature, maxTokens, onToken, false)
		addUsage(&totalUsage, u)
		return content, totalUsage, err
	}

	// Create a per-proposer ToolRegistry for native tools (read_file, grep,
	// glob). MCP tools are routed through the shared dispatch function.
	var toolRegistry *embedded.ToolRegistry
	if projectPath != "" {
		// Build MCPToolDefs from the OpenAIToolDef list so the ToolRegistry
		// knows about MCP tools for WantsSheepName checks.
		var mcpDefs []embedded.MCPToolDef
		for _, td := range tools {
			if td.Function.Name != "read_file" && td.Function.Name != "grep" && td.Function.Name != "glob" &&
				td.Function.Name != "write_file" && td.Function.Name != "edit_file" && td.Function.Name != "bash" {
				mcpDefs = append(mcpDefs, embedded.MCPToolDef{
					Name:        td.Function.Name,
					Description: td.Function.Description,
					Parameters:  td.Function.Parameters,
				})
			}
		}
		toolRegistry = embedded.NewToolRegistry(projectPath, sheepName, mcpDefs, dispatch)
	}

	// Compute the convergence cutoff and reserve from the ctx deadline. Tool
	// exploration runs until convergeAt; the reserved tail (reserve) funds a
	// forced final-answer request. In production RunProposers always sets a
	// deadline, so hasCutoff is true; the no-deadline branch exists only for
	// tests (which return a final answer promptly and never loop unbounded).
	convergeAt, reserve, hasCutoff := convergenceCutoff(ctx)

	// Context token budget for this endpoint. Used to detect when the
	// accumulated message history is approaching the model's context window
	// limit, triggering an in-place context refresh (task #7164).
	ctxTokens := ep.ContextTokens
	if ctxTokens == 0 {
		ctxTokens = embedded.DefaultContextTokens
	}

	// ── Tool exploration phase (bounded by wall clock, not a round count) ──
	turn := 0
	handoffCount := 0 // in-place context refreshes performed so far
	var exploreErr error // a non-cancel error mid-exploration → salvage, don't discard
	toolCallCount := make(map[string]int) // (tool, raw args) → executions so far
	for {
		// Approaching the deadline → stop exploring and force a final answer.
		if hasCutoff && !time.Now().Before(convergeAt) {
			break
		}

		turn++
		req := &embedded.ChatRequest{
			Model:       ep.Model,
			Messages:    messages,
			Temperature: temperature,
			MaxTokens:   maxTokens,
			Stream:      true,
			Tools:       tools,
			ToolChoice:  "auto",
		}

		// Per-turn timeout: cap each LLM call at convergeAt so a slow turn
		// cannot bleed into the convergence reserve. Without this, a turn
		// started just before convergeAt could run past the parent ctx
		// deadline (600s in production), producing "scan SSE: context
		// deadline exceeded" and starving forced convergence of its reserved
		// budget (task #7150).
		//
		// If the per-turn ctx expires but the parent ctx is still alive,
		// this is a normal exploration cutoff — NOT a transport error — so
		// exploreErr must stay clean and the loop falls through to forced
		// convergence naturally.
		callCtx := ctx
		var turnCancel context.CancelFunc
		if hasCutoff {
			turnTimeout := time.Until(convergeAt)
			minTurn := reserve / minPerTurnFraction
			if turnTimeout < minTurn {
				break // too little time left — don't start a new turn
			}
			callCtx, turnCancel = context.WithTimeout(ctx, turnTimeout)
		}

		msg, usage, err := chatTurn(callCtx, client, req, onToken)
		if turnCancel != nil {
			turnCancel()
		}
		addUsage(&totalUsage, usage)
		if err != nil {
			// User cancellation → abort immediately, no salvage.
			if ctx.Err() == context.Canceled {
				return "", totalUsage, err
			}
			// A transient send error mid-loop must not discard the accumulated
			// tool context (task #7077 MELCHIOR — previously any send error
			// returned instantly, throwing away useful exploration). Fall through
			// to forced convergence to salvage a final answer; remember the error
			// in case convergence also fails.
			//
			// The exploration deadline is the *expected* convergence trigger, not
			// a transport failure — never record it as exploreErr. Otherwise a
			// deadline that fires mid-turn followed by a failed convergence would
			// surface a bare "context deadline exceeded" instead of the far more
			// diagnostic "no substantive answer after convergence nudge" (task
			// #7081 review).
			//
			// With per-turn timeout (task #7150): a DeadlineExceeded from the
			// per-turn ctx is also an expected cutoff. Distinguish it from a
			// parent-ctx expiry by checking ctx.Err() — if the parent ctx is
			// still alive, the per-turn ctx expired and we break cleanly to
			// forced convergence without recording exploreErr.
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				// Per-turn ctx expired but parent ctx is alive — normal
				// exploration cutoff, not an error.
				break
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				exploreErr = err
			}
			break
		}

		// A nil message, or an answer that carries no substantive content (empty
		// or tool-call markup), falls through to forced convergence, which
		// nudges once for a real answer.
		if msg == nil {
			break
		}
		if len(msg.ToolCalls) == 0 {
			// Only a gate-passing answer ends exploration here. Tool-call markup
			// emitted as text (task #7077 CASPER) has non-empty Content yet no
			// substance — returning it here would let the RunProposers gate
			// reject it with no chance to recover, so break to a nudged forced
			// convergence instead.
			if msg.Content != "" && CheckAnswerContent(msg.Content) == nil {
				return msg.Content, totalUsage, nil
			}
			break
		}

		// Tool calls present — execute each one and feed the results back.
		messages = append(messages, *msg)
		for idx, tc := range msg.ToolCalls {
			if tc.ID == "" {
				tc.ID = fmt.Sprintf("call_%d_%d", turn, idx)
			}
			// Refuse identical repeats: the same (tool, args) call re-executed
			// keeps growing the context without new information (task #7178).
			sig := tc.Func.Name + "\x00" + tc.Func.Args
			toolCallCount[sig]++
			if toolCallCount[sig] > maxIdenticalToolCalls {
				messages = append(messages, embedded.ChatMessage{
					Role: embedded.ChatRoleTool,
					Content: fmt.Sprintf("Error: identical call to %s repeated %d times. Do not repeat this exact call — take a different approach (different arguments or a different tool) or produce your final answer now.",
						tc.Func.Name, toolCallCount[sig]),
					ToolCallID: tc.ID,
				})
				continue
			}
			messages = append(messages, executeProposerToolCall(ctx, tc, toolRegistry, dispatch, sheepName, ctxTokens))
		}

		// ── In-place context handoff (task #7164) ──
		//
		// The message history grows with every tool call. On a local LLM with
		// a finite context window, an oversized prompt causes prompt evaluation
		// to take minutes — the per-turn timeout fires, producing "scan SSE:
		// context deadline exceeded" and starving forced convergence.
		//
		// To prevent this, estimate the token count after each tool round. When
		// it exceeds the soft threshold (65%), ask the model to summarize its
		// findings so far, then replace the bloated history with [system, user,
		// summary]. This is an *in-place* refresh — no follow-up task is queued
		// (unlike the embedded loop's attemptHandoff) because MAGI must produce
		// a single, self-contained verdict from one proposer invocation.
		//
		// At most one refresh is allowed (maxInPlaceHandoffs). If the context
		// exceeds the hard threshold (85%) even after a refresh (or when no
		// refresh budget remains), exploration stops immediately and forced
		// convergence takes over with whatever context exists.
		estTokens := estimateProposerTokens(messages, tools)
		if estTokens >= ctxTokens*ctxHandoffHard/100 {
			// Hard limit — stop exploring regardless of handoff budget.
			break
		}
		if estTokens >= ctxTokens*ctxHandoffSoft/100 && handoffCount < maxInPlaceHandoffs {
			refreshTimeout := 30 * time.Second
			if hasCutoff {
				if remaining := time.Until(convergeAt); remaining < refreshTimeout {
					refreshTimeout = remaining
				}
			}
			refreshed, refreshUsage, err := inPlaceContextRefresh(ctx, client, ep, messages, temperature, maxTokens, onToken, refreshTimeout)
			addUsage(&totalUsage, refreshUsage)
			if err != nil {
				// Refresh failed (timeout, transport error) — don't discard
				// the existing context. Break to forced convergence instead.
				break
			}
			messages = refreshed
			handoffCount++
			if onToken != nil {
				onToken("\n[컨텍스트 핸드오프] 조사 내용을 요약하여 컨텍스트를 갱신했습니다.\n")
			}
		}
	}

	// ── Forced convergence: drop tools, demand a final answer ──
	//
	// Run it on an independent budget detached from the exploration deadline
	// (task #7077 BALTHASAR). Sharing ctx was the defect: an exploration turn
	// that ran up to — or past — the deadline left the forced request no time to
	// produce an answer, so the reserve meant to *save* convergence was instead
	// consumed by exploration and convergence died with "context deadline
	// exceeded". WithoutCancel guarantees a fresh `reserve` budget regardless of
	// how exploration ended (deadline hit, transient error, or clean cutoff).
	//
	// Trade-off: WithoutCancel also detaches parent *cancellation*, so a user
	// stop issued after exploration ends is ignored for up to `reserve` while
	// convergence runs. This is deliberate — MAGI's completeness requirement
	// (always attempt a final answer from accumulated context) is favored over
	// instant cancellation at the tail of the budget (task #7081 review).
	// ── Convergence diet (task #7205) ──
	//
	// Percentage-based handoff thresholds miss the throughput problem: on a
	// large-window endpoint an oversized-but-under-65% history still cannot be
	// re-evaluated within the convergence reserve by a slow local model. When
	// the accumulated prompt exceeds an absolute size, summarize first and
	// converge on the compact history. Budget: a detached slice of the reserve
	// (same completeness-over-cancellation trade-off as the reserve itself);
	// on any failure fall through to convergence with the original messages.
	if hasCutoff {
		if est := estimateProposerTokens(messages, nil); est >= convergenceDietTokens {
			dietTimeout := reserve / 3
			if dietTimeout < 10*time.Second {
				dietTimeout = 10 * time.Second
			}
			if dietTimeout > 45*time.Second {
				dietTimeout = 45 * time.Second
			}
			refreshed, dietUsage, dietErr := inPlaceContextRefresh(
				context.WithoutCancel(ctx), client, ep, messages, temperature, maxTokens, onToken, dietTimeout)
			addUsage(&totalUsage, dietUsage)
			if dietErr == nil {
				messages = refreshed
				if onToken != nil {
					onToken(fmt.Sprintf("\n[수렴 전 컨텍스트 다이어트 — %d → %d tokens 추정]\n",
						est, estimateProposerTokens(messages, nil)))
				}
			}
		}
	}

	// Estimate the prompt size once, just before convergence — reused by the
	// convergence-entry telemetry line and the stage-tagged timeout error.
	estAtConvergence := estimateProposerTokens(messages, nil)
	if onToken != nil && hasCutoff {
		onToken(fmt.Sprintf("\n[수렴 단계 진입 — 탐색 %d턴, 추정 컨텍스트 %d tokens, reserve %s]\n",
			turn, estAtConvergence, reserve.Round(time.Second)))
	}

	fcCtx := ctx
	if hasCutoff {
		var fcCancel context.CancelFunc
		fcCtx, fcCancel = context.WithTimeout(context.WithoutCancel(ctx), reserve)
		defer fcCancel()
	}

	content, u, err := forceFinalAnswer(fcCtx, client, ep, messages, temperature, maxTokens, onToken, true)
	addUsage(&totalUsage, u)
	if err != nil && exploreErr != nil {
		return "", totalUsage, fmt.Errorf("exploration failed (convergence could not salvage): %w", exploreErr)
	}
	// A convergence deadline means the reserve was too small for this prompt
	// size on this endpoint — tag the stage and the size so the failure line
	// is diagnostic without log archaeology (task #7205).
	if err != nil && hasCutoff && errors.Is(err, context.DeadlineExceeded) {
		return "", totalUsage, fmt.Errorf("convergence stage timed out (reserve %s, est prompt %d tokens): %w",
			reserve.Round(time.Second), estAtConvergence, err)
	}
	return content, totalUsage, err
}

// inPlaceContextRefresh asks the model to summarize its investigation so far,
// then returns a fresh message slice [system, user, summary] that replaces the
// bloated history. This prevents the local LLM from choking on an oversized
// prompt — which manifests as "scan SSE: context deadline exceeded" when prompt
// evaluation takes longer than the per-turn timeout (task #7164).
//
// The caller supplies the timeout budget so both exploration-phase handoffs
// and convergence-diet summarizations can set their own limits without the
// function needing to know about convergeAt. Tools are dropped from the
// request so the model cannot start a new investigation instead of summarizing.
//
// On any error, the caller should fall through to forced convergence with the
// original (un-refreshed) messages.
func inPlaceContextRefresh(
	ctx context.Context,
	client *embedded.Client,
	ep EndpointRef,
	messages []embedded.ChatMessage,
	temperature float32,
	maxTokens int,
	onToken func(string),
	timeout time.Duration,
) ([]embedded.ChatMessage, embedded.ChatUsage, error) {
	// Build a summary request: original messages + a directive to summarize.
	// We copy the messages slice explicitly to avoid aliasing the underlying
	// array that the caller (callEndpoint) still uses during exploration.
	summaryMsgs := make([]embedded.ChatMessage, len(messages), len(messages)+1)
	copy(summaryMsgs, messages)
	summaryMsgs = append(summaryMsgs, embedded.ChatMessage{Role: embedded.ChatRoleUser, Content: handoffSummaryDirective})

	summaryReq := &embedded.ChatRequest{
		Model:       ep.Model,
		Messages:    summaryMsgs,
		Temperature: 0.3, // low temperature for faithful summarization
		MaxTokens:   maxTokens,
		Stream:      true,
		// No Tools, no ToolChoice — text only.
	}

	// Use a short timeout so the summary doesn't eat the convergence reserve.
	if timeout < 5*time.Second {
		return nil, embedded.ChatUsage{}, fmt.Errorf("insufficient time for context refresh")
	}

	sumCtx, sumCancel := context.WithTimeout(ctx, timeout)
	defer sumCancel()

	msg, usage, err := chatTurn(sumCtx, client, summaryReq, onToken)
	if err != nil {
		return nil, usage, fmt.Errorf("context refresh summary: %w", err)
	}
	if msg == nil || strings.TrimSpace(msg.Content) == "" {
		return nil, usage, fmt.Errorf("context refresh produced empty summary")
	}

	// Build the refreshed message list: system + user + summary.
	// The system prompt (messages[0]) and original user prompt (messages[1])
	// are preserved verbatim. Everything else is replaced by the summary.
	refreshed := []embedded.ChatMessage{
		messages[0], // system
		messages[1], // original user prompt
		{
			Role:    embedded.ChatRoleAssistant,
			Content: "## 지금까지의 조사 요약\n" + strings.TrimSpace(msg.Content),
		},
	}

	return refreshed, usage, nil
}

// reaskBudget converts a slot's effective timeout into the wall-clock budget
// for one tools-off reask request (see reaskBudgetFraction).
func reaskBudget(effTimeout time.Duration) time.Duration {
	b := effTimeout / reaskBudgetFraction
	if b < reaskBudgetMin {
		b = reaskBudgetMin
	}
	if b > reaskBudgetMax {
		b = reaskBudgetMax
	}
	return b
}

// convergenceCutoff returns the instant at which a proposer must stop tool
// exploration and force a final answer, plus the reserve — the tail of the
// remaining budget set aside to fund that forced request (used as an
// independent, detached timeout by callEndpoint). ok is false when ctx has no
// deadline, in which case exploration is unbounded (tests only — production
// always sets a per-proposer timeout).
func convergenceCutoff(ctx context.Context) (convergeAt time.Time, reserve time.Duration, ok bool) {
	dl, has := ctx.Deadline()
	if !has {
		return time.Time{}, 0, false
	}
	remaining := time.Until(dl)
	if remaining <= 0 {
		return dl, minConvergenceReserve, true // already past — converge immediately
	}

	// Reserve is proportional to the remaining budget: 25% of remaining,
	// but bounded by a reasonable floor and ceiling so that:
	// - Short timeouts (e.g. 30s) still leave meaningful exploration time
	// - Long timeouts don't waste too much on convergence
	// The floor scales with remaining: for very short budgets (≤40s) the
	// floor is lower (10s) so exploration isn't starved (task #7165 review).
	reserve = remaining / 4

	minReserve := minConvergenceReserve
	if remaining <= 40*time.Second {
		minReserve = 10 * time.Second
	}
	if reserve < minReserve {
		reserve = minReserve
	}
	if reserve > remaining/2 {
		reserve = remaining / 2 // never spend more than half on convergence
	}
	return dl.Add(-reserve), reserve, true
}

// forceFinalAnswer performs the terminal, tools-off request(s) that produce a
// proposer's final answer. When appendDirective is set, a convergence
// instruction is added first (used when tool exploration was cut short). An
// empty answer triggers exactly one nudge before the proposer is declared
// failed, so a single empty response never collapses the deliberation.
func forceFinalAnswer(
	ctx context.Context,
	client *embedded.Client,
	ep EndpointRef,
	messages []embedded.ChatMessage,
	temperature float32,
	maxTokens int,
	onToken func(string),
	appendDirective bool,
) (string, embedded.ChatUsage, error) {
	msgs := messages
	if appendDirective {
		msgs = append(msgs, embedded.ChatMessage{
			Role:    embedded.ChatRoleUser,
			Content: convergenceDirective,
		})
	}

	var usage embedded.ChatUsage
	var lastGateErr error

	// Two attempts: the initial request plus one nudge on an unusable answer.
	for attempt := 0; attempt < 2; attempt++ {
		req := &embedded.ChatRequest{
			Model:       ep.Model,
			Messages:    msgs,
			Temperature: temperature,
			MaxTokens:   maxTokens,
			Stream:      true,
			// No tools — this request must yield a text answer.
		}

		// Salvage buffer (task #7205): copy streamed content deltas so a
		// convergence timeout can adopt the partial prose instead of discarding
		// everything. onToken only ever receives content deltas (never
		// reasoning_content), so the buffer is clean answer text.
		var salvage strings.Builder
		wrapped := func(s string) {
			salvage.WriteString(s)
			if onToken != nil {
				onToken(s)
			}
		}

		msg, u, err := chatTurn(ctx, client, req, wrapped)
		addUsage(&usage, u)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				partial := strings.TrimSpace(salvage.String())
				if CheckAnswerContent(partial) == nil {
					if onToken != nil {
						onToken("\n[수렴 타임아웃 — 스트리밍된 부분 응답을 채택합니다]\n")
					}
					return partial + "\n\n" + salvageMarker, usage, nil
				}
			}
			return "", usage, err
		}

		// Success = a gate-passing answer, not merely non-empty content. A
		// tool-call-markup response has non-empty Content yet no substance, so a
		// bare Content != "" check let it slip past unnudged and then fail the
		// RunProposers gate (task #7077 CASPER). Align the nudge trigger with the
		// gate so an unusable answer earns its one retry.
		var content string
		if msg != nil {
			content = msg.Content
		}
		if gateErr := CheckAnswerContent(content); gateErr == nil {
			return content, usage, nil
		} else {
			lastGateErr = gateErr
		}

		// Unusable answer — nudge once more.
		if msg != nil {
			msgs = append(msgs, *msg)
		}
		msgs = append(msgs, embedded.ChatMessage{
			Role:    embedded.ChatRoleUser,
			Content: finalAnswerNudge,
		})
	}

	return "", usage, fmt.Errorf("%s: no substantive answer after convergence nudge: %w", ep.ID, lastGateErr)
}

// reaskProposer sends one tools-off follow-up request to a proposer: the
// original task (capped), the proposer's own previous answer (capped), and a
// directive (the confidence nudge here; step-10's abstain second chance). It
// is a package variable so tests can fake it.
//
// The request runs on a budget detached from the caller's deadline
// (WithoutCancel) — the slot budget is typically spent by the time a reask is
// needed. Same completeness-over-cancellation trade-off as the convergence
// reserve; the budget caps the delay. A ctx already canceled/expired aborts
// immediately, so a user stop issued before the reask starts is honored.
//
// Callers must never discard the previous answer on reask failure (#7000
// conservative-gate principle): adopt the result only when it passes
// CheckAnswerContent and reports a confidence.
var reaskProposer = func(
	ctx context.Context,
	spec ProposerSpec,
	systemPrompt string,
	taskPrompt string,
	prevAnswer string,
	directive string,
	budget time.Duration,
	projectPath, sheepName string,
	onToken func(string),
) (string, embedded.ChatUsage, error) {
	if err := ctx.Err(); err != nil {
		return "", embedded.ChatUsage{}, err
	}

	var b strings.Builder
	b.WriteString("[원 태스크]\n")
	b.WriteString(capText(taskPrompt, 4000))
	b.WriteString("\n\n너의 이전 답변:\n")
	b.WriteString(capText(prevAnswer, 12000))
	b.WriteString("\n\n")
	b.WriteString(directive)
	userPrompt := b.String()

	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()

	provider := spec.Provider
	if provider == "" {
		provider = ProviderEmbedded
	}
	switch provider {
	case ProviderClaudeCLI:
		return callClaudeCLI(rctx, spec, systemPrompt, userPrompt, projectPath, sheepName, onToken)
	case ProviderOpenCodeCLI:
		return callOpenCodeCLI(rctx, spec, systemPrompt, userPrompt, projectPath, sheepName, onToken)
	case ProviderGrokCLI:
		return callGrokCLI(rctx, spec, systemPrompt, userPrompt, projectPath, sheepName, onToken)
	}

	// Embedded: one tools-off chat turn — no mini agent loop, no nudge.
	client := embedded.NewClient(spec.Endpoint.BaseURL, spec.Endpoint.APIKey, spec.Endpoint.Model)
	ctxTokens := spec.Endpoint.ContextTokens
	if ctxTokens == 0 {
		ctxTokens = embedded.DefaultContextTokens
	}
	req := &embedded.ChatRequest{
		Model: spec.Endpoint.Model,
		Messages: []embedded.ChatMessage{
			{Role: embedded.ChatRoleSystem, Content: systemPrompt},
			{Role: embedded.ChatRoleUser, Content: userPrompt},
		},
		Temperature: 0.3, // restatement, not fresh exploration
		MaxTokens:   ctxTokens / 4,
		Stream:      true,
		// No tools — the reask must yield a text answer.
	}
	msg, u, err := chatTurn(rctx, client, req, onToken)
	if err != nil {
		return "", u, err
	}
	if msg == nil {
		return "", u, fmt.Errorf("reask returned no message")
	}
	return msg.Content, u, nil
}

// executeProposerToolCall validates and runs a single tool call from a
// proposer, returning the tool-role result message. Only tools permitted for
// proposers are run (read/query tools plus the isolated browser tool set);
// tools that mutate shared filesystem / cluster / task state are rejected with
// an error fed back to the model (design §Phase 1.5, see IsAllowedProposerTool).
func executeProposerToolCall(
	ctx context.Context,
	tc embedded.ToolCall,
	toolRegistry *embedded.ToolRegistry,
	dispatch embedded.MCPDispatcher,
	sheepName string,
	ctxTokens int,
) embedded.ChatMessage {
	toolName := tc.Func.Name

	// Validate: only tools permitted for proposers are allowed.
	if !IsAllowedProposerTool(toolName) {
		return embedded.ChatMessage{
			Role:       embedded.ChatRoleTool,
			Content:    fmt.Sprintf("Error: tool %q is not allowed in MAGI deliberation. Tools that mutate shared filesystem, cluster, or task state are prohibited; use query/read tools or browser tools.", toolName),
			ToolCallID: tc.ID,
		}
	}

	// Parse arguments.
	var args map[string]interface{}
	if tc.Func.Args != "" {
		if err := json.Unmarshal([]byte(tc.Func.Args), &args); err != nil {
			return embedded.ChatMessage{
				Role:       embedded.ChatRoleTool,
				Content:    fmt.Sprintf("JSON parse error for %s: %v", toolName, err),
				ToolCallID: tc.ID,
			}
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	// Inject sheep_name for MCP tools that need it.
	if toolRegistry != nil && toolRegistry.WantsSheepName(toolName) {
		args["sheep_name"] = sheepName
	}

	// Execute the tool.
	var resultStr string
	var execErr error
	switch {
	case toolRegistry != nil:
		resultStr, execErr = toolRegistry.Dispatch(ctx, toolName, args)
	case dispatch != nil:
		resultStr, _, execErr = dispatch(toolName, args)
	default:
		execErr = fmt.Errorf("no tool dispatcher available for %s", toolName)
	}
	if execErr != nil {
		resultStr = fmt.Sprintf("Error: %v", execErr)
	}

	return embedded.ChatMessage{
		Role:       embedded.ChatRoleTool,
		Content:    truncateToolResult(resultStr, ctxTokens),
		ToolCallID: tc.ID,
	}
}

// addUsage accumulates token usage in place.
func addUsage(dst *embedded.ChatUsage, u embedded.ChatUsage) {
	dst.PromptTokens += u.PromptTokens
	dst.CompletionTokens += u.CompletionTokens
	dst.TotalTokens += u.TotalTokens
}

// estimateTextTokens estimates tokens for a text string. ASCII averages ~4
// bytes per token, but CJK (한글 등) averages ~1 token per character. This is
// a local copy of embedded.estimateTextTokens because that function is unexported.
func estimateTextTokens(s string) int {
	ascii := 0
	tokens := 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			tokens++
		}
	}
	return tokens + ascii/4
}

// estimateProposerTokens estimates the token count for a slice of messages,
// including the overhead of the tools definition array sent with every
// request. ASCII averages ~4 bytes/token; CJK (한글 등) averages ~1 token/char.
func estimateProposerTokens(messages []embedded.ChatMessage, tools []embedded.OpenAIToolDef) int {
	total := 0
	for _, msg := range messages {
		total += estimateTextTokens(msg.Content)
		for _, p := range msg.ContentParts {
			total += estimateTextTokens(p.Text)
		}
		for _, tc := range msg.ToolCalls {
			total += estimateTextTokens(tc.Func.Name) + estimateTextTokens(tc.Func.Args) + 16
		}
		total += 50 // per-message overhead
	}
	// Tools definition overhead: each tool has a name, description, and
	// parameters schema — typically 200-500 tokens per tool.
	for _, td := range tools {
		total += estimateTextTokens(td.Function.Name) + estimateTextTokens(td.Function.Description) + 100
	}
	return total
}

// truncateToolResult caps a tool result to a reasonable size for the chat
// context. Large outputs (e.g. reading a big file) would blow up the context
// window and degrade the model's deliberation. The cap is proportional to the
// endpoint's context window: 10% of ctxTokens, with a floor of 2000 runes.
func truncateToolResult(s string, ctxTokens int) string {
	maxRunes := ctxTokens / 10
	if maxRunes < 2000 {
		maxRunes = 2000
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "\n... [truncated]"
}

// RunProposersOptions bundles inputs for one blind parallel round.
type RunProposersOptions struct {
	Proposers       []ProposerSpec
	BaseSystem      string                      // base system prompt from the wiring layer
	UserPrompts     []string                    // per-slot user prompt (round 1: all identical; debate round: per-slot)
	Timeout         time.Duration               // per-proposer timeout (design: default 120s, set by caller)
	Temperature     float32                     // 0 → default 0.7 (diversity)
	OnOutput        func(string)                // live output sink, may be nil
	OnProposerToken func(slot int, text string) // live token stream, may be nil

	// Phase 1.5: read-only tools shared by all proposers.
	ToolDefs     []embedded.OpenAIToolDef
	ToolDispatch embedded.MCPDispatcher
	ProjectPath  string
	SheepName    string
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

			slotStart := time.Now()
			result := ProposerResult{Spec: sp}

			// Per-proposer timeout (design §5.1) with per-slot override: a slow
			// local model can get a longer budget than the global default
			// without extending every slot (task #7205).
			effTimeout := opts.Timeout
			if sp.Timeout > 0 {
				effTimeout = sp.Timeout
			}
			callCtx, cancel := context.WithTimeout(ctx, effTimeout)
			defer cancel()

			// Build the persona-augmented system prompt.
			systemPrompt := BuildProposerSystemPrompt(opts.BaseSystem, sp, slot)

			// Select the user prompt for this slot.
			userPrompt := ""
			if slot < len(opts.UserPrompts) {
				userPrompt = opts.UserPrompts[slot]
			}

			// Dispatch to the appropriate backend based on Provider.
			provider := sp.Provider
			if provider == "" {
				provider = ProviderEmbedded
			}

			var content string
			var usage embedded.ChatUsage
			var err error

			tokenCb := func(token string) {
				if opts.OnProposerToken != nil {
					opts.OnProposerToken(slot, token)
				}
			}

			// Generate a per-proposer sheep name so each MAGI proposer gets
			// its own isolated browser session profile. Without this, three
			// concurrent models share one Chrome instance and their DOM
			// manipulations collide (task #7139). This applies to every
			// provider — the embedded loop injects it directly into browser
			// tool args, while CLI providers receive it as a prompt directive
			// (they run external agent loops we cannot intercept).
			perSlotSheepName := PersonaSheepName(opts.SheepName, sp, slot)

			switch provider {
			case ProviderClaudeCLI:
				content, usage, err = callClaudeCLI(callCtx, sp, systemPrompt, userPrompt, opts.ProjectPath, perSlotSheepName, tokenCb)
			case ProviderOpenCodeCLI:
				content, usage, err = callOpenCodeCLI(callCtx, sp, systemPrompt, userPrompt, opts.ProjectPath, perSlotSheepName, tokenCb)
			case ProviderGrokCLI:
				content, usage, err = callGrokCLI(callCtx, sp, systemPrompt, userPrompt, opts.ProjectPath, perSlotSheepName, tokenCb)
			default: // ProviderEmbedded
				// Compute max tokens: ContextTokens / 4 (same rule as the
				// embedded agent loop). Fall back to DefaultContextTokens.
				ctxTokens := sp.Endpoint.ContextTokens
				if ctxTokens == 0 {
					ctxTokens = embedded.DefaultContextTokens
				}
				maxTokens := ctxTokens / 4

				content, usage, err = callEndpoint(callCtx, sp.Endpoint, systemPrompt, userPrompt, temp, maxTokens, tokenCb,
					opts.ToolDefs, opts.ToolDispatch, opts.ProjectPath, perSlotSheepName)
			}

			if err != nil {
				result.Err = err
				result.Usage = usage // failed proposers still consumed tokens (task #7205)
				emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, false, 0, time.Since(slotStart), err))
				results[slot] = result
				return
			}

			// Separate the self-reported confidence from the answer body.
			cleaned, conf := ExtractConfidence(content)

			// Content gate: leaked tool-call text or empty prose is a failure,
			// not an answer — record it like a transport error so the wiring
			// fallback can engage (lesson from task #7031).
			if gateErr := CheckAnswerContent(cleaned); gateErr != nil {
				result.Err = gateErr
				result.Usage = usage // failed proposers still consumed tokens (task #7205)
				emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, false, 0, time.Since(slotStart), gateErr))
				results[slot] = result
				return
			}

			result.Answer = cleaned
			result.Confidence = conf
			result.Usage = usage

			// Confidence nudge (task #7205): an answer without a CONFIDENCE
			// self-report weakens the judge's weighting, so ask once for a
			// restatement. Salvaged partial answers are exempt — that endpoint
			// just proved too slow for this slot's budget, and the salvage
			// marker already tells the judge the answer was cut off. The
			// previous answer is never discarded: the reask result is adopted
			// only when it passes the gate and reports a confidence.
			if conf < 0 && !strings.Contains(cleaned, salvageMarker) && ctx.Err() == nil {
				budget := reaskBudget(effTimeout)
				tokenCb(fmt.Sprintf("\n[신뢰도 재질문 — CONFIDENCE 미보고, 예산 %d초]\n", int(budget.Seconds())))
				reasked, ru, rerr := reaskProposer(ctx, sp, systemPrompt, userPrompt, cleaned,
					confidenceReaskDirective, budget, opts.ProjectPath, perSlotSheepName, tokenCb)
				addUsage(&result.Usage, ru)
				if rerr == nil {
					recleaned, reconf := ExtractConfidence(reasked)
					if reconf >= 0 && CheckAnswerContent(recleaned) == nil {
						result.Answer = recleaned
						result.Confidence = reconf
					}
				}
			}

			emitOutput(&mu, opts.OnOutput, formatProposerLine(sp, slot, true, result.Confidence, time.Since(slotStart), nil))
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
//
//	success: "[MAGI:0]   🔬 MELCHIOR-1 (qwen3-27b) 응답 완료 — 신뢰도 8/10 (74초)\n"
//	failure: "[MAGI:0]   🔬 MELCHIOR-1 (qwen3-27b) 응답 실패 — <err> (312초)\n"
//
// When confidence is -1 (not reported), shows "신뢰도 미보고".
// The [MAGI:N] prefix allows the frontend to route the line to the correct
// proposer panel (slot N = 0, 1, or 2).
func formatProposerLine(spec ProposerSpec, slot int, success bool, confidence int, elapsed time.Duration, err error) string {
	emoji := PersonaEmoji(spec)
	displayName := PersonaDisplayName(spec, slot)
	model := proposerModelLabel(spec)
	prefix := fmt.Sprintf("[MAGI:%d] ", slot)
	secs := int(elapsed.Seconds())

	if success {
		confStr := "신뢰도 미보고"
		if confidence >= 0 {
			confStr = fmt.Sprintf("신뢰도 %d/10", confidence)
		}
		return fmt.Sprintf("%s %s %s (%s) 응답 완료 — %s (%d초)\n", prefix, emoji, displayName, model, confStr, secs)
	}

	return fmt.Sprintf("%s %s %s (%s) 응답 실패 — %v (%d초)\n", prefix, emoji, displayName, model, err, secs)
}

// proposerModelLabel returns a display string for the model used by a proposer.
func proposerModelLabel(spec ProposerSpec) string {
	provider := spec.Provider
	if provider == "" {
		provider = ProviderEmbedded
	}
	switch provider {
	case ProviderClaudeCLI:
		if spec.ModelID != "" {
			return "claude:" + spec.ModelID
		}
		return "claude:default"
	case ProviderOpenCodeCLI:
		if spec.ModelID != "" {
			return "opencode:" + spec.ModelID
		}
		return "opencode:default"
	default:
		return spec.Endpoint.Model
	}
}

// ── CLI-based proposer backends ──────────────────────────────────────
//
// claude_cli and opencode_cli proposers run as subprocesses (like the
// aggregator's claude_cli path). The CLI subprocess owns its own tool loop,
// so shepherd cannot intercept individual tool calls to enforce the proposer
// permission set the embedded path applies via IsAllowedProposerTool. This is
// acceptable because a CLI proposer with its own agentic capabilities is a
// valid alternative. The one cross-cutting concern shepherd still injects is
// browser session isolation: a per-slot sheep_name directive is appended to
// the prompt (browserSessionDirective) so concurrent CLI proposers don't
// collide on one Chrome instance.
//
// Streaming: CLI stdout is line-buffered; each line is forwarded to the
// onToken callback so the frontend can render live output.

// browserSessionDirective returns a system-prompt addendum that pins a CLI
// proposer's browser tool calls to its own per-slot sheep session, preventing
// DOM conflicts when proposers run concurrently. Unlike the embedded path —
// which overrides sheep_name directly in the tool args — CLI providers run an
// external agent loop we cannot intercept, so isolation can only be requested
// via the prompt. Returns "" when sheepName is empty (no browser isolation).
func browserSessionDirective(sheepName string) string {
	if sheepName == "" {
		return ""
	}
	return fmt.Sprintf("\n\n[브라우저 세션 격리] 브라우저 도구(browser_*)를 사용할 경우 반드시 sheep_name=%q 를 지정하라. 다른 세션 이름을 쓰면 동시에 심의 중인 다른 모델과 브라우저 세션이 충돌한다.", sheepName)
}

// callClaudeCLI runs `claude --print` with an optional model flag. sheepName
// pins any browser tool calls to a per-proposer session (see
// browserSessionDirective); pass "" to skip browser isolation.
func callClaudeCLI(ctx context.Context, spec ProposerSpec, systemPrompt, userPrompt, workDir, sheepName string, onToken func(string)) (string, embedded.ChatUsage, error) {
	args := []string{"--print"}
	if spec.ModelID != "" {
		args = append(args, "--model", spec.ModelID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(systemPrompt + browserSessionDirective(sheepName) + "\n\n" + userPrompt)
	envutil.SetCleanEnv(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("claude pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("claude start: %w", err)
	}

	var buf bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line + "\n")
		if onToken != nil {
			onToken(line + "\n")
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("claude wait: %w", err)
	}

	output := strings.TrimSpace(buf.String())
	if output == "" {
		return "", embedded.ChatUsage{}, fmt.Errorf("claude returned empty output")
	}
	return output, embedded.ChatUsage{}, nil
}

// callOpenCodeCLI runs `opencode run --format json` with an optional model
// flag. sheepName pins any browser tool calls to a per-proposer session (see
// browserSessionDirective); pass "" to skip browser isolation.
func callOpenCodeCLI(ctx context.Context, spec ProposerSpec, systemPrompt, userPrompt, workDir, sheepName string, onToken func(string)) (string, embedded.ChatUsage, error) {
	args := []string{"run", "--format", "json"}
	if spec.ModelID != "" {
		args = append(args, "-m", spec.ModelID)
	}

	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(systemPrompt + browserSessionDirective(sheepName) + "\n\n" + userPrompt)
	envutil.SetCleanEnv(cmd)
	cmd.Env = append(cmd.Env, `OPENCODE_PERMISSION={"*":"allow"}`)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("opencode pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("opencode start: %w", err)
	}

	var buf bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line + "\n")

		// Parse OpenCode JSON events to extract text content for streaming.
		parsed := parseOpenCodeEvent(line)
		if parsed != "" && onToken != nil {
			onToken(parsed + "\n")
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("opencode wait: %w", err)
	}

	output := strings.TrimSpace(buf.String())
	if output == "" {
		return "", embedded.ChatUsage{}, fmt.Errorf("opencode returned empty output")
	}

	// Extract the final text from OpenCode JSON event stream.
	finalText := extractOpenCodeFinalText(output)
	if finalText == "" {
		finalText = output // fallback to raw output
	}
	return finalText, embedded.ChatUsage{}, nil
}

// callGrokCLI runs `grok -p <prompt> --output-format streaming-json` with an
// optional model flag. grok emits per-token deltas — {"type":"text","data":".."}
// for the answer, {"type":"thought",..} for reasoning — so the final answer is
// the concatenation of every "text" delta. sheepName pins any browser tool calls
// to a per-proposer session (see browserSessionDirective); pass "" to skip.
func callGrokCLI(ctx context.Context, spec ProposerSpec, systemPrompt, userPrompt, workDir, sheepName string, onToken func(string)) (string, embedded.ChatUsage, error) {
	args := []string{"--output-format", "streaming-json", "--always-approve"}
	if spec.ModelID != "" {
		args = append(args, "-m", spec.ModelID)
	}
	// The single-turn prompt goes last, via the -p flag.
	args = append(args, "-p", systemPrompt+browserSessionDirective(sheepName)+"\n\n"+userPrompt)

	cmd := exec.CommandContext(ctx, "grok", args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader("")
	envutil.SetCleanEnv(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("grok pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("grok start: %w", err)
	}

	var answer strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Grok's streaming-json emits per-token deltas — often a single character
	// per {"type":"text","data":".."} event. Passing each delta straight to
	// onToken causes the live output to fragment into hundreds of tiny
	// [MAGI:n] x [MAGI:n] y [MAGI:n] z lines (task #7086 output log shows this
	// clearly). Instead, we buffer text deltas and flush on newline or when a
	// reasonable chunk size accumulates, matching the line-granularity that
	// Claude CLI and OpenCode CLI already provide.
	var liveBuf strings.Builder
	flushLive := func() {
		if onToken != nil && liveBuf.Len() > 0 {
			onToken(liveBuf.String())
			liveBuf.Reset()
		}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Type == "text" && ev.Data != "" {
			answer.WriteString(ev.Data)
			if onToken != nil {
				liveBuf.WriteString(ev.Data)
				nlIdx := strings.IndexByte(liveBuf.String(), '\n')
				for nlIdx >= 0 {
					s := liveBuf.String()
					onToken(s[:nlIdx+1])
					s = s[nlIdx+1:]
					liveBuf.Reset()
					liveBuf.WriteString(s)
					nlIdx = strings.IndexByte(liveBuf.String(), '\n')
				}
				// Safety flush: if no newline has arrived for a while, emit
				// the accumulated chunk so the UI doesn't appear frozen during
				// long paragraphs without line breaks.
				if liveBuf.Len() >= 120 {
					flushLive()
				}
			}
		}
	}

	// Flush any remaining buffered text after the stream ends.
	flushLive()

	if err := cmd.Wait(); err != nil {
		return "", embedded.ChatUsage{}, fmt.Errorf("grok wait: %w", err)
	}

	output := strings.TrimSpace(answer.String())
	if output == "" {
		return "", embedded.ChatUsage{}, fmt.Errorf("grok returned empty output")
	}
	return output, embedded.ChatUsage{}, nil
}

// parseOpenCodeEvent extracts displayable text from a single OpenCode JSON line.
//
// OpenCode v1.16+ emits events in the AI SDK streaming format:
//
//	{"type":"text","part":{"type":"text","text":"Hello!"}}
//	{"type":"step_start","part":{"type":"step-start"}}
//	{"type":"step_finish","part":{"type":"step-finish","reason":"stop"}}
//
// The text content lives in part.text (not a top-level "content" field).
// Older versions used {"type":"message","content":"..."} — we keep that as
// a fallback for compatibility.
func parseOpenCodeEvent(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return ""
	}
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return ""
	}
	eventType, _ := event["type"].(string)

	// v1.16+ format: text lives in part.text.
	if eventType == "text" {
		if part, ok := event["part"].(map[string]interface{}); ok {
			if text, ok := part["text"].(string); ok {
				return text
			}
		}
	}

	// Legacy format fallback: top-level content field.
	switch eventType {
	case "message", "text", "assistant":
		if content, ok := event["content"].(string); ok {
			return content
		}
	}
	return ""
}

// extractOpenCodeFinalText extracts the final assistant message from a
// sequence of OpenCode JSON events.
//
// It concatenates all text parts from "text"-type events (v1.16+ format).
// For legacy format (top-level "content"), it takes the last non-empty value.
func extractOpenCodeFinalText(raw string) string {
	lines := strings.Split(raw, "\n")
	var parts []string
	var lastLegacyText string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		eventType, _ := event["type"].(string)

		// v1.16+ format: accumulate all text parts.
		if eventType == "text" {
			if part, ok := event["part"].(map[string]interface{}); ok {
				if text, ok := part["text"].(string); ok && text != "" {
					parts = append(parts, text)
				}
			}
		}

		// Legacy format fallback.
		if eventType == "message" || eventType == "assistant" {
			if content, ok := event["content"].(string); ok && content != "" {
				lastLegacyText = content
			}
		}
	}

	if len(parts) > 0 {
		return strings.Join(parts, "")
	}
	return lastLegacyText
}
