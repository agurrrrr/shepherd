package procutil

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
	// A Claude stream-json Read of a phone screenshot is one ~1.05MB line
	// (the base64 appears twice). The event after it must still be readable.
	huge := `{"type":"user","message":{"content":"` + strings.Repeat("x", DefaultMaxLineBytes) + `"}}`
	rest := `{"type":"result","result":"alive"}`
	r := bufio.NewReaderSize(strings.NewReader(huge+"\n"+rest+"\n"), 4096)

	first, trunc, err := ReadCappedLine(r, DefaultMaxLineBytes)
	if err != nil {
		t.Fatalf("huge line err: %v", err)
	}
	if !trunc {
		t.Fatal("expected truncation on a line over the cap")
	}
	if len(first) != DefaultMaxLineBytes {
		t.Fatalf("kept %d, want %d", len(first), DefaultMaxLineBytes)
	}

	second, trunc, err := ReadCappedLine(r, DefaultMaxLineBytes)
	if err != nil || trunc || second != rest {
		t.Fatalf("lost line after oversize skip: %q trunc=%v err=%v", second, trunc, err)
	}
}

func TestReadCappedLine_DefaultCap(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(strings.Repeat("y", DefaultMaxLineBytes+1) + "\n"))
	got, trunc, err := ReadCappedLine(r, 0)
	if err != nil || !trunc || len(got) != DefaultMaxLineBytes {
		t.Fatalf("len=%d trunc=%v err=%v", len(got), trunc, err)
	}
}

func TestReadCappedLine_Empty(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	if _, _, err := ReadCappedLine(r, 16); err != io.EOF {
		t.Fatalf("empty reader want EOF, got %v", err)
	}
}
