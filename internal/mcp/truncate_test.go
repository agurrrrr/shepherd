package mcp

import (
	"testing"
)

func TestSanitizeString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"clean", "hello world", "hello world"},
		{"null_bytes", "hello\x00world", "helloworld"},
		{"control_chars", "abc\x03\x00\x08def", "abcdef"},
		{"fffd", "test\ufffddata", "testdata"},
		{"keep_newlines", "line1\nline2", "line1\nline2"},
		{"keep_tabs", "col1\tcol2", "col1\tcol2"},
		{"binary_blob", "\x03\x00\x08\x00\ufffd\x02\x00", ""},
		{"mixed", "정상 텍스트\x00\ufffd제어\x03문자", "정상 텍스트제어문자"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeString(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateStringBinary(t *testing.T) {
	// Simulates the #6695 bug: summary is a binary blob
	binarySummary := "\x03\x00\x08\x00\ufffdY\x00\x00\x01\x00\x1c\x00"
	got := truncateString(binarySummary, 50)
	if got != "" {
		t.Errorf("truncateString(binary) = %q, want empty string", got)
	}
	
	// Normal text should work
	got = truncateString("정상적인 요약입니다", 50)
	if got != "정상적인 요약입니다" {
		t.Errorf("truncateString(normal) = %q, want %q", got, "정상적인 요약입니다")
	}
}
