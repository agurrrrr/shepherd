package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agurrrrr/shepherd/internal/config"
)

// TestSheepMemorySmoke exercises the per-sheep memory injection in isolation
// (i.e. without spinning up the DB). Goal: verify the section is gated by
// include_sheep_memory, that {{.MemoryDir}} substitution works, that an empty
// sheep name falls back to the <SHEEP_MEMORY_DIR> placeholder, and that the
// memory directory is created on first use.
func TestSheepMemorySmoke(t *testing.T) {
	if err := config.Init(); err != nil {
		t.Fatalf("config init: %v", err)
	}

	origEnabled := config.GetBool("include_sheep_memory")
	origPrompt := config.GetString("sheep_memory_prompt")
	t.Cleanup(func() {
		_ = config.Set("include_sheep_memory", origEnabled)
		_ = config.Set("sheep_memory_prompt", origPrompt)
	})

	// Disabled → empty
	_ = config.Set("include_sheep_memory", false)
	if got := buildSheepMemorySection(""); got != "" {
		t.Errorf("[disabled,empty-sheep] expected empty, got %q", got)
	}
	if got := buildSheepMemorySection("any"); got != "" {
		t.Errorf("[disabled,named-sheep] expected empty, got %q", got)
	}

	// Enabled, empty prompt → still empty (user can disable by clearing it)
	_ = config.Set("include_sheep_memory", true)
	_ = config.Set("sheep_memory_prompt", "")
	if got := buildSheepMemorySection("any"); got != "" {
		t.Errorf("[enabled,blank-prompt] expected empty, got %q", got)
	}

	// Enabled, custom prompt, no sheep → placeholder substitution
	_ = config.Set("sheep_memory_prompt", "DIR={{.MemoryDir}}")
	got := buildSheepMemorySection("")
	if !strings.Contains(got, "DIR=<SHEEP_MEMORY_DIR>") {
		t.Errorf("[enabled,no-sheep] placeholder not substituted: %q", got)
	}
	if !strings.Contains(got, "현재 MEMORY.md") {
		t.Errorf("[enabled,no-sheep] missing index header: %q", got)
	}

	// Enabled, named sheep → dir created + content substituted
	const sheepName = "_smoke_test_sheep"
	dir := config.GetSheepMemoryDir(sheepName)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	got = buildSheepMemorySection(sheepName)
	if !strings.Contains(got, "DIR="+dir) {
		t.Errorf("[named] dir not substituted: %q", got)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("[named] dir not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "MEMORY.md")); err != nil {
		t.Errorf("[named] MEMORY.md seed not created: %v", err)
	}

	// Seed MEMORY.md contents must flow through into the rendered section.
	if !strings.Contains(got, sheepName) {
		t.Errorf("[named] MEMORY.md seed contents missing from injection: %q", got)
	}

	// Default prompt restoration also works (sanity)
	_ = config.Set("sheep_memory_prompt", config.DefaultSheepMemoryPrompt)
	got = buildSheepMemorySection(sheepName)
	if !strings.Contains(got, "Sheep Personal Memory") {
		t.Errorf("[default-prompt] expected default header, got first 200 bytes: %q", truncateStr(got, 200))
	}
}
