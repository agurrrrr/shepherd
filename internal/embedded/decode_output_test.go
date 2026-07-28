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
