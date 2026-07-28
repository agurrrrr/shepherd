package embedded

import (
	"encoding/base64"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

// decodeUTF16LEBase64 is the inverse of encodeUTF16LEBase64, standing in for
// what PowerShell does with -EncodedCommand. Round-tripping through it is the
// only way to check the encoder without a Windows box.
func decodeUTF16LEBase64(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("UTF-16LE payload has odd length %d", len(raw))
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

// The preamble is what makes PowerShell behave like a shell shepherd can trust.
// Both guards below have bitten before: without the version check every command
// dies on 5.1, and without the null check a cmdlet-only script cannot report
// a native exit code.
func TestPSScriptGuards(t *testing.T) {
	script := psScript("go build ./...")

	checks := []struct {
		desc string
		want string
	}{
		{"error preference", `$ErrorActionPreference = 'Stop'`},
		{"UTF-8 output encoding", `[Console]::OutputEncoding = [Text.Encoding]::UTF8`},
		{"$PSStyle version guard", `if ($PSVersionTable.PSVersion.Major -ge 7) { $PSStyle.OutputRendering = 'PlainText' }`},
		{"$LASTEXITCODE null check", `if ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }`},
		{"the command itself", "go build ./..."},
	}
	for _, c := range checks {
		if !strings.Contains(script, c.want) {
			t.Errorf("script is missing the %s:\n%q", c.desc, script)
		}
	}

	// $PSStyle must never be touched before the version is known, or 5.1 hits
	// a null property assignment under 'Stop'.
	if strings.Index(script, "$PSVersionTable") > strings.Index(script, "$PSStyle") {
		t.Error("$PSStyle is referenced before the $PSVersionTable guard")
	}
	// chcp is a console-code-page tool and does nothing for a piped stdout.
	if strings.Contains(script, "chcp") {
		t.Error("script calls chcp; stdout is a pipe, so it has no effect")
	}
}

// The command must survive verbatim — that is the entire reason for
// -EncodedCommand over -Command.
func TestPSScriptWrapsCommandVerbatim(t *testing.T) {
	const command = `Write-Output "hi"`
	script := psScript(command)

	if !strings.HasPrefix(script, psPreamble) {
		t.Errorf("script does not start with the preamble:\n%q", script)
	}
	if !strings.HasSuffix(script, psEpilogue) {
		t.Errorf("script does not end with the epilogue:\n%q", script)
	}
	if got := strings.TrimSuffix(strings.TrimPrefix(script, psPreamble), psEpilogue); got != command {
		t.Errorf("command was altered: got %q, want %q", got, command)
	}
}

// The characters below are exactly the ones -Command mangles: quotes that the
// Windows command line re-parses, backticks that PowerShell reads as escapes,
// $ that triggers interpolation, and non-ASCII that a code-page mismatch eats.
func TestEncodeUTF16LEBase64RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"ascii", "go build ./..."},
		{"double quotes", `Write-Output "hello world"`},
		{"single quotes", `Write-Output 'it''s fine'`},
		{"backtick", "Write-Output `$notavar"},
		{"dollar sign", `$x = 'a$b'; Write-Output "$x"`},
		{"korean", "echo '한글 출력 테스트'"},
		{"mixed", "$msg = \"빌드 `\"완료`\" \\$100\"; Write-Output $msg"},
		{"newlines", "cd src\ngo test ./...\n"},
		{"surrogate pair", "Write-Output '🐑 flock'"},
		{"empty", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeUTF16LEBase64(t, encodeUTF16LEBase64(c.in)); got != c.in {
				t.Errorf("round trip = %q, want %q", got, c.in)
			}
		})
	}
}

// -EncodedCommand rejects a BOM in the payload.
func TestEncodeUTF16LEBase64HasNoBOM(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(encodeUTF16LEBase64("echo hi"))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		t.Error("payload starts with a UTF-16LE BOM")
	}
}

// A short command goes on the command line, and what PowerShell would decode
// has to be the script we built.
func TestPSInvocationEncodesShortCommand(t *testing.T) {
	args, release, err := psInvocation("go test ./...")
	if err != nil {
		t.Fatalf("psInvocation: %v", err)
	}
	if release != nil {
		t.Fatal("short command should not touch the filesystem")
	}
	want := []string{"-NoProfile", "-NonInteractive", "-EncodedCommand"}
	if !reflect.DeepEqual(args[:3], want) {
		t.Fatalf("flags = %q, want %q", args[:3], want)
	}
	if got := decodeUTF16LEBase64(t, args[3]); got != psScript("go test ./...") {
		t.Errorf("decoded payload = %q, want the wrapped script", got)
	}
}

