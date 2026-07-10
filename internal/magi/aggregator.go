package magi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"

	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/envutil"
)

// AggregatorSpec selects the judging backend (resolved by the wiring layer).
type AggregatorSpec struct {
	Type             string      // "claude_cli" | "opencode_cli" | "grok_cli" | "endpoint"
	Endpoint         EndpointRef // used when Type == "endpoint"
	FallbackEndpoint EndpointRef // used when a CLI backend fails (design §7)
	WorkDir          string      // project path for the CLI subprocess
	ModelID          string      // model alias for claude_cli/opencode_cli/grok_cli (optional)
}

// aggregatorOnOutput is the live-output sink used by aggregatorComplete
// for fallback warnings. Set by Judge before calling aggregatorComplete.
// Package-level var so the signature of aggregatorComplete stays clean
// (no onOutput parameter) while still allowing warnings to reach the stream.
var aggregatorOnOutput func(string)

// aggregatorComplete sends one prompt to the aggregator backend.
// Package-level var so tests can fake it.
var aggregatorComplete = func(ctx context.Context, spec AggregatorSpec, systemPrompt, userPrompt string) (string, embedded.ChatUsage, error) {
	switch spec.Type {
	case "endpoint":
		return aggregatorEndpoint(ctx, spec.Endpoint, systemPrompt, userPrompt)
	case "claude_cli":
		return aggregatorClaudeCLI(ctx, spec, systemPrompt, userPrompt, aggregatorOnOutput)
	case "opencode_cli":
		return aggregatorOpenCodeCLI(ctx, spec, systemPrompt, userPrompt, aggregatorOnOutput)
	case "grok_cli":
		return aggregatorGrokCLI(ctx, spec, systemPrompt, userPrompt, aggregatorOnOutput)
	default:
		return "", embedded.ChatUsage{}, fmt.Errorf("unknown aggregator type %q", spec.Type)
	}
}

// aggregatorEndpoint calls a local OpenAI-compatible endpoint at temperature 0.2
// for deterministic judging (design §5.3). The aggregator does not use tools —
// it only judges the proposers' answers.
func aggregatorEndpoint(ctx context.Context, ep EndpointRef, systemPrompt, userPrompt string) (string, embedded.ChatUsage, error) {
	ctxTokens := ep.ContextTokens
	if ctxTokens == 0 {
		ctxTokens = embedded.DefaultContextTokens
	}
	maxTokens := ctxTokens / 4
	return callEndpoint(ctx, ep, systemPrompt, userPrompt, 0.2, maxTokens, nil, nil, nil, "", "")
}

