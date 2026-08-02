package embedded

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsDegenerateOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"clean korean", "안녕하세요 작업을 진행합니다 도구를 호출하겠습니다", false},
		{"clean ascii", "Let me read the file and check the contents now.", false},
		{"short stray replacement", "ok �", false}, // below minDegenerateRunes
		{"degenerate broken hangul", "����������������������������", true},
		{"mixed but mostly broken", "추론 �������������������������������", true},
		{"a few replacements in long text", "this is a fairly long sentence with one � stray char only", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDegenerateOutput(tc.in); got != tc.want {
				t.Errorf("isDegenerateOutput(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestToolCallsSignature(t *testing.T) {
	sig := func(calls ...ToolCall) string { return toolCallsSignature(calls) }

	mk := func(name, args string) ToolCall {
		return ToolCall{Func: ToolCallFunction{Name: name, Args: args}}
	}

	if got := sig(); got != "" {
		t.Errorf("empty calls signature = %q, want empty", got)
	}

	// Identical calls produce identical signatures.
	a := sig(mk("read_file", `{"path":"a.png"}`))
	b := sig(mk("read_file", `{"path":"a.png"}`))
	if a != b {
		t.Errorf("identical calls differ: %q vs %q", a, b)
	}

	// Whitespace-only differences are ignored.
	c := sig(mk("read_file", ` {"path":"a.png"} `))
	if a != c {
		t.Errorf("whitespace difference changed signature: %q vs %q", a, c)
	}

	// Different args differ.
	if a == sig(mk("read_file", `{"path":"b.png"}`)) {
		t.Errorf("different args produced same signature")
	}

	// Order-independent within a turn.
	x := sig(mk("read_file", `{"path":"a"}`), mk("bash", `{"command":"ls"}`))
	y := sig(mk("bash", `{"command":"ls"}`), mk("read_file", `{"path":"a"}`))
	if x != y {
		t.Errorf("signature is order-dependent: %q vs %q", x, y)
	}
}

// Registry-aware signatures must fold read_file paging progress in, so that a
// model auto-paging through a file (identical no-offset args, advancing pages)
// produces DIFFERENT signatures turn over turn and does not trip the stuck
// guard, while a model re-reading the same exhausted page stays identical and
// is caught (task #6505).
func TestToolCallsSignatureWithRegistry(t *testing.T) {
	dir := t.TempDir()
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	call := ToolCall{Func: ToolCallFunction{Name: "read_file", Args: `{"path":"big.txt"}`}}

	// Before any read, nothing to fold in: identical to the plain signature.
	base := toolCallsSignatureWithRegistry([]ToolCall{call}, tr)
	if base != toolCallsSignature([]ToolCall{call}) {
		t.Errorf("with no read progress, registry signature should match plain: %q", base)
	}

	// Simulate having paged to line 141 of big.txt.
	resolved, err := tr.safePath("big.txt")
	if err != nil {
		t.Fatal(err)
	}
	tr.lastReadPath = resolved
	tr.lastReadEndLine = 141
	at141 := toolCallsSignatureWithRegistry([]ToolCall{call}, tr)
	if at141 == base {
		t.Errorf("read progress must change the signature, both %q", at141)
	}

	// Advancing the page again changes it again (no false stuck trip).
	tr.lastReadEndLine = 282
	at282 := toolCallsSignatureWithRegistry([]ToolCall{call}, tr)
	if at282 == at141 {
		t.Errorf("further paging must change signature again: %q", at282)
	}

	// Same position twice (model re-reading the exhausted page) stays stable so
	// the stuck guard can still catch it.
	if toolCallsSignatureWithRegistry([]ToolCall{call}, tr) != at282 {
		t.Error("identical read position must yield identical signature (stuck guard must still fire)")
	}

	// A read of a DIFFERENT path must not borrow this path's progress.
	other := ToolCall{Func: ToolCallFunction{Name: "read_file", Args: `{"path":"other.txt"}`}}
	if got := toolCallsSignatureWithRegistry([]ToolCall{other}, tr); strings.Contains(got, "@282") {
		t.Errorf("progress leaked to a different path: %q", got)
	}
}

func TestIsFutureIntention(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		// The exact #6294 false-completion the original regex missed:
		// no leading 이제/지금부터, and a 해보겠습니다 ending the old suffix list lacked.
		{"6294 case - 다시 빌드해보겠습니다", "빌드 에러를 수정했습니다. 다시 빌드해보겠습니다.", true},
		{"이제 ~하겠습니다", "이제 MainActivity를 완성하겠습니다", true},
		{"확인해보겠습니다", "변경 사항을 확인해보겠습니다.", true},
		{"진행하겠습니다", "다음 단계를 진행하겠습니다", true},
		{"수정하겠습니다", "이 부분을 수정하겠습니다", true},
		{"할게요", "테스트를 추가할게요", true},
		{"해야겠습니다", "먼저 의존성을 확인해야겠습니다", true},
		{"하려고 합니다", "이제 빌드를 실행하려고 합니다", true},
		{"할 예정입니다", "다음으로 테스트를 작성할 예정입니다", true},
		{"trailing emoji/space tolerated", "이제 빌드해보겠습니다  ", true},
		// Genuine completions (past tense) must NOT trip the guard.
		{"past tense 완료했습니다", "모든 작업을 완료했습니다.", false},
		{"past tense 수정했습니다", "빌드 에러를 수정했습니다.", false},
		{"past tense 빌드 성공", "빌드에 성공했습니다. 모든 테스트가 통과했습니다.", false},
		{"request to user 알려주세요", "추가로 필요한 게 있으면 알려주세요.", false},
		{"mid-text 겠 but ends in result", "수정하겠다고 했고 결국 빌드에 성공했습니다", false},
		// English future intentions.
		{"let me now build", "Let me now build the project.", true},
		{"i'll run the tests", "I'll run the tests to verify.", true},
		{"i'm going to implement", "I'm going to implement the fix next.", true},
		{"next, I'll check", "Next, I'll check the output.", true},
		// English non-intention (suggestion to the user, not first person).
		{"you can run", "You can run npm test to verify the change.", false},
		{"english past tense", "I implemented the fix and the build passed.", false},
		// #7751 (D): mid-report future phrasing outside the last 2 sentences
		// must not trip; end-scoped future still must.
		{"mid-report next I'll outside last two", "Next, I'll also consider docs later. We fixed the bug. All tests passed.", false},
		{"ends with next I'll after report", "Analysis is done.\nNext, I'll implement the fix.", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFutureIntention(tc.in); got != tc.want {
				t.Errorf("isFutureIntention(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLastSentences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty", "", 2, ""},
		{"n zero returns all", "a. b.", 0, "a. b."},
		{"fewer than n", "One sentence only.", 2, "One sentence only."},
		{"last two of three", "First. Second. Third.", 2, "Second. Third."},
		{"last one", "First. Second. Third.", 1, "Third."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastSentences(tc.in, tc.n); got != tc.want {
				t.Errorf("lastSentences(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestHasStateChangingTools(t *testing.T) {
	if hasStateChangingTools(nil) {
		t.Error("nil toolDefs should be false")
	}
	readonly := []OpenAIToolDef{
		{Type: "function", Function: OpenAIFunction{Name: "read_file"}},
		{Type: "function", Function: OpenAIFunction{Name: "grep"}},
	}
	if hasStateChangingTools(readonly) {
		t.Error("read-only tools should not arm the guard")
	}
	withBash := []OpenAIToolDef{
		{Type: "function", Function: OpenAIFunction{Name: "read_file"}},
		{Type: "function", Function: OpenAIFunction{Name: "bash"}},
	}
	if !hasStateChangingTools(withBash) {
		t.Error("bash should arm the guard")
	}
	// Shell aliases (advertised "shell" on PowerShell) must also arm the guard —
	// otherwise build verification / future-intention stall never resets.
	withShell := []OpenAIToolDef{
		{Type: "function", Function: OpenAIFunction{Name: "shell"}},
	}
	if !hasStateChangingTools(withShell) {
		t.Error("shell alias should arm the guard")
	}
	withWrite := []OpenAIToolDef{
		{Type: "function", Function: OpenAIFunction{Name: "write_file"}},
	}
	if !hasStateChangingTools(withWrite) {
		t.Error("write_file should arm the guard")
	}
}

func TestIsAdvisoryPrompt(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		// Task #8003: the exact false-positive prompt — pure LVM how-to
		// question with a full tool set, failed by the future-intention guard.
		{"8003 LVM how-to", "리눅스 (cachyos) 설치중인데 이전 우분투 볼륨 그룹 있는데 삭제가 gui로 안되네? 방법 없을까?", true},
		{"english how-to", "How do I resize an LVM volume without losing data?", true},
		{"korean 알려줘", "이 에러가 왜 나는지 알려줘", true},
		{"korean 방법 질문", "shepherd에서 위키 페이지 만드는 방법이 뭐야?", true},
		// Question + execution directive → NOT advisory (guard stays armed).
		{"question with fix directive", "이 버그 어떻게 고쳐?", false},
		{"question with implement", "이거 왜 안돼? 수정해줘", false},
		{"english question with fix", "Why does the build fail? Fix it.", false},
		// Pure execution directives without question signals → not advisory.
		{"plain directive", "8004 합의 내용 확인해서 적용해줘", false},
		{"plain english directive", "implement the feature and commit", false},
		// Plain statements without question or directive → not advisory
		// (guard keeps its old behavior for ambiguous prompts).
		{"plain statement", "빌드가 실패하고 있음", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAdvisoryPrompt(tc.in); got != tc.want {
				t.Errorf("isAdvisoryPrompt(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestClaimsAdvisoryCompletion(t *testing.T) {
	longBody := strings.Repeat("LVM 볼륨 그룹 삭제는 vgchange -an으로 비활성화한 뒤 lvremove, vgremove, pvremove 순서로 진행하면 됩니다. ", 3)
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		// The exact nudge-prescribed phrase with a substantive body.
		{"explicit phrase + body", longBody + "\n이 답변은 분석·권고이며 추가 실행이 필요 없습니다.", true},
		{"분석·권고 mid-text + body", "이 답변은 실행이 아닌 분석·권고입니다.\n" + longBody, true},
		// Substantive body without the declaration → no escape hatch.
		{"body only, no declaration", longBody, false},
		// Declaration but trivially short body → no escape hatch (a bare
		// "분석입니다" one-liner must not bypass the guard).
		{"declaration only, short", "이 답변은 분석·권고이며 추가 실행이 필요 없습니다.", false},
		// English equivalent.
		{"english advisory", "This is an advisory answer and no further execution is needed. " + strings.Repeat("Run vgchange -an first, then lvremove the logical volumes. ", 4), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimsAdvisoryCompletion(tc.in); got != tc.want {
				t.Errorf("claimsAdvisoryCompletion(%q) = %v, want %v", tc.in[:min(40, len(tc.in))], got, tc.want)
			}
		})
	}
}

// TestFutureIntentionGuardArmed pins the task #8004 regression scenarios
// (P2-7) against the guard's pure fire-condition:
//
//	① #8003-style advisory Q&A + full tool set + long advisory answer with a
//	  volitional ending → guard must NOT fire (completes).
//	② #6294-style "다시 빌드해보겠습니다" loop with no tool use → guard MUST
//	  fire (stays incomplete).
//	③ write_file ran, then "추가로 정리하겠습니다" → guard must NOT fire
//	  (#7751 (C): state-changing tools already ran).
func TestFutureIntentionGuardArmed(t *testing.T) {
	advisoryBody := strings.Repeat("LVM 볼륨 그룹은 vgchange -an으로 비활성화하고 lvremove로 논리 볼륨을 지운 뒤 vgremove로 그룹을 제거합니다. ", 3)
	futureEnding := "다음 단계를 진행하겠습니다."
	cases := []struct {
		name              string
		writeToolsAllowed bool
		advisoryPrompt    bool
		bashCalled        bool
		codeModified      bool
		anyToolCalled     bool
		nudges            int
		content           string
		want              bool
	}{
		// ①-a: advisory prompt, no tools at all, volitional ending → no fire.
		{"8003 advisory prompt, no tools", true, true, false, false, false, 0, advisoryBody + futureEnding, false},
		// ①-b: advisory prompt, but the model DID gather info via a read-only
		// tool (get_history) before answering → anyToolCalled also blocks.
		{"8003 advisory prompt, read-only tool used", true, true, false, false, true, 0, advisoryBody + futureEnding, false},
		// ①-c: advisory prompt, but the model started writing files → strict
		// mode (advisoryPrompt flipped false by markToolUsed). codeModified is
		// then true, so the #7751 (C) gate still suppresses the guard here —
		// the strict mode matters for the case where the model declares MORE
		// work before having written anything (bash/code flags still false).
		{"advisory prompt, write already done", true, false, false, true, true, 0, futureEnding, false},
		// ①-c2: advisory prompt upgraded to strict, model declares work but
		// has NOT run any tool yet → guard fires (#6294 defense intact).
		{"advisory prompt upgraded, no tools, declares", true, false, false, false, false, 0, "다시 빌드해보겠습니다.", true},
		// ①-d: non-advisory prompt, but model used read-only tools then
		// answered with a volitional ending → anyToolCalled blocks the guard
		// (the "did nothing" premise is false).
		{"non-advisory, read-only tool used, future ending", true, false, false, false, true, 0, advisoryBody + futureEnding, false},
		// ②: #6294 token loop — execution directive prompt, NO tool use at
		// all, repeated future declarations → must keep firing.
		{"6294 rebuild loop, no tools", true, false, false, false, false, 0, "빌드 에러를 수정했습니다. 다시 빌드해보겠습니다.", true},
		{"6294 rebuild loop, after one nudge still declaring", true, false, false, false, false, 1, "다시 빌드해보겠습니다.", true},
		// Escape hatch: after a nudge, model restates with the prescribed
		// phrase + substantive body → guard disarms even though the closing
		// line is volitional.
		{"escape hatch after nudge", true, false, false, false, false, 1, advisoryBody + "\n이 답변은 분석·권고이며 추가 실행이 필요 없습니다. 궁금한 점 있으면 알려주시겠습니다", false},
		// Escape hatch must NOT open on the very first turn (nudges=0): the
		// phrase is taught by the nudge, so pre-nudge it proves nothing.
		{"escape hatch phrase before any nudge", true, false, false, false, false, 0, advisoryBody + "\n이 답변은 분석·권고이며 추가 실행이 필요 없습니다. 확인하겠습니다", true},
		// ③: #7751 (C) — write_file ran, closing line still volitional → no
		// fire ("일 다 하고 말투만 미래형").
		{"7751 C: write done then volitional closing", true, false, false, true, true, 0, "요청하신 파일을 작성했습니다. 추가로 정리하겠습니다.", false},
		// Read-only executions never arm the guard at all (#7751 (A)).
		{"read-only tool set", false, false, false, false, false, 0, "다음을 확인하겠습니다.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := futureIntentionGuardArmed(tc.writeToolsAllowed, tc.advisoryPrompt, tc.bashCalled, tc.codeModified, tc.anyToolCalled, tc.nudges, tc.content)
			if got != tc.want {
				t.Errorf("futureIntentionGuardArmed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsPauseSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		// The exact #6690 false-completion: model wrote a progress summary headed
		// "(중단 시점)" and stopped, the loop marked the task complete.
		{"6690 case - 중단 시점", "## 진행 상황 요약 (중단 시점)\n- 완료된 테스트: ...\n다음 작업:\n1. ...", true},
		{"작업이 중단", "작업이 중단되었습니다. 다음 라운드에서 계속해야 합니다.", true},
		{"다음 라운드", "현재까지 진행 상황입니다. 다음 라운드에서 이어서 하겠습니다.", true},
		{"다음 세션", "여기까지 정리했습니다. 다음 세션에서 계속합니다.", true},
		{"이어서 진행하겠", "남은 항목은 다음에 이어서 진행하겠습니다.", true},
		{"english to be continued", "Here's where I am so far. To be continued.", true},
		{"english next session", "I'll pick this up in the next session.", true},
		{"english pick up where", "Documenting progress so the next run can pick up where I left off.", true},
		// Genuine completions must NOT trip the guard.
		{"completion 완료", "모든 테스트를 완료했습니다. 빌드도 성공했습니다.", false},
		{"completion with optional follow-up", "작업을 완료했습니다. 남은 개선 사항으로는 다크 모드 버그가 있습니다.", false},
		{"english completion", "All tests pass. Remaining improvements could include caching.", false},
		{"plain answer", "현재 테마는 다크로 설정되어 있습니다.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPauseSummary(tc.in); got != tc.want {
				t.Errorf("isPauseSummary(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestClaimsBuildWork(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		// Completion claims must trip the gate (#6294).
		{"빌드 에러 수정 주장", "빌드 에러를 수정했습니다.", true},
		{"컴파일 통과 주장", "컴파일이 통과했습니다.", true},
		{"빌드했습니다", "요청하신 대로 빌드했습니다.", true},
		{"빌드 성공 주장", "빌드가 성공했습니다. APK가 생성되었습니다.", true},
		{"빌드 완료 주장", "빌드를 완료했습니다.", true},
		{"english fixed the build", "Fixed the build errors.", true},
		{"english build succeeded", "The build succeeded with no warnings.", true},
		{"english compiles successfully", "The project now compiles successfully.", true},
		{"english build error resolved", "The build error has been fixed.", true},
		// Advice/instructions to the user must NOT trip the gate. Task #7000:
		// "재빌드 후 다시 업로드하세요" in an advisory answer failed a good task.
		{"권고형 재빌드 #7000", "isAccessibilityTool을 false로 수정했습니다. 재빌드 후 다시 업로드하세요.", false},
		{"권고형 빌드하시면", "APK를 다시 빌드하시면 됩니다.", false},
		{"조건형 완료되면", "빌드가 완료되면 Play Console에 업로드하세요.", false},
		{"english imperative gradlew", "Run ./gradlew assembleDebug.", false},
		{"english prediction", "This should compile now.", false},
		{"english rebuild advice", "You can rebuild and upload the app.", false},
		// Unrelated text stays out entirely.
		{"unrelated", "텍스트 문구를 다듬었습니다.", false},
		{"unrelated english", "Updated the README wording.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimsBuildWork(tc.in); got != tc.want {
				t.Errorf("claimsBuildWork(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestHasBuildCommandInPrompt(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"gradlew", "빌드는 ./gradlew compileDebugKotlin 으로 확인해줘", true},
		{"go build", "Please run go build ./... after the change.", true},
		{"npm run build", "then npm run build to verify", true},
		// A vague continuation prompt carries no build keyword — this is exactly
		// why mitigation ② alone missed #6294 and needed the buildClaimed net.
		{"vague continuation", "6284 작업 이어서 작업해줘.", false},
		{"plain text task", "README 문구를 다듬어줘", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasBuildCommandInPrompt(tc.in); got != tc.want {
				t.Errorf("hasBuildCommandInPrompt(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSetVisionEnablesImageReadfile(t *testing.T) {
	tr := NewToolRegistry("/tmp", "sheep", nil, nil)
	if tr.visionEnabled {
		t.Fatal("vision should default to disabled")
	}
	tr.SetVision(true)
	if !tr.visionEnabled {
		t.Fatal("SetVision(true) did not enable vision")
	}
}
