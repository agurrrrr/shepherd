package worker

import (
	"bufio"
	"context"
	"io"

	"github.com/agurrrrr/shepherd/internal/procutil"
)

// forEachStreamLine calls handle for each stdout line of a JSON-lines
// provider (Claude stream-json, OpenCode, pi) until EOF, a read error, or
// ctx is done.
//
// Lines longer than procutil.DefaultMaxLineBytes are drained and skipped:
// what is left after the cut is broken JSON no parser can use. Claude emits
// such lines for image tool results — a Read of a DPR-3 phone screenshot is
// one ~1.05MB user event, because the base64 appears in both message and
// tool_use_result. The previous bufio.Scanner (1MB cap) stopped at that line,
// so the final result event never arrived: stdin stayed open, the finished
// Claude process idled, and the task stayed running until someone stopped it
// by hand (#10867, #10869, #10876, #10882).
func forEachStreamLine(ctx context.Context, r io.Reader, handle func(line string)) {
	reader := bufio.NewReaderSize(r, 64*1024)
	for {
		line, truncated, err := procutil.ReadCappedLine(reader, procutil.DefaultMaxLineBytes)
		if err != nil || ctx.Err() != nil {
			return
		}
		if truncated {
			continue
		}
		handle(line)
	}
}
