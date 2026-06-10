package embedded

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatMessageMarshalPlainContent(t *testing.T) {
	msg := ChatMessage{Role: ChatRoleUser, Content: "hello"}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	// Plain string content must serialize as a JSON string, not an array.
	var probe struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if string(probe.Content) != `"hello"` {
		t.Errorf("expected string content, got %s", probe.Content)
	}
}

func TestChatMessageMarshalMultimodal(t *testing.T) {
	msg := ChatMessage{
		Role: ChatRoleUser,
		ContentParts: []ContentPart{
			{Type: "text", Text: "look:"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
		},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"content":[`) {
		t.Errorf("expected content array, got %s", s)
	}
	if !strings.Contains(s, `"type":"image_url"`) || !strings.Contains(s, "data:image/png;base64,AAAA") {
		t.Errorf("expected image_url part, got %s", s)
	}

	// Round-trip the array shape to be sure it is valid OpenAI vision format.
	var probe struct {
		Content []map[string]interface{} `json:"content"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("content is not a valid array: %v", err)
	}
	if len(probe.Content) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(probe.Content))
	}
}
