package names

import (
	"strings"
	"testing"
)

func TestValidateCustom(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"plain korean", "탕비양", false},
		{"plain ascii", "tangbiyang", false},
		{"mixed with digits", "dot2뭉치", false},
		{"trimmed valid", "  메에롱  ", false},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"internal space", "양 동이", true},
		{"tab inside", "양\t동이", true},
		{"33 runes", strings.Repeat("양", 33), true},
		{"exactly 32 runes ok", strings.Repeat("양", 32), false},
		{"reserved shepherd", "shepherd", true},
		{"reserved 목자", "목자", true},
		{"slash", "a/b", true},
		{"backslash", `a\b`, true},
		{"colon", "a:b", true},
		{"asterisk", "a*b", true},
		{"question", "a?b", true},
		{"dquote", `a"b`, true},
		{"lt gt pipe", "a<b>c|d", true},
		{"dot prefix", ".hidden", true},
		{"dot single", ".", true},
		{"dot dot", "..", true},
		{"control char", "양\n동이", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCustom(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCustom(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestIsNumberedPoolName(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"양13", true},
		{"양1", true},
		{"양동이", false},
		{"양", false},
		{"양13a", false},
		{"羊13", false},
	}
	for _, tt := range tests {
		if got := IsNumberedPoolName(tt.input); got != tt.want {
			t.Errorf("IsNumberedPoolName(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
