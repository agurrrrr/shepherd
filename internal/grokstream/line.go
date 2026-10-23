package grokstream

// MaxLineBytes is how much of each NDJSON line we keep for parsing.
//
// Grok 1.0.3 ACP tool_call_update lines can exceed 1MB (observed 1.36MB).
// A bufio.Scanner with a 1MB cap returns ErrTooLong and stops the reader,
// which fills the 64KB stdout pipe and leaves grok blocked in pipe_write.
// Thought / message / tool_call-start lines are small; 256KB is enough to
// parse them. Pass this to procutil.ReadCappedLine, which truncates longer
// lines and discards the rest up to the next newline so the reader stays
// alive.
const MaxLineBytes = 256 * 1024
