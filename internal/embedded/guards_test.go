package embedded

import "testing"

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
