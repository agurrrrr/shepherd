package embedded

import (
	"strings"
	"testing"
)

func TestNormalizeConfusables(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"\u201Chello", `"hello`},
		{"hello\u201D", `hello"`},
		{"\u2018hi", "'hi"},
		{"hi\u2019", "hi'"},
		{"foo\u2014bar", "foo--bar"},
		{"10\u201320", "10-20"},
		{"wait\u2026", "wait..."},
		{"hello\u00A0world", "hello world"},
		{"plain ASCII", "plain ASCII"},
		{"", ""},
		{"hello 🌍 世界", "hello 🌍 世界"},
		{"\u201Chello\u201D\u2014world\u2026", `"hello"--world...`},
	}
	for _, tt := range tests {
		if got := normalizeConfusables(tt.in); got != tt.want {
			t.Errorf("normalizeConfusables(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHasConfusables(t *testing.T) {
	if hasConfusables("plain ASCII") {
		t.Error("plain ASCII should not have confusables")
	}
	if hasConfusables("emoji 🎉") {
		t.Error("emoji should not count as confusable")
	}
	if !hasConfusables("He said \u201Chello\u201D") {
		t.Error("smart quotes should be confusable")
	}
	if !hasConfusables("a\u00A0b") {
		t.Error("nbsp should be confusable")
	}
}

func TestBuildOffsetMapSmartQuotes(t *testing.T) {
	// "\u201Chi\u201D" → "\"hi\""
	s := "\u201Chi\u201D"
	norm, m := buildOffsetMap(s)
	if norm != `"hi"` {
		t.Fatalf("normalized = %q, want %q", norm, `"hi"`)
	}
	if len(m) != 5 { // 4 bytes + sentinel
		t.Fatalf("map len = %d, want 5", len(m))
	}
	if m[0] != 0 || m[1] != 3 || m[2] != 4 || m[3] != 5 || m[4] != 8 {
		t.Errorf("map = %v, unexpected", m)
	}
}

func TestBuildOffsetMapEmDash(t *testing.T) {
	s := "a\u2014b"
	norm, m := buildOffsetMap(s)
	if norm != "a--b" {
		t.Fatalf("normalized = %q", norm)
	}
	// map: a→0, -→1, -→1, b→4, sentinel→5
	want := []int{0, 1, 1, 4, 5}
	if len(m) != len(want) {
		t.Fatalf("map len = %d, want %d", len(m), len(want))
	}
	for i := range want {
		if m[i] != want[i] {
			t.Errorf("map[%d] = %d, want %d (full %v)", i, m[i], want[i], m)
		}
	}
}

func TestBuildOffsetMapTerminalSentinel(t *testing.T) {
	s := "hello"
	norm, m := buildOffsetMap(s)
	if norm != s {
		t.Fatalf("pure ascii should be identity, got %q", norm)
	}
	if m[len(m)-1] != len(s) {
		t.Errorf("sentinel = %d, want %d", m[len(m)-1], len(s))
	}
}

func TestFindNormalizedMatchSmartQuotes(t *testing.T) {
	text := "say \u201Chello\u201D world"
	kind, matches := findNormalizedMatchPositions(text, `"hello"`)
	if kind != normMatches {
		t.Fatalf("kind = %v, want Matches", kind)
	}
	if len(matches) != 1 {
		t.Fatalf("len = %d", len(matches))
	}
	m := matches[0]
	got := text[m.originalStart : m.originalStart+m.originalLen]
	if got != "\u201Chello\u201D" {
		t.Errorf("slice = %q", got)
	}
}

func TestFindNormalizedMatchEmDashNBSPEllipsis(t *testing.T) {
	cases := []struct {
		text, pattern string
	}{
		{"foo\u2014bar", "foo--bar"},
		{"hello\u00A0world", "hello world"},
		{"wait\u2026", "wait..."},
	}
	for _, c := range cases {
		kind, matches := findNormalizedMatchPositions(c.text, c.pattern)
		if kind != normMatches || len(matches) != 1 {
			t.Errorf("%q vs %q: kind=%v len=%d", c.text, c.pattern, kind, len(matches))
		}
	}
}

func TestFindNormalizedMatchNoMatch(t *testing.T) {
	kind, _ := findNormalizedMatchPositions("hello world", "xyz")
	if kind != normNoMatch {
		t.Errorf("kind = %v, want NoMatch", kind)
	}
}

func TestFindNormalizedMatchPartialExpansionAmbiguous(t *testing.T) {
	// Pattern "-" matching inside em-dash "—" → "--" must be Ambiguous.
	kind, _ := findNormalizedMatchPositions("\u2014", "-")
	if kind != normAmbiguous {
		t.Errorf("partial dash inside em-dash: kind=%v, want Ambiguous", kind)
	}
	kind, _ = findNormalizedMatchPositions("\u2026", ".")
	if kind != normAmbiguous {
		t.Errorf("partial dot inside ellipsis: kind=%v, want Ambiguous", kind)
	}
	kind, _ = findNormalizedMatchPositions("\u2026", "..")
	if kind != normAmbiguous {
		t.Errorf("partial .. inside ellipsis: kind=%v, want Ambiguous", kind)
	}
}

func TestFindNormalizedMatchFullExpansionAccepted(t *testing.T) {
	kind, matches := findNormalizedMatchPositions("a\u2014b", "--")
	if kind != normMatches || len(matches) != 1 {
		t.Fatalf("kind=%v len=%d", kind, len(matches))
	}
	m := matches[0]
	if text := "a\u2014b"; text[m.originalStart:m.originalStart+m.originalLen] != "\u2014" {
		t.Error("should match full em-dash")
	}
}

func TestReplaceNormalizedMatches(t *testing.T) {
	text := "say \u201Chello\u201D world"
	kind, matches := findNormalizedMatchPositions(text, `"hello"`)
	if kind != normMatches {
		t.Fatal(kind)
	}
	got := replaceNormalizedMatches(text, matches, `"goodbye"`)
	if got != `say "goodbye" world` {
		t.Errorf("got %q", got)
	}
}

func TestReplaceNormalizedPreservesSurrounding(t *testing.T) {
	text := "before \u201Ctarget\u201D after"
	_, matches := findNormalizedMatchPositions(text, `"target"`)
	got := replaceNormalizedMatches(text, matches, `"replaced"`)
	if got != `before "replaced" after` {
		t.Errorf("got %q", got)
	}
}

func TestRemapRoundtripContract(t *testing.T) {
	original := "She said \u201Cstream through\u201D clearly"
	norm, m := buildOffsetMap(original)
	pattern := `"stream through"`
	idx := strings.Index(norm, pattern)
	if idx < 0 {
		t.Fatal("pattern not in normalized")
	}
	origSlice := original[m[idx]:m[idx+len(pattern)]]
	if normalizeConfusables(origSlice) != pattern {
		t.Errorf("roundtrip failed: slice=%q", origSlice)
	}
}
