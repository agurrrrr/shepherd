package names

import (
	"testing"
)

func TestIsValid(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid name 양동이", "양동이", true},
		{"valid name 양말이", "양말이", true},
		{"valid name 숀", "숀", true},
		{"invalid name", "없는이름", false},
		{"empty name", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValid(tt.input)
			if result != tt.expected {
				t.Errorf("IsValid(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetNext(t *testing.T) {
	tests := []struct {
		name      string
		usedNames []string
		expected  string
	}{
		{"no used names", []string{}, "양동이"},
		{"first used", []string{"양동이"}, "양말이"},
		{"first two used", []string{"양동이", "양말이"}, "양철이"},
		{"all used", DefaultNames, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetNext(tt.usedNames)
			if result != tt.expected {
				t.Errorf("GetNext(%v) = %q, want %q", tt.usedNames, result, tt.expected)
			}
		})
	}
}

func TestCount(t *testing.T) {
	count := Count()
	if count != len(DefaultNames) {
		t.Errorf("Count() = %d, want %d", count, len(DefaultNames))
	}
}

func TestGetAll(t *testing.T) {
	all := GetAll()
	if len(all) != len(DefaultNames) {
		t.Errorf("GetAll() returned %d names, want %d", len(all), len(DefaultNames))
	}

	// 원본 슬라이스가 수정되지 않는지 확인
	all[0] = "modified"
	if DefaultNames[0] == "modified" {
		t.Error("GetAll() returned reference to original slice")
	}
}
