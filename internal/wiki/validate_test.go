package wiki

import (
	"testing"
)

func TestIsWikiLink(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{"simple slug", "my_page", true},
		{"slug with hyphen", "my-page", true},
		{"slug with underscore", "my_page", true},
		{"slug with numbers", "page_123", true},
		{"http url", "http://example.com", false},
		{"https url", "https://example.com/path", false},
		{"https uppercase", "HTTPS://example.com", false},
		{"protocol relative", "//example.com/path", false},
		{"anchor", "#section", false},
		{"absolute path", "/some/path", false},
		{"www prefix", "www.example.com", false},
		{"mailto", "mailto:test@example.com", false},
		{"empty string", "", false},
		{"whitespace only", "  ", false},
		{"query param slug", "page?q=1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWikiLink(tt.target)
			if got != tt.want {
				t.Errorf("isWikiLink(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestExtractMarkdownLinks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "no links",
			content: "just plain text",
			want:    nil,
		},
		{
			name:    "empty content",
			content: "",
			want:    nil,
		},
		{
			name:    "single wiki link",
			content: "See [details](my_page) for more.",
			want:    []string{"my_page"},
		},
		{
			name:    "multiple wiki links",
			content: "Check [A](page_a) and [B](page_b).",
			want:    []string{"page_a", "page_b"},
		},
		{
			name:    "mixed wiki and http links",
			content: "See [wiki](my_slug) and [google](https://google.com).",
			want:    []string{"my_slug"},
		},
		{
			name:    "anchor links excluded",
			content: "See [top](#top) and [page](my_page).",
			want:    []string{"my_page"},
		},
		{
			name:    "absolute path links excluded",
			content: "See [assets](/images/foo.png) and [page](my_page).",
			want:    []string{"my_page"},
		},
		{
			name:    "www links excluded",
			content: "Visit [site](www.example.com) and [wiki](my_page).",
			want:    []string{"my_page"},
		},
		{
			name:    "mailto links excluded",
			content: "Email [me](mailto:test@example.com), read [doc](architecture).",
			want:    []string{"architecture"},
		},
		{
			name:    "protocol relative excluded",
			content: "CDN [link](//cdn.example.com) and [wiki](page_1).",
			want:    []string{"page_1"},
		},
		{
			name:    "duplicate links",
			content: "See [A](page_a) and also [B](page_a).",
			want:    []string{"page_a", "page_a"},
		},
		{
			name:    "multiline content",
			content: "First [link](slug_one).\n\nSecond [link](slug_two).\n\nURL [here](https://example.com).",
			want:    []string{"slug_one", "slug_two"},
		},
		{
			name:    "nested brackets",
			content: "Code `[code](not_a_link)` in [text](real_slug).",
			want:    []string{"not_a_link", "real_slug"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMarkdownLinks(tt.content)
			if len(got) != len(tt.want) {
				t.Errorf("ExtractMarkdownLinks() returned %d items, want %d: got=%v, want=%v",
					len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ExtractMarkdownLinks()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