// Past the threshold the command line would silently overflow CreateProcess's
// 32767-character limit, so the script has to move to a file instead.
func TestPSInvocationSpillsLongCommandToFile(t *testing.T) {
	// ~2.7x inflation means a command this size lands well past the limit.
	command := "Write-Output '" + strings.Repeat("x", psEncodedCommandLimit) + "'"

	args, release, err := psInvocation(command)
	if err != nil {
		t.Fatalf("psInvocation: %v", err)
	}
	if release == nil {
		t.Fatal("spilled invocation returned no release func; the temp file would leak")
	}
	defer release()

	if len(args) != 4 || args[2] != "-File" {
		t.Fatalf("args = %q, want -File form", args)
	}
	path := args[3]
	if !strings.HasSuffix(path, ".ps1") {
		t.Errorf("script path %q does not end in .ps1; PowerShell refuses to -File anything else", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp script: %v", err)
	}
	// PowerShell 5.1 reads a BOM-less UTF-8 .ps1 as ANSI and corrupts
	// non-ASCII, so the BOM is required rather than cosmetic.
	if !strings.HasPrefix(string(content), utf8BOM) {
		t.Error("temp script has no UTF-8 BOM")
	}
	if got := strings.TrimPrefix(string(content), utf8BOM); got != psScript(command) {
		t.Error("temp script content does not match the wrapped script")
	}
}

// The boundary matters: one character of slack either way decides between a
// working command line and a silent failure.
func TestPSInvocationThresholdBoundary(t *testing.T) {
	// Binary-search the largest command that still encodes within the limit.
	lo, hi := 0, psEncodedCommandLimit
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if len(encodeUTF16LEBase64(psScript(strings.Repeat("x", mid)))) <= psEncodedCommandLimit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	atLimit, release, err := psInvocation(strings.Repeat("x", lo))
	if err != nil {
		t.Fatalf("psInvocation at limit: %v", err)
	}
	if release != nil {
		release()
		t.Fatal("command that fits the limit was spilled to a file")
	}
	if atLimit[2] != "-EncodedCommand" {
		t.Fatalf("at limit: args = %q, want -EncodedCommand", atLimit)
	}
	if got := len(atLimit[3]); got > psEncodedCommandLimit {
		t.Fatalf("encoded payload %d exceeds the limit %d", got, psEncodedCommandLimit)
	}

	overLimit, release, err := psInvocation(strings.Repeat("x", lo+1))
	if err != nil {
		t.Fatalf("psInvocation over limit: %v", err)
	}
	if release == nil {
		t.Fatal("command one character past the limit was not spilled to a file")
	}
	defer release()
	if overLimit[2] != "-File" {
		t.Fatalf("over limit: args = %q, want -File", overLimit)
	}
}

// release is what deletes the temp script; shellProc.close runs it on every
// exit path, so it also has to tolerate being called twice.
func TestPSInvocationReleaseDeletesScript(t *testing.T) {
	args, release, err := psInvocation("Write-Output '" + strings.Repeat("y", psEncodedCommandLimit) + "'")
	if err != nil {
		t.Fatalf("psInvocation: %v", err)
	}
	if release == nil {
		t.Fatal("expected a spilled invocation")
	}
	path := args[3]
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("temp script missing before release: %v", err)
	}

	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("temp script still present after release (err = %v)", err)
	}
	release() // must not panic
}

// close() is the guarantee that a spilled script is removed on success,
// failure and timeout alike.
func TestShellProcCloseRunsReleaseOnce(t *testing.T) {
	calls := 0
	p := &shellProc{release: func() { calls++ }}

	p.close()
	p.close()
	if calls != 1 {
		t.Errorf("release called %d times, want exactly 1", calls)
	}

	// A proc that owns nothing must still be safe to close.
	(&shellProc{}).close()
	var nilProc *shellProc
	nilProc.close()
}

func TestBashToolDescriptionMentionsPowerShell(t *testing.T) {
	stubLookPath(t, map[string]string{"pwsh": "/opt/pwsh"})
	setShellConfig(t, "pwsh")

	desc := bashToolDescription()
	if !strings.Contains(desc, "PowerShell") {
		t.Errorf("PowerShell shell got no dialect hint: %q", desc)
	}
	if !strings.Contains(desc, "';'") {
		t.Errorf("description does not mention the ';' separator: %q", desc)
	}

	setShellConfig(t, "bash")
	stubLookPath(t, map[string]string{"bash": "/usr/bin/bash"})
	if desc := bashToolDescription(); strings.Contains(desc, "PowerShell") {
		t.Errorf("bash shell got a PowerShell hint: %q", desc)
	}
}
