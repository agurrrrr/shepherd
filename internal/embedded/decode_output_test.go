package embedded

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/korean"
)

func TestDecodeShellOutputUTF8Passthrough(t *testing.T) {
	in := []byte("hello 한글 UTF-8")
	if got := decodeShellOutput(in); got != string(in) {
		t.Errorf("UTF-8 passthrough failed: got %q", got)
	}
}

func TestDecodeShellOutputCP949Fallback(t *testing.T) {
	// "한글" in EUC-KR / CP949
	hangul, err := korean.EUCKR.NewEncoder().Bytes([]byte("한글"))
	if err != nil {
		t.Fatal(err)
	}
	if utf8.Valid(hangul) {
		t.Fatal("test setup: EUC-KR bytes should not be valid UTF-8")
	}
	got := decodeShellOutput(hangul)
	if got != "한글" {
		t.Errorf("CP949 fallback: got %q want 한글", got)
	}
}

func TestDecodeShellOutputNoGuessOnBinary(t *testing.T) {
	// NUL → leave as-is (not an encoding problem)
	in := []byte{0x00, 0xff, 0xfe}
	got := decodeShellOutput(in)
	if !bytes.Equal([]byte(got), in) {
		t.Errorf("binary with NUL should pass through unchanged")
	}
}

func TestDecodeShellOutputInvalidNonKoreanLeftAlone(t *testing.T) {
	// Random invalid UTF-8 that is not valid EUC-KR either.
	in := []byte{0xff, 0xff, 0xff}
	got := decodeShellOutput(in)
	if !bytes.Equal([]byte(got), in) {
		t.Errorf("undecodable bytes should pass through unchanged, got %q", got)
	}
}

// B1: Korean Windows native tools interleave CP949 text with UTF-8 fragments.
// The strict whole-buffer round-trip fails on such buffers, so the mixed
// decoder must recover the CP949 segments without corrupting the UTF-8 parts.
func TestDecodeShellOutputMixedCP949AndUTF8(t *testing.T) {
	hangul, err := korean.EUCKR.NewEncoder().Bytes([]byte("한글"))
	if err != nil {
		t.Fatal(err)
	}
	// "프로세스" CP949 + " | " UTF-8 ASCII + "이미지 이름" CP949
	imgName, err := korean.EUCKR.NewEncoder().Bytes([]byte("이미지 이름"))
	if err != nil {
		t.Fatal(err)
	}
	var in []byte
	in = append(in, hangul...)
	in = append(in, []byte(" | ")...)
	in = append(in, imgName...)

	got := decodeShellOutput(in)
	want := "한글 | 이미지 이름"
	if got != want {
		t.Errorf("mixed decode: got %q want %q", got, want)
	}
}

// B1: UTF-8 segments inside a mixed buffer must not be re-interpreted.
func TestDecodeShellOutputMixedPreservesUTF8Segments(t *testing.T) {
	cp, err := korean.EUCKR.NewEncoder().Bytes([]byte("작업 관리자"))
	if err != nil {
		t.Fatal(err)
	}
	// UTF-8 Korean prefix, then CP949 segment.
	in := append([]byte("상태: "), cp...)
	got := decodeShellOutput(in)
	want := "상태: 작업 관리자"
	if got != want {
		t.Errorf("mixed decode preserving UTF-8: got %q want %q", got, want)
	}
}

// B1: undecodable garbage (neither valid UTF-8 nor decodable EUC-KR) passes
// through the mixed decoder unchanged.
func TestDecodeShellOutputMixedRejectsNonHangulGarbage(t *testing.T) {
	in2 := []byte{0xff, 0xfe, 0xfd}
	got2 := decodeShellOutput(in2)
	if !bytes.Equal([]byte(got2), in2) {
		t.Errorf("undecodable garbage should pass through unchanged, got %q", got2)
	}
}

// Unit-test the Hangul guard directly: a run that decodes to non-Hangul is
// rejected even though it round-trips.
func TestDecodeCP949RunRequiresHangul(t *testing.T) {
	if _, _, ok := decodeCP949Run([]byte{0xa1, 0xa1}); ok {
		t.Error("U+3000-only run must be rejected (no Hangul)")
	}
	if _, _, ok := decodeCP949Run([]byte{0xff, 0xfe}); ok {
		t.Error("undecodable bytes must be rejected")
	}
	// A genuine Hangul run IS accepted.
	hangul, err := korean.EUCKR.NewEncoder().Bytes([]byte("한글"))
	if err != nil {
		t.Fatal(err)
	}
	s, n, ok := decodeCP949Run(hangul)
	if !ok || s != "한글" || n != len(hangul) {
		t.Errorf("Hangul run rejected: ok=%v s=%q n=%d", ok, s, n)
	}
}
