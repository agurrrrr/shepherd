package embedded

import (
	"encoding/base64"
	"fmt"
	"os"
	"unicode/utf16"
)

// PowerShell invocation for the "bash" tool.
//
// This file has no build tag on purpose: everything here is pure string
// assembly, so it can be unit-tested on any platform even though it only runs
// when detectShell() lands on pwsh/powershell (see shell_windows.go — that is
// the "stock Windows without Git Bash" fallback).
//
// Two problems shape the design.
//
// 1. Quoting. Passing the command with `-Command "..."` means Go builds a
//    Windows command line with syscall.EscapeArg and PowerShell then re-parses
//    it under *different* rules. Commands containing quotes, backticks or `$`
//    get mangled silently. Since the command string is written by a model, that
//    is a matter of time rather than an edge case. -EncodedCommand takes a
//    base64 of UTF-16LE and skips the re-parse entirely.
//
// 2. Exit codes. PowerShell reports success (0) for a failed cmdlet unless
//    $ErrorActionPreference is 'Stop', which would make shepherd treat a broken
//    build as a passing one. The preamble below fixes that; the epilogue
//    forwards a native program's exit code, which PowerShell otherwise drops.

const (
	// psPreamble runs before the model's command.
	//
	// The $PSVersionTable guard is load-bearing, not defensive: $PSStyle only
	// exists in PowerShell 7+, so on Windows PowerShell 5.1 the assignment
	// would be a property set on $null — and with $ErrorActionPreference
	// already 'Stop' that is a terminating error, killing *every* command on
	// the 5.1 path at the first line.
	//
	// chcp 65001 is deliberately absent: stdout here is a pipe (bytes.Buffer),
	// and chcp.com only touches a real console's code page. Decoding whatever
	// the shell emits is handled Go-side.
	psPreamble = `$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
if ($PSVersionTable.PSVersion.Major -ge 7) { $PSStyle.OutputRendering = 'PlainText' }
<# ---- model-supplied command ---- #>
`

	// psEpilogue normalizes the exit code.
	//
	// $LASTEXITCODE holds the exit code of the last *native* executable and
	// stays $null for a script that only ran cmdlets — `exit $LASTEXITCODE`
	// unguarded would coerce that $null to 0, which is right by accident, but
	// on 5.1 it is also a fine way to hit an error under 'Stop'. The null check
	// keeps the "no native command ran" case on PowerShell's own exit path
	// (0 on success, 1 on a terminating error).
	psEpilogue = `
<# ---- exit code normalization ---- #>
if ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }
`

	// psEncodedCommandLimit caps the base64 payload we are willing to put on a
	// command line, in characters.
	//
	// CreateProcess accepts at most 32767 characters for the whole command
	// line, and that budget also covers the shell's path (which can be long:
	// C:\Program Files\PowerShell\7\pwsh.exe), the flags and the terminating
	// NUL. 30000 leaves ~2700 characters of headroom for those.
	//
	// UTF-16LE + base64 inflates ASCII input ~2.7x, so this threshold trips at
	// roughly 11KB of original command — well within reach of a heredoc-style
	// "write this file" command. Exceeding the limit would otherwise fail with
	// no error at all, hence the .ps1 fallback below.
	psEncodedCommandLimit = 30000

	// utf8BOM marks a script file as UTF-8.
	//
	// Windows PowerShell 5.1 reads a BOM-less .ps1 as ANSI (the system code
	// page), so any non-ASCII in the command — Korean strings, box-drawing
	// characters, smart quotes — comes out corrupted. PowerShell 7 defaults to
	// UTF-8 and tolerates the BOM, so writing it unconditionally is safe.
	utf8BOM = "\xEF\xBB\xBF"
)

// psScript wraps the model's command in the preamble/epilogue.
//
// Both fragments live in this one function so the script is assembled in a
// single place — scattering the preamble across call sites is how the 5.1
// guard above gets dropped by accident.
func psScript(command string) string {
	return psPreamble + command + psEpilogue
}

// encodeUTF16LEBase64 encodes s the way -EncodedCommand expects: UTF-16LE,
// no BOM, then standard base64.
//
// This uses unicode/utf16 rather than golang.org/x/text; x/text is only an
// indirect dependency here and the stdlib covers the conversion exactly.
func encodeUTF16LEBase64(s string) string {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 0, len(units)*2)
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// psInvocation builds the argv (excluding argv[0]) for running command under
// PowerShell, plus a release func that frees anything the invocation allocated.
//
// release is nil when there is nothing to free. When it is not nil the caller
// must run it on every exit path — success, failure and timeout alike — which
// is why it is threaded through shellProc rather than deferred locally.
func psInvocation(command string) (args []string, release func(), err error) {
	script := psScript(command)
	encoded := encodeUTF16LEBase64(script)

	if len(encoded) <= psEncodedCommandLimit {
		return []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encoded}, nil, nil
	}

	path, release, err := writePowerShellScript(script)
	if err != nil {
		return nil, nil, err
	}
	return []string{"-NoProfile", "-NonInteractive", "-File", path}, release, nil
}

// writePowerShellScript spills a script too large for the command line into a
// temp .ps1 and returns its path plus the func that deletes it.
func writePowerShellScript(script string) (path string, release func(), err error) {
	f, err := os.CreateTemp("", "shepherd-*.ps1")
	if err != nil {
		return "", nil, fmt.Errorf("create temp PowerShell script: %w", err)
	}
	name := f.Name()
	remove := func() { os.Remove(name) }

	if _, err := f.WriteString(utf8BOM + script); err != nil {
		f.Close()
		remove()
		return "", nil, fmt.Errorf("write temp PowerShell script: %w", err)
	}
	// Close before handing the path to PowerShell: on Windows an open handle
	// can make the file unreadable to another process.
	if err := f.Close(); err != nil {
		remove()
		return "", nil, fmt.Errorf("close temp PowerShell script: %w", err)
	}

	return name, remove, nil
}
