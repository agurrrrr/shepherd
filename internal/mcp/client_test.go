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

// TestExtractToolImages covers pulling image content blocks out of a tools/call
// result (task #6684). mobile_take_screenshot returns an image block with base64
// data; without extracting it the vision model sees nothing.
func TestExtractToolImages(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantCount int
		wantMIME  string
		wantData  string
	}{
		{
			name:      "image block with explicit mimeType",
			raw:       `{"content":[{"type":"image","data":"QUJD","mimeType":"image/png"}]}`,
			wantCount: 1,
			wantMIME:  "image/png",
			wantData:  "QUJD",
		},
		{
			name:      "image block missing mimeType defaults to png",
			raw:       `{"content":[{"type":"image","data":"QUJD"}]}`,
			wantCount: 1,
			wantMIME:  "image/png",
			wantData:  "QUJD",
		},
		{
			name:      "mixed text + image keeps only the image",
			raw:       `{"content":[{"type":"text","text":"ok"},{"type":"image","data":"WFla","mimeType":"image/jpeg"}]}`,
			wantCount: 1,
			wantMIME:  "image/jpeg",
			wantData:  "WFla",
		},
		{
			name:      "image block without data is skipped",
			raw:       `{"content":[{"type":"image","mimeType":"image/png"}]}`,
			wantCount: 0,
		},
		{
			name:      "text-only result has no images",
			raw:       `{"content":[{"type":"text","text":"hello"}]}`,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result CallToolResult
			if err := json.Unmarshal([]byte(tt.raw), &result); err != nil {
				t.Fatalf("unmarshal raw: %v", err)
			}
			imgs := extractToolImages(result)
			if len(imgs) != tt.wantCount {
				t.Fatalf("extractToolImages() returned %d images, want %d", len(imgs), tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}
			if imgs[0].MIMEType != tt.wantMIME {
				t.Errorf("MIMEType = %q, want %q", imgs[0].MIMEType, tt.wantMIME)
			}
			if imgs[0].Data != tt.wantData {
				t.Errorf("Data = %q, want %q", imgs[0].Data, tt.wantData)
			}
		})
	}
}
