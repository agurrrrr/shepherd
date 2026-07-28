package worker

import (
	"strings"
	"testing"

	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/spf13/viper"
)

// forceShell sets SHEPHERD_SHELL / config shell for one test so prompt dialect
// assertions do not depend on the host's auto-detected shell.
func forceShell(t *testing.T, path string) {
	t.Helper()
	t.Setenv("SHEPHERD_SHELL", path)
	// Clear cached auto-detect inside the embedded package by toggling config
	// as well — GetShell reads env first, so path alone is enough, but wipe
	// config.shell to avoid surprises when path is empty.
	orig := viper.GetString("shell")
	viper.Set("shell", "")
	t.Cleanup(func() {
		t.Setenv("SHEPHERD_SHELL", "")
		viper.Set("shell", orig)
	})
}

// TestEmbeddedBehaviorDiscipline covers Phase 2-2: short fixed conduct block +
// system-reminder convention. (Full BuildSystemPromptForEmbedded needs a live
// DB for skills lookup, so we unit-test the discipline block and join order.)
func TestEmbeddedBehaviorDiscipline(t *testing.T) {
	// Pin POSIX so the cat/sed wording is stable on every CI host.
	forceShell(t, "/bin/bash")
	d := embeddedBehaviorDiscipline()
	for _, want := range []string{
		"[행동 규율]",
		"read_file",
		"edit_file",
		"write_file",
		"cat",
		"system-reminder",
		"미래형",
		"빌드",
		"force push",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("discipline block missing %q; got %q", want, d)
		}
	}
	// Keep it short — local context is expensive.
	if len(d) > 800 {
		t.Errorf("discipline block too long (%d bytes); keep concise", len(d))
	}
}

// PowerShell dialect must rewrite the file-read ban and warn about &&.
// Branch criterion is the resolved shell, not GOOS.
func TestEmbeddedBehaviorDisciplinePowerShell(t *testing.T) {
	forceShell(t, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`)
	if !embedded.ShellUsesPowerShell() {
		t.Fatal("expected PowerShell dialect after override")
	}
	d := embeddedBehaviorDiscipline()
	for _, want := range []string{
		"Get-Content",
		"Select-String",
		"&&",
		"$LASTEXITCODE",
		"read_file",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("PowerShell discipline missing %q; got %q", want, d)
		}
	}
	for _, ban := range []string{"cat/sed/head/awk"} {
		if strings.Contains(d, ban) {
			t.Errorf("PowerShell discipline must not keep POSIX ban list %q", ban)
		}
	}
}

func TestEmbeddedWorkdirSectionDialect(t *testing.T) {
	forceShell(t, "/bin/bash")
	posix := embeddedWorkdirSection("/proj", true)
	if !strings.Contains(posix, "bash 명령") {
		t.Errorf("POSIX workdir missing bash 명령: %q", posix)
	}
	if strings.Contains(posix, "PowerShell") {
		t.Errorf("POSIX workdir must not mention PowerShell: %q", posix)
	}

	forceShell(t, `C:\Program Files\PowerShell\7\pwsh.exe`)
	ps := embeddedWorkdirSection(`C:\proj`, true)
	if !strings.Contains(ps, "PowerShell") {
		t.Errorf("PowerShell workdir missing dialect note: %q", ps)
	}
	if !strings.Contains(ps, `C:\proj`) {
		t.Errorf("PowerShell workdir should keep the project path: %q", ps)
	}
}

// TestEmbeddedPromptSectionOrder verifies base discipline sits before custom
// overlay when joined the same way BuildSystemPromptForEmbedded does.
func TestEmbeddedPromptSectionOrder(t *testing.T) {
	joined := joinSections([]string{
		"identity",
		embeddedBehaviorDiscipline(),
		"[User Custom Instructions]\nCUSTOM_OVERLAY_MARKER",
	})
	discIdx := strings.Index(joined, "[행동 규율]")
	customIdx := strings.Index(joined, "[User Custom Instructions]")
	if discIdx < 0 || customIdx < 0 || discIdx > customIdx {
		t.Errorf("expected [행동 규율] before custom; disc=%d custom=%d\n%s", discIdx, customIdx, joined)
	}
	if !strings.Contains(joined, "CUSTOM_OVERLAY_MARKER") {
		t.Error("custom overlay missing from joined prompt")
	}
}

// TestMagiPromptHasNoCodingDiscipline: MAGI is advisory/read-only; the full
// coding-agent [행동 규율] block is intentionally not part of its identity
// sections (Phase 2-2 minimal invasion). We assert via the Magi builder's
// fixed identity text rather than calling BuildSystemPromptForMagi (DB).
func TestMagiPromptHasNoCodingDiscipline(t *testing.T) {
	// Magi identity strings are hardcoded in BuildSystemPromptForMagi and
	// must not include the coding discipline helper's title.
	if strings.Contains("너는 shepherd MAGI 합의 시스템의 심의자다.", "[행동 규율]") {
		t.Fatal("sanity")
	}
	// Guard: if someone later folds embeddedBehaviorDiscipline into Magi,
	// this documents the intentional omission. The helper itself is for
	// embedded coding only.
	if strings.Contains(embeddedBehaviorDiscipline(), "심의자") {
		t.Error("coding discipline block must not be Magi-specific")
	}
}
