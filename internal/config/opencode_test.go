package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeJSONC(t *testing.T) {
	// Mirrors a real config.json OpenCode accepts but encoding/json rejects:
	// trailing commas plus comments, with comment/comma lookalikes in strings.
	in := `{
  // default model
  "model": "opencode/deepseek-v4.1-flash",
  "url": "http://host:8080/v1", /* block
  comment */
  "text": "a, } // not a comment /* nor this */",
  "list": [1, 2, ],
  "provider": {
    "p": { "name": "q\"uoted," },
  },
}`
	var v map[string]any
	if err := json.Unmarshal(normalizeJSONC([]byte(in)), &v); err != nil {
		t.Fatalf("normalized JSONC did not parse: %v\n%s", err, normalizeJSONC([]byte(in)))
	}
	if v["model"] != "opencode/deepseek-v4.1-flash" {
		t.Errorf("model = %v", v["model"])
	}
	if v["url"] != "http://host:8080/v1" {
		t.Errorf("url = %v (// inside string must survive)", v["url"])
	}
	if v["text"] != "a, } // not a comment /* nor this */" {
		t.Errorf("text = %v (string contents must be untouched)", v["text"])
	}
	if got := v["list"].([]any); len(got) != 2 {
		t.Errorf("list = %v, want 2 elements", got)
	}
	if name := v["provider"].(map[string]any)["p"].(map[string]any)["name"]; name != `q"uoted,` {
		t.Errorf("escaped quote handling broke: %v", name)
	}
}

func TestNormalizeJSONCLeavesStrictJSONAlone(t *testing.T) {
	in := `{"a": [1, 2], "b": {"c": "d, ]"}}`
	if got := string(normalizeJSONC([]byte(in))); got != in {
		t.Errorf("strict JSON changed:\n got %s\nwant %s", got, in)
	}
}

func TestParseOpenCodeModelsOutput(t *testing.T) {
	out := "opencode/big-pickle\n\x1b[1mopencode-go/deepseek-v4.1-flash\x1b[0m\n\nqwen3.6/qwen3.6-27b\nopenrouter/anthropic/claude\nSome banner line\n/bad\nbad/\n"
	want := []string{
		"opencode/big-pickle",
		"opencode-go/deepseek-v4.1-flash",
		"qwen3.6/qwen3.6-27b",
		"openrouter/anthropic/claude",
	}
	if got := parseOpenCodeModelsOutput(out); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestListOpenCodeModelsUsesCLIListOnly(t *testing.T) {
	// The selector must mirror `opencode models`, not OpenCode's config.json.
	c := &opencodeCLIModelsCache
	c.mu.Lock()
	oldIDs, oldFetched := c.ids, c.fetched
	c.ids = []string{"opencode/a", "opencode/b", "opencode/a"}
	c.fetched = true
	c.mu.Unlock()
	t.Cleanup(func() {
		c.mu.Lock()
		c.ids, c.fetched = oldIDs, oldFetched
		c.mu.Unlock()
	})

	want := []OpenCodeModelOption{
		{ID: "opencode/a", Label: "opencode/a"},
		{ID: "opencode/b", Label: "opencode/b"},
	}
	if got := ListOpenCodeModels(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUnknownOpenCodeModelHint(t *testing.T) {
	ids := []string{"opencode/deepseek-v4.1-flash", "opencode-go/deepseek-v4.1-flash", "opencode/big-pickle"}

	if hint := unknownOpenCodeModelHint("opencode/deepseek-v4.1-flash", ids); hint != "" {
		t.Errorf("known model got hint %q", hint)
	}

	hint := unknownOpenCodeModelHint("opencode-zen/deepseek-v4.1-flash", ids)
	for _, want := range []string{`"opencode-zen/deepseek-v4.1-flash"`, `"opencode/deepseek-v4.1-flash"`, `"opencode-go/deepseek-v4.1-flash"`} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q missing %s", hint, want)
		}
	}
	if strings.Contains(hint, "big-pickle") {
		t.Errorf("hint %q suggests an unrelated model", hint)
	}

	hint = unknownOpenCodeModelHint("nope/missing", ids)
	if hint == "" || strings.Contains(hint, "did you mean") {
		t.Errorf("unknown model without lookalikes: hint %q", hint)
	}
}
