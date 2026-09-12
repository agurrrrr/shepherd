package apiclient

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSSEStream(t *testing.T) {
	body := ": connected\n\n" +
		"event: output\ndata: {\"text\":\"hello\"}\n\n" +
		"event: output\ndata: {\"text\":\"world\"}\n\n" +
		"event: done\ndata: {\"result\":\"ok\",\"files_modified\":[\"a.go\"]}\n\n"

	type frame struct {
		event string
		data  string
	}
	var got []frame

	err := parseSSEStream(strings.NewReader(body), func(eventType string, data json.RawMessage) {
		got = append(got, frame{event: eventType, data: string(data)})
	})
	if err != nil {
		t.Fatalf("parseSSEStream returned error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 frames, got %d: %+v", len(got), got)
	}
	if got[0].event != "output" || got[0].data != `{"text":"hello"}` {
		t.Errorf("frame 0 = %+v", got[0])
	}
	if got[1].event != "output" || got[1].data != `{"text":"world"}` {
		t.Errorf("frame 1 = %+v", got[1])
	}
	if got[2].event != "done" || !strings.Contains(got[2].data, `"result":"ok"`) {
		t.Errorf("frame 2 = %+v", got[2])
	}
}

func TestParseSSEStreamSkipsFramelessData(t *testing.T) {
	// A data line without a preceding event line must not be emitted.
	var got []string
	err := parseSSEStream(strings.NewReader("data: {\"x\":1}\n\n"), func(eventType string, _ json.RawMessage) {
		got = append(got, eventType)
	})
	if err != nil {
		t.Fatalf("parseSSEStream returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no frames, got %v", got)
	}
}
