package namegen

import (
	"context"
	"fmt"
	"testing"

	"github.com/agurrrrr/shepherd/internal/config"
)

// withFakeCall swaps callProvider for the duration of a test so no real CLI or
// endpoint is touched (#638 §9).
func withFakeCall(t *testing.T, fn func(ctx context.Context, provider, model, prompt string) (string, error)) {
	t.Helper()
	orig := callProvider
	callProvider = fn
	t.Cleanup(func() { callProvider = orig })
}

func TestParseCandidates(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		used    []string
		want    []string
		wantErr bool
	}{
		{
			name: "plain json",
			raw:  `{"names":["탕비양","탕메에","건어뭉치","간식이","냉장고양"]}`,
			want: []string{"탕비양", "탕메에", "건어뭉치", "간식이", "냉장고양"},
		},
		{
			name: "fenced json",
			raw:  "```json\n{\"names\":[\"도트뭉치\",\"픽셀양\",\"도트메에\",\"스프라이트양\",\"점점양\"]}\n```",
			want: []string{"도트뭉치", "픽셀양", "도트메에", "스프라이트양", "점점양"},
		},
		{
			name:    "broken json",
			raw:     `{"names":["탕비양"`,
			wantErr: true,
		},
		{
			name:    "all invalid",
			raw:     `{"names":["a/b","weird name","shepherd",".hidden"]}`,
			wantErr: true,
		},
		{
			name: "used names dropped",
			raw:  `{"names":["탕비양","메에롱","간식이"]}`,
			used: []string{"메에롱"},
			want: []string{"탕비양", "간식이"},
		},
		{
			name: "duplicates inside answer collapse",
			raw:  `{"names":["탕비양","탕비양","간식이"]}`,
			want: []string{"탕비양", "간식이"},
		},
		{
			name:    "numbered pool form rejected",
			raw:     `{"names":["양13","양14","진짜이름"]}`,
			want:    []string{"진짜이름"},
			wantErr: false,
		},
	}

	long := ""
	for i := 0; i < 33; i++ {
		long += "양"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCandidates(tt.raw, tt.used)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}

	// Separate case: 33-rune name must be dropped (table above kept simple).
	got, err := parseCandidates(fmt.Sprintf(`{"names":["%s","짧은이름"]}`, long), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "짧은이름" {
		t.Errorf("33-rune name should be dropped, got %v", got)
	}
}

func TestSuggestUsesFakeProvider(t *testing.T) {
	viperDefaults()
	withFakeCall(t, func(ctx context.Context, provider, model, prompt string) (string, error) {
		if provider != "claude" {
			t.Errorf("provider = %q", provider)
		}
		if model != "haiku" {
			t.Errorf("model = %q", model)
		}
		return `{"names":["나가메에","나가양","mcp메에","감시양","알림뭉치"]}`, nil
	})

	got, err := Suggest(context.Background(), Request{
		ProjectName: "nagar-mcp",
		Provider:    "claude",
		Model:       "haiku",
	})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(got) == 0 || got[0] != "나가메에" {
		t.Fatalf("got %v", got)
	}
}

func TestSuggestProviderCallErrorPropagates(t *testing.T) {
	viperDefaults()
	withFakeCall(t, func(ctx context.Context, provider, model, prompt string) (string, error) {
		return "", fmt.Errorf("cli not found")
	})
	if _, err := Suggest(context.Background(), Request{ProjectName: "x", Provider: "claude"}); err == nil {
		t.Fatal("want error when provider call fails")
	}
}

func TestSuggestRejectsMagiAndAuto(t *testing.T) {
	viperDefaults()
	for _, p := range []string{"magi", "auto"} {
		if _, err := Suggest(context.Background(), Request{ProjectName: "x", Provider: p}); err == nil {
			t.Errorf("provider %q should be rejected", p)
		}
	}
}

func TestSuggestNoProviderConfigured(t *testing.T) {
	viperDefaults()
	if _, err := Suggest(context.Background(), Request{ProjectName: "x"}); err == nil {
		t.Fatal("want error when neither request nor config carries a provider")
	}
}

// viperDefaults keeps config reads deterministic for this package's tests.
func viperDefaults() {
	// config.Get* on unset keys returns zero values; nothing to seed beyond
	// what the tests set explicitly. Kept as a hook so future env leakage is
	// visible in one place.
	_ = config.GetString("language")
}
