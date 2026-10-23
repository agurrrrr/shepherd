package worker

import (
	"context"
	"strings"
	"testing"
)

// claudeImageResultLine mimics the stream-json user event Claude emits for a
// Read of a screenshot: the base64 shows up in message and tool_use_result.
func claudeImageResultLine(base64Len int) string {
	data := strings.Repeat("A", base64Len)
	return `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"` +
		data + `","media_type":"image/png"}}]}]},"tool_use_result":{"type":"image","file":{"base64":"` + data + `"}}}`
}

func TestForEachStreamLine_ResultAfterOversizedImageEvent(t *testing.T) {
	// #10882: a ~1.05MB image event stopped the old 1MB Scanner, so the
	// final result event was never seen and the task hung in running.
	image := claudeImageResultLine(527 * 1024)
	if len(image) <= 1024*1024 {
		t.Fatalf("fixture too small to reproduce: %d bytes", len(image))
	}
	tool := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/edge-long.png"}}]}}`
	result := `{"type":"result","subtype":"success","result":"done","session_id":"s1"}`
	stream := tool + "\n" + image + "\n" + result + "\n"

	var got []string
	forEachStreamLine(context.Background(), strings.NewReader(stream), func(line string) {
		got = append(got, line)
	})

	if len(got) != 2 || got[0] != tool || got[1] != result {
		t.Fatalf("got %d lines, want tool_use then result: %q", len(got), got)
	}
	if !isStreamResultEvent(got[1]) {
		t.Fatal("result event not recognized after the oversized line")
	}
	if r := parseStreamOutput(strings.Join(got, "\n")); r == nil || r.Result != "done" {
		t.Fatalf("parseStreamOutput = %+v, want result %q", r, "done")
	}
}

func TestForEachStreamLine_StopsWhenContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var got []string
	forEachStreamLine(ctx, strings.NewReader("a\nb\nc\n"), func(line string) {
		got = append(got, line)
		cancel()
	})
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %q, want only the line read before cancel", got)
	}
}
