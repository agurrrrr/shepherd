package magi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
)

// longProse builds a Korean prose block comfortably above minSubstantiveRunes.
func longProse() string {
	return strings.Repeat("이 문제의 원인은 텍스트 분할 로직이 단락 기준에서 라인 기준으로 바뀐 것으로 보인다. ", 5)
}

func TestCheckAnswerContent_PlainProse(t *testing.T) {
	if err := CheckAnswerContent("원인은 분할 로직 변경이다. 단락 기준으로 복원하라."); err != nil {
		t.Errorf("plain prose should pass, got %v", err)
	}
}

func TestCheckAnswerContent_ShortProseWithoutMarker(t *testing.T) {
	// Short answers with no tool-call markup must pass — length is only
	// judged when markup is present.
	if err := CheckAnswerContent("답: 42"); err != nil {
		t.Errorf("short marker-free prose should pass, got %v", err)
	}
}

func TestCheckAnswerContent_Empty(t *testing.T) {
	for _, s := range []string{"", "   \n\t  "} {
		if err := CheckAnswerContent(s); err == nil {
			t.Errorf("empty answer %q should fail", s)
		}
	}
}

func TestCheckAnswerContent_ToolCallOnly(t *testing.T) {
	cases := []string{
		// qwen-style paired block.
		"<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"main.go\"}}\n</tool_call>",
		// Brief preamble + tool call — still not an answer.
		"먼저 코드를 확인하겠습니다.\n<tool_call>\n{\"name\": \"run_command\", \"arguments\": {\"cmd\": \"git log\"}}\n</tool_call>",
		// Unclosed block (stream cut mid-call).
		"<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"" + strings.Repeat("a/", 200) + "\"}}",
		// Alternate marker families.
		"<function_call>{\"name\": \"grep\", \"arguments\": {}}</function_call>",
		"[TOOL_CALLS] read_file {\"path\": \"src/split.go\"}",
	}
	for _, s := range cases {
		if err := CheckAnswerContent(s); err == nil {
			t.Errorf("tool-call answer should fail: %q", s)
		}
	}
}

func TestCheckAnswerContent_BareToolCallJSON(t *testing.T) {
	cases := []string{
		`{"name": "read_file", "arguments": {"path": "main.go"}}`,
		`{"name": "read_file", "parameters": {"path": "main.go"}}`,
		"```json\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"main.go\"}}\n```",
	}
	for _, s := range cases {
		if err := CheckAnswerContent(s); err == nil {
			t.Errorf("bare tool-call JSON should fail: %q", s)
		}
	}
}

func TestCheckAnswerContent_JSONAnswerNotToolCall(t *testing.T) {
	// A JSON object that is a legitimate answer payload must pass.
	if err := CheckAnswerContent(`{"cause": "line-based split", "fix": "restore paragraph split"}`); err != nil {
		t.Errorf("non-tool-call JSON should pass, got %v", err)
	}
}

func TestCheckAnswerContent_ProseAroundToolCall(t *testing.T) {
	// Substantive analysis that happens to include a tool-call block passes.
	answer := longProse() + "\n<tool_call>\n{\"name\": \"read_file\", \"arguments\": {}}\n</tool_call>\n" + longProse()
	if err := CheckAnswerContent(answer); err != nil {
		t.Errorf("prose around tool call should pass, got %v", err)
	}
}

// TestRunProposers_ContentGate verifies that a tool-call-only response is
// recorded as a failure so SuccessfulResults excludes it (lesson from #7031).
func TestRunProposers_ContentGate(t *testing.T) {
	orig := callEndpoint
	defer func() { callEndpoint = orig }()

	callEndpoint = func(ctx context.Context, ep EndpointRef, systemPrompt, userPrompt string, temperature float32, maxTokens int, onToken func(string), _ []embedded.OpenAIToolDef, _ embedded.MCPDispatcher, _, _ string) (string, embedded.ChatUsage, error) {
		if ep.ID == "ep-tool" {
			return "<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"main.go\"}}\n</tool_call>", embedded.ChatUsage{}, nil
		}
		return "실질적인 답변이다.\nCONFIDENCE: 7", embedded.ChatUsage{}, nil
	}

	var outputs []string
	results := RunProposers(context.Background(), RunProposersOptions{
		Proposers: []ProposerSpec{
			{Endpoint: EndpointRef{ID: "ep-tool", Model: "m1"}, PersonaKey: "melchior"},
			{Endpoint: EndpointRef{ID: "ep-ok", Model: "m2"}, PersonaKey: "balthasar"},
		},
		BaseSystem:  "base",
		UserPrompts: []string{"task", "task"},
		Timeout:     5 * time.Second,
		OnOutput:    func(s string) { outputs = append(outputs, s) },
	})

	if results[0].Err == nil {
		t.Fatal("tool-call-only slot should have Err set")
	}
	if results[1].Err != nil {
		t.Fatalf("substantive slot should succeed, got %v", results[1].Err)
	}

	successful := SuccessfulResults(results)
	if len(successful) != 1 {
		t.Fatalf("expected 1 successful result, got %d", len(successful))
	}

	// Failure must surface in live output as 응답 실패.
	joined := strings.Join(outputs, "")
	if !strings.Contains(joined, "응답 실패") {
		t.Errorf("live output should report the gated failure, got %q", joined)
	}
}
