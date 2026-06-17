package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderToolResult covers how a tools/call result is flattened into the
// text the embedded agent sees, including the structuredContent fallback that
// task #6350 added for servers (e.g. nagar-mcp) that leave the text content
// array empty.
func TestRenderToolResult(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		want     string
		contains string // when set, assert substring instead of exact match
	}{
		{
			name: "text content only",
			raw:  `{"content":[{"type":"text","text":"hello"}]}`,
			want: "hello",
		},
		{
			name: "multiple text blocks joined by newline",
			raw:  `{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`,
			want: "a\nb",
		},
		{
			// nagar-mcp shape: empty content, payload lives in structuredContent.
			name:     "structured-only result falls back to structuredContent",
			raw:      `{"content":[],"structuredContent":{"namespaces":[{"name":"default"}]}}`,
			contains: `"namespaces"`,
		},
		{
			// When both are present, prefer the (canonical) text content and do
			// not duplicate the structured payload.
			name: "text content wins over structuredContent",
			raw:  `{"content":[{"type":"text","text":"canonical"}],"structuredContent":{"x":1}}`,
			want: "canonical",
		},
		{
			name: "empty content and null structuredContent yields empty",
			raw:  `{"content":[],"structuredContent":null}`,
			want: "",
		},
		{
			name: "no content at all yields empty",
			raw:  `{"content":[]}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result CallToolResult
			if err := json.Unmarshal([]byte(tt.raw), &result); err != nil {
				t.Fatalf("unmarshal raw: %v", err)
			}
			got := renderToolResult(result)
			if tt.contains != "" {
				if !strings.Contains(got, tt.contains) {
					t.Fatalf("renderToolResult() = %q, want it to contain %q", got, tt.contains)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("renderToolResult() = %q, want %q", got, tt.want)
			}
		})
	}
}
