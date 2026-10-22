package config

import (
	"context"
	"os"
	"reflect"
	"testing"
)

// liveGrokModelsOutput is the exact stdout shape of `grok models` (grok 1.0.62,
// logged in with grok.com). Keep it byte-faithful: the parser keys off the
// bullet markers and the "(default)" suffix.
const liveGrokModelsOutput = "You are logged in with grok.com.\n" +
	"\n" +
	"Default model: grok-4.7\n" +
	"\n" +
	"Available models:\n" +
	"  * grok-4.7 (default)\n" +
	"  - grok-4.7-build-fast\n" +
	"  - grok-4.6\n" +
	"  - grok-4.5\n"

func Test_parseGrokModelsOutput_live_output_parses_in_cli_order(t *testing.T) {
	got := parseGrokModelsOutput(liveGrokModelsOutput)

	want := []GrokModelOption{
		{ID: "grok-4.7", Label: "grok-4.7 (default)", IsDefault: true},
		{ID: "grok-4.7-build-fast", Label: "grok-4.7-build-fast"},
		{ID: "grok-4.6", Label: "grok-4.6"},
		{ID: "grok-4.5", Label: "grok-4.5"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseGrokModelsOutput() = %#v, want %#v", got, want)
	}
}

func Test_parseGrokModelsOutput_edge_cases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []GrokModelOption
	}{
		{
			name: "empty output",
			in:   "",
			want: nil,
		},
		{
			name: "signed out notice only",
			in:   "You are not logged in. Run `grok login` first.\n",
			want: nil,
		},
		{
			name: "default marker on second bullet",
			in:   "Available models:\n  - grok-4.8-preview\n  * grok-4.7 (default)\n",
			want: []GrokModelOption{
				{ID: "grok-4.8-preview", Label: "grok-4.8-preview"},
				{ID: "grok-4.7", Label: "grok-4.7 (default)", IsDefault: true},
			},
		},
		{
			name: "default line without bullet marker",
			in:   "Default model: grok-4.9\nAvailable models:\n  - grok-4.9\n  - grok-4.6\n",
			want: []GrokModelOption{
				{ID: "grok-4.9", Label: "grok-4.9 (default)", IsDefault: true},
				{ID: "grok-4.6", Label: "grok-4.6"},
			},
		},
		{
			name: "duplicate ids collapse",
			in:   "* grok-4.7 (default)\n- grok-4.7\n",
			want: []GrokModelOption{
				{ID: "grok-4.7", Label: "grok-4.7 (default)", IsDefault: true},
			},
		},
		{
			name: "ansi colors stripped",
			in:   "\x1b[1mAvailable models:\x1b[0m\n  \x1b[32m*\x1b[0m grok-4.7 (default)\n",
			want: []GrokModelOption{
				{ID: "grok-4.7", Label: "grok-4.7 (default)", IsDefault: true},
			},
		},
		{
			name: "prose bullets are not models",
			in:   "Default model: grok-4.7\n\nUse `-m` to pick another model.\n  - grok-4.6\n",
			want: []GrokModelOption{
				{ID: "grok-4.6", Label: "grok-4.6"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGrokModelsOutput(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseGrokModelsOutput(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func Test_CachedGrokModels_falls_back_when_cli_list_is_empty(t *testing.T) {
	// Empty cache state means the CLI could not report anything; the selector
	// must still offer the curated fallback instead of an empty <select>.
	got := grokFallbackModels()
	if len(got) == 0 {
		t.Fatal("grokFallbackModels() must never be empty")
	}
	for _, m := range got {
		if m.ID == "" {
			t.Errorf("fallback model has empty ID: %#v", m)
		}
	}
}

func Test_GrokModelsCacheInfo_reports_source(t *testing.T) {
	source, errText, fetchedAt := GrokModelsCacheInfo()
	if source != "pending" {
		t.Fatalf("fresh cache source = %q, want %q", source, "pending")
	}
	if errText != "" || !fetchedAt.IsZero() {
		t.Errorf("fresh cache should carry no error/timestamp, got %q / %v", errText, fetchedAt)
	}
}

// Test_ListGrokCLIModels_against_installed_cli exercises the real parser against
// whatever `grok models` prints on this machine. It is skipped unless
// GROK_INTEGRATION=1 so CI without a grok login stays green.
func Test_ListGrokCLIModels_against_installed_cli(t *testing.T) {
	if os.Getenv("GROK_INTEGRATION") == "" {
		t.Skip("set GROK_INTEGRATION=1 to run against the installed grok CLI")
	}
	models, err := ListGrokCLIModels(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ListGrokCLIModels() error = %v", err)
	}
	if len(models) == 0 {
		t.Fatal("ListGrokCLIModels() returned no models for a logged-in account")
	}
	for _, m := range models {
		if m.ID == "" || m.Label == "" {
			t.Errorf("model missing id/label: %#v", m)
		}
		t.Logf("grok model: %s (default=%v)", m.ID, m.IsDefault)
	}
}
