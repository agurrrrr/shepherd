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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFutureIntention(tc.in); got != tc.want {
				t.Errorf("isFutureIntention(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMentionsBuildWork(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"빌드", "빌드 에러를 수정했습니다.", true},
		{"컴파일", "컴파일이 통과했습니다.", true},
		{"english build", "Fixed the build errors.", true},
		{"compile", "This should compile now.", true},
		{"gradlew", "Run ./gradlew assembleDebug.", true},
		{"unrelated", "텍스트 문구를 다듬었습니다.", false},
		{"unrelated english", "Updated the README wording.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mentionsBuildWork(tc.in); got != tc.want {
				t.Errorf("mentionsBuildWork(%q) = %v, want %v", tc.in, got, tc.want)
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