// aggregatorClaudeCLI invokes "claude --print" as a subprocess. On failure
// it emits a live-output warning and falls back to the FallbackEndpoint via
// the endpoint path (design §7: the first proposer endpoint doubles as
// aggregator fallback).
//
// The onOutput parameter is wired through aggregatorComplete's closure so
// the fallback warning reaches the live stream.
func aggregatorClaudeCLI(ctx context.Context, spec AggregatorSpec, systemPrompt, userPrompt string, onOutput func(string)) (string, embedded.ChatUsage, error) {
	args := []string{"--print"}
	if spec.ModelID != "" {
		args = append(args, "--model", spec.ModelID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = spec.WorkDir
	cmd.Stdin = strings.NewReader(systemPrompt + "\n\n" + userPrompt)
	envutil.SetCleanEnv(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if onOutput != nil {
			onOutput("⚠️ Claude aggregator 실패 — 로컬 폴백 사용\n")
		}
		return aggregatorEndpoint(ctx, spec.FallbackEndpoint, systemPrompt, userPrompt)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		if onOutput != nil {
			onOutput("⚠️ Claude aggregator 실패 — 로컬 폴백 사용\n")
		}
		return aggregatorEndpoint(ctx, spec.FallbackEndpoint, systemPrompt, userPrompt)
	}

	// Claude CLI does not report token usage — return zero value.
	return output, embedded.ChatUsage{}, nil
}

// aggregatorOpenCodeCLI invokes "opencode run --format json" as a subprocess.
// On failure it emits a live-output warning and falls back to the
// FallbackEndpoint via the endpoint path (design §7).
//
// The onOutput parameter is wired through aggregatorComplete's closure so
// the fallback warning reaches the live stream.
func aggregatorOpenCodeCLI(ctx context.Context, spec AggregatorSpec, systemPrompt, userPrompt string, onOutput func(string)) (string, embedded.ChatUsage, error) {
	args := []string{"run", "--format", "json"}
	if spec.ModelID != "" {
		args = append(args, "-m", spec.ModelID)
	}

	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = spec.WorkDir
	cmd.Stdin = strings.NewReader(systemPrompt + "\n\n" + userPrompt)
	envutil.SetCleanEnv(cmd)
	cmd.Env = append(cmd.Env, `OPENCODE_PERMISSION={"*":"allow"}`)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if onOutput != nil {
			msg := "⚠️ OpenCode aggregator 실패 — 로컬 폴백 사용"
			if stderr.Len() > 0 {
				msg += ": " + strings.TrimSpace(stderr.String())
			}
			onOutput(msg + "\n")
		}
		return aggregatorEndpoint(ctx, spec.FallbackEndpoint, systemPrompt, userPrompt)
	}

	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		if onOutput != nil {
			onOutput("⚠️ OpenCode aggregator 실패 — 로컬 폴백 사용\n")
		}
		return aggregatorEndpoint(ctx, spec.FallbackEndpoint, systemPrompt, userPrompt)
	}

	// Extract the final text from OpenCode JSON event stream.
	output := extractOpenCodeFinalText(raw)
	if output == "" {
		output = raw // fallback to raw output
	}

	// OpenCode CLI does not report token usage — return zero value.
	return output, embedded.ChatUsage{}, nil
}

// aggregatorGrokCLI invokes "grok -p ... --output-format streaming-json" as a
// subprocess. On failure it emits a live-output warning and falls back to the
// FallbackEndpoint via the endpoint path (design §7).
//
// The onOutput parameter is wired through aggregatorComplete's closure so the
// fallback warning reaches the live stream.
func aggregatorGrokCLI(ctx context.Context, spec AggregatorSpec, systemPrompt, userPrompt string, onOutput func(string)) (string, embedded.ChatUsage, error) {
	args := []string{"--output-format", "streaming-json", "--always-approve"}
	if spec.ModelID != "" {
		args = append(args, "-m", spec.ModelID)
	}
	args = append(args, "-p", systemPrompt+"\n\n"+userPrompt)

	cmd := exec.CommandContext(ctx, "grok", args...)
	cmd.Dir = spec.WorkDir
	cmd.Stdin = strings.NewReader("")
	envutil.SetCleanEnv(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if onOutput != nil {
			msg := "⚠️ Grok aggregator 실패 — 로컬 폴백 사용"
			if stderr.Len() > 0 {
				msg += ": " + strings.TrimSpace(stderr.String())
			}
			onOutput(msg + "\n")
		}
		return aggregatorEndpoint(ctx, spec.FallbackEndpoint, systemPrompt, userPrompt)
	}

	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		if onOutput != nil {
			onOutput("⚠️ Grok aggregator 실패 — 로컬 폴백 사용\n")
		}
		return aggregatorEndpoint(ctx, spec.FallbackEndpoint, systemPrompt, userPrompt)
	}

	// grok streams token deltas — the answer is the concatenation of "text" data.
	output := extractGrokFinalText(raw)
	if output == "" {
		output = raw // fallback to raw output
	}

	// grok CLI does not report token usage — return zero value.
	return output, embedded.ChatUsage{}, nil
}

// extractGrokFinalText reconstructs grok's answer from its streaming-json output
// by concatenating every {"type":"text","data":".."} delta.
func extractGrokFinalText(raw string) string {
	var b strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
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
		if ev.Type == "text" {
			b.WriteString(ev.Data)
		}
	}
	return strings.TrimSpace(b.String())
}

