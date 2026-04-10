package agent

import (
	"testing"
)

func TestProviderType(t *testing.T) {
	tests := []struct {
		name     string
		provider ProviderType
		expected string
	}{
		{"Claude", ProviderClaude, "claude"},
		{"OpenCode", ProviderOpencode, "opencode"},
		{"Auto", ProviderAuto, "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.provider) != tt.expected {
				t.Errorf("ProviderType %s = %q, want %q", tt.name, tt.provider, tt.expected)
			}
		})
	}
}

func TestClaudeProviderName(t *testing.T) {
	p := NewClaudeProvider()
	if p.Name() != "claude" {
		t.Errorf("ClaudeProvider.Name() = %q, want %q", p.Name(), "claude")
	}
}

func TestOpenCodeProviderName(t *testing.T) {
	p := NewOpenCodeProvider()
	if p.Name() != "opencode" {
		t.Errorf("OpenCodeProvider.Name() = %q, want %q", p.Name(), "opencode")
	}
}

func TestRouterGetProvider(t *testing.T) {
	r := NewRouter()

	tests := []struct {
		name         string
		providerType ProviderType
		expectedName string
	}{
		{"Claude provider", ProviderClaude, "claude"},
		{"Auto defaults to Claude", ProviderAuto, "claude"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := r.GetProvider(tt.providerType)
			if p.Name() != tt.expectedName {
				t.Errorf("GetProvider(%s).Name() = %q, want %q", tt.providerType, p.Name(), tt.expectedName)
			}
		})
	}
}

func TestRouterRoute(t *testing.T) {
	r := NewRouter()

	tests := []struct {
		name         string
		prompt       string
		preferred    ProviderType
		expectedName string
	}{
		{"Explicit Claude", "코드 작성해줘", ProviderClaude, "claude"},
	}

	// OpenCode 사용 가능 여부에 따라 예상 결과 다름
	opencodeAvailable := r.IsOpenCodeAvailable()
	if opencodeAvailable {
		tests = append(tests,
			struct {
				name         string
				prompt       string
				preferred    ProviderType
				expectedName string
			}{"Auto with review keyword", "코드 검토해줘", ProviderAuto, "opencode"},
			struct {
				name         string
				prompt       string
				preferred    ProviderType
				expectedName string
			}{"Auto with opencode keyword", "로컬로 해줘", ProviderAuto, "opencode"},
		)
	} else {
		tests = append(tests,
			struct {
				name         string
				prompt       string
				preferred    ProviderType
				expectedName string
			}{"Auto with review keyword (fallback)", "코드 검토해줘", ProviderAuto, "claude"},
			struct {
				name         string
				prompt       string
				preferred    ProviderType
				expectedName string
			}{"Auto with opencode keyword (fallback)", "로컬로 해줘", ProviderAuto, "claude"},
		)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := r.Route(tt.prompt, tt.preferred)
			if p.Name() != tt.expectedName {
				t.Errorf("Route(%q, %s).Name() = %q, want %q", tt.prompt, tt.preferred, p.Name(), tt.expectedName)
			}
		})
	}
}

func TestShouldUseOpenCode(t *testing.T) {
	r := NewRouter()

	tests := []struct {
		prompt   string
		expected bool
	}{
		{"코드 작성해줘", false},
		{"코드 검토해줘", true},
		{"리뷰해줘", true},
		{"로컬로 해줘", true},
		{"opencode로 처리해", true},
		{"설명해줘", true},
		{"버그 수정해줘", false},
	}

	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			result := r.shouldUseOpenCode(tt.prompt)
			if result != tt.expected {
				t.Errorf("shouldUseOpenCode(%q) = %v, want %v", tt.prompt, result, tt.expected)
			}
		})
	}
}

