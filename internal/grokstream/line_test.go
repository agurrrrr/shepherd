package grokstream

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestReadCappedLine_ShortLines(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("one\ntwo\r\nthree"))
	a, trunc, err := ReadCappedLine(r, 64)
	if err != nil || trunc || a != "one" {
		t.Fatalf("a=%q trunc=%v err=%v", a, trunc, err)
	}
	b, trunc, err := ReadCappedLine(r, 64)
	if err != nil || trunc || b != "two" {
		t.Fatalf("b=%q trunc=%v err=%v", b, trunc, err)
	}
	c, trunc, err := ReadCappedLine(r, 64)
	if err != nil || trunc || c != "three" {
		t.Fatalf("c=%q trunc=%v err=%v", c, trunc, err)
	}
	if _, _, err := ReadCappedLine(r, 64); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestReadCappedLine_ExactMaxNotTruncated(t *testing.T) {
	body := strings.Repeat("a", 64)
	r := bufio.NewReader(strings.NewReader(body + "\nnext\n"))
	got, trunc, err := ReadCappedLine(r, 64)
	if err != nil || trunc || got != body {
		t.Fatalf("got len=%d trunc=%v err=%v", len(got), trunc, err)
	}
	next, trunc, err := ReadCappedLine(r, 64)
	if err != nil || trunc || next != "next" {
		t.Fatalf("next=%q trunc=%v err=%v", next, trunc, err)
	}
}

func TestReadCappedLine_OversizeDoesNotKillReader(t *testing.T) {
	// Reproduce the 1MB+ ACP tool_call_update case at a smaller scale:
	// a 300KiB line followed by a valid text event must still be readable.
	huge := strings.Repeat("x", 300*1024)
	rest := `{"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"text":"alive"}}}}`
	r := bufio.NewReaderSize(strings.NewReader(huge+"\n"+rest+"\n"), 4096)

	first, trunc, err := ReadCappedLine(r, MaxLineBytes)
	if err != nil {
		t.Fatalf("huge line err: %v", err)
	}
	if !trunc {
		t.Fatal("expected truncation on 300KiB line with 256KiB cap")
	}
	if len(first) != MaxLineBytes {
		t.Fatalf("kept %d, want %d", len(first), MaxLineBytes)
	}

	second, trunc, err := ReadCappedLine(r, MaxLineBytes)
	if err != nil || trunc {
		t.Fatalf("second line trunc=%v err=%v", trunc, err)
	}
	ev := ParseLine(second)
	if ev == nil || ev.Type != "text" || ev.Data != "alive" {
		t.Fatalf("lost line after oversize skip: %+v raw=%q", ev, second)
	}
}

func TestReadCappedLine_Empty(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	if _, _, err := ReadCappedLine(r, 16); err != io.EOF {
		t.Fatalf("empty reader want EOF, got %v", err)
	}
}