// judgeSystemPrompt is the system prompt for the aggregator. The full
// role declaration and bias suppression instructions are in the user prompt
// (BuildJudgePrompt) because they must be co-located with the answers for
// the model to apply them. The system prompt is a minimal identity cue.
const judgeSystemPrompt = `너는 MAGI 합의 시스템의 판정자다. 심의자들의 답변을 평가하고 종합하여 JSON 형식으로 판정을 내려라.`

// judgeJSONSchema is the JSON schema instruction embedded in the user prompt
// (design §5.3).
const judgeJSONSchema = `[출력 형식]
다음 JSON 스키마에 맞는 JSON 객체 하나만 출력하라. 다른 텍스트는 금지한다.

{
  "verdict": "unanimous | majority | split",
  "agreement_axis": "핵심 결론에서 무엇이 일치/불일치했는지 한 줄",
  "synthesis": "종합 답변 — 이것이 사용자에게 전달되는 최종 산출물이므로 완결된 답으로 작성하라",
  "dissent": "소수의견 요약 (없으면 빈 문자열)",
  "abstained": ["기권 처리한 심의자 이름", ...],
  "confidence": 0-10
}

필드 의미:
- verdict: 기권을 제외한 유효 답변 기준 — 핵심 결론이 모두 일치하면 "unanimous", 다수가 일치하면 "majority", 모두 다르거나 유효 답변이 1개 이하면 "split"
- synthesis: 종합 답변 (최종 산출물)
- dissent: 소수의견 요약 (없으면 빈 문자열)
- abstained: 기권 처리 규칙으로 집계에서 제외한 심의자의 이름 배열 — 답변 섹션 제목(### 뒤)의 이름을 그대로 사용하라. 기권이 없으면 빈 배열
- confidence: 종합 답변에 대한 확신 (0-10 정수)`

// BuildJudgePrompt renders the three answers in random order with persona
// names only (identity masking) and instructs a JSON-only verdict.
// Order randomization mitigates position bias (design §2.6).
func BuildJudgePrompt(results []ProposerResult, taskPrompt string) string {
	var b strings.Builder

	b.WriteString("너는 MAGI 합의 시스템의 판정자다. 아래 심의자들의 답변을 평가하고 종합하라.\n\n")

	// Bias suppression (design §2.6).
	b.WriteString("[편향 억제 지시]\n")
	b.WriteString("- 답변의 **길이가 아니라 근거의 질**로 평가하라.\n")
	b.WriteString("- 어느 모델이 썼는지는 알 수 없으며 추측하지 마라.\n")
	b.WriteString("- 답변 제시 순서에 영향받지 마라.\n\n")

	// Abstention rule (lesson from task #7031): content-free answers must not
	// count toward the majority, or a single substantive answer gets packaged
	// as consensus with high confidence.
	b.WriteString("[기권 처리 규칙]\n")
	b.WriteString("- 결론이 없는 답변(도구 호출 시도만 있음, 실질 내용 없음, 절차 안내만 있고 결론 없음)은 '기권'으로 취급하고 다수결 집계에서 제외하라.\n")
	b.WriteString("- 기권을 제외한 유효 답변이 1개 이하면 합의가 성립하지 않는다: verdict를 \"split\"으로 하고, dissent에 어떤 답변을 왜 기권 처리했는지 명시하라.\n")
	b.WriteString("- 기권 처리한 심의자의 이름을 출력 JSON의 \"abstained\" 배열에 기재하라 (섹션 제목의 이름 그대로).\n\n")

	b.WriteString("[원 태스크]\n")
	b.WriteString(capText(taskPrompt, 4000))
	b.WriteString("\n\n")

	b.WriteString("[심의자 답변들]\n\n")

	// Shuffle order to mitigate position bias (design §2.6).
	indices := make([]int, len(results))
	for i := range indices {
		indices[i] = i
	}
	rand.Shuffle(len(indices), func(i, j int) {
		indices[i], indices[j] = indices[j], indices[i]
	})

	for _, idx := range indices {
		r := results[idx]
		displayName := PersonaDisplayName(r.Spec, idx)
		confStr := "신뢰도 미보고"
		if r.Confidence >= 0 {
			confStr = fmt.Sprintf("신뢰도 %d/10", r.Confidence)
		}
		fmt.Fprintf(&b, "### %s (%s)\n%s\n\n", displayName, confStr, capText(r.Answer, 12000))
	}

	b.WriteString(judgeJSONSchema)
	b.WriteString("\n\nJSON 객체 하나만 출력하라. 다른 텍스트 금지.")

	return b.String()
}

// Judge runs the aggregator once, re-prompting once on JSON failure.
// Returns (nil, usage, nil) when both attempts fail to parse — the caller
// falls back to side-by-side output (design §5.3: never mark incomplete;
// an answer exists, so gate conservatively — lesson from task #7000).
//
// The returned int is the actual call count (for cost reporting).
func Judge(ctx context.Context, spec AggregatorSpec, results []ProposerResult, taskPrompt string, onOutput func(string)) (*Verdict, embedded.ChatUsage, int, error) {
	var totalUsage embedded.ChatUsage
	calls := 0

	// Wire the live-output sink so aggregatorComplete can emit fallback
	// warnings (design §7: claude CLI failure → endpoint fallback).
	aggregatorOnOutput = onOutput
	defer func() { aggregatorOnOutput = nil }()

	userPrompt := BuildJudgePrompt(results, taskPrompt)

	// First attempt.
	output, usage, err := aggregatorComplete(ctx, spec, judgeSystemPrompt, userPrompt)
	calls++
	totalUsage.PromptTokens += usage.PromptTokens
	totalUsage.CompletionTokens += usage.CompletionTokens
	totalUsage.TotalTokens += usage.TotalTokens

	if err != nil {
		// Backend itself failed (including fallback exhaustion).
		return nil, totalUsage, calls, fmt.Errorf("aggregator backend failed: %w", err)
	}

	verdict, parseErr := ParseVerdict(output)
	if parseErr == nil {
		return verdict, totalUsage, calls, nil
	}

	// Re-prompt once (design §5.3).
	if onOutput != nil {
		onOutput("⚠️ 판정 JSON 파싱 실패 — 재시도 중...\n")
	}

	reprompt := output + "\n\n출력이 유효한 JSON이 아니다. 스키마에 맞는 JSON 객체 하나만 다시 출력하라."

	output2, usage2, err := aggregatorComplete(ctx, spec, judgeSystemPrompt, reprompt)
	calls++
	totalUsage.PromptTokens += usage2.PromptTokens
	totalUsage.CompletionTokens += usage2.CompletionTokens
	totalUsage.TotalTokens += usage2.TotalTokens

	if err != nil {
		return nil, totalUsage, calls, fmt.Errorf("aggregator backend failed on retry: %w", err)
	}

	verdict2, parseErr2 := ParseVerdict(output2)
	if parseErr2 == nil {
		return verdict2, totalUsage, calls, nil
	}

	// Both attempts failed to parse — return nil verdict without error.
	// The caller uses SideBySideFallback (design §5.3).
	return nil, totalUsage, calls, nil
}

// SideBySideFallback renders all answers with persona headers when the
// aggregator verdict could not be obtained (design §5.3).
func SideBySideFallback(results []ProposerResult) string {
	var b strings.Builder

	b.WriteString("⚠️ MAGI 판정 실패 — 세 심의자의 답변을 원문 병기합니다.\n\n")

	for i, r := range results {
		if r.Err != nil {
			continue
		}
		displayName := PersonaDisplayName(r.Spec, i)
		fmt.Fprintf(&b, "--- %s ---\n%s\n\n", displayName, r.Answer)
	}

	return b.String()
}
