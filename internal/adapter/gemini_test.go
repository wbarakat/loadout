package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestGeminiApplyAndCheck(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".gemini", "skills"),
		MemoryFile: filepath.Join(home, ".gemini", "GEMINI.md"),
	}
	a := adapter.Gemini{Cfg: cfg}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(cfg.SkillsDir, "deploy-checks")); err != nil {
		t.Fatal("skill link is missing")
	}
	block, ok := adapter.ReadManagedBlock(cfg.MemoryFile)
	if !ok || !strings.Contains(block, "I use Go.") {
		t.Fatalf("bad block %q", block)
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
	}
	// Drift: change a fact; check must now flag the stale block.
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\n---\nI use Rust now.\n"), 0o644)
	if ps := a.Check(v); len(ps) == 0 {
		t.Fatal("check must flag a stale memory block")
	}
}

func TestGeminiApplyDryRunWritesNothingAndReportsStatus(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".gemini", "skills"),
		MemoryFile: filepath.Join(home, ".gemini", "GEMINI.md"),
	}
	a := adapter.Gemini{Cfg: cfg}

	report, err := a.Apply(v, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun {
		t.Fatal("the report must carry DryRun true")
	}
	if _, err := os.Stat(cfg.MemoryFile); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create the memory file")
	}
	if _, err := os.Lstat(filepath.Join(cfg.SkillsDir, "deploy-checks")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create the skill link")
	}
	applied := strings.Join(report.Applied, "|")
	if !strings.Contains(applied, "memory: block would change") {
		t.Fatalf("a not-yet-synced target must report the block would change, got %v", report.Applied)
	}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	report, err = a.Apply(v, true)
	if err != nil {
		t.Fatal(err)
	}
	applied = strings.Join(report.Applied, "|")
	if !strings.Contains(applied, "memory: up to date") {
		t.Fatalf("a synced target must report up to date, got %v", report.Applied)
	}
}

func TestGeminiApplyProjectsMemoryDespiteBlockedSkill(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".gemini", "skills"),
		MemoryFile: filepath.Join(home, ".gemini", "GEMINI.md"),
	}
	a := adapter.Gemini{Cfg: cfg}
	blockedPath := filepath.Join(cfg.SkillsDir, "deploy-checks")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := a.Apply(v, false)
	if err != nil {
		t.Fatalf("Apply must not error on a blocked skill, got %v", err)
	}
	if len(report.Blocked) == 0 {
		t.Fatal("Apply must still report the blocked skill")
	}
	block, ok := adapter.ReadManagedBlock(cfg.MemoryFile)
	if !ok || !strings.Contains(block, "I use Go.") {
		t.Fatalf("the memory block must still be written despite the blocked skill: %q ok=%v", block, ok)
	}
}

func TestGeminiMemoryFileCreatedOnFirstApply(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	memoryFile := filepath.Join(home, ".gemini", "GEMINI.md")
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".gemini", "skills"),
		MemoryFile: memoryFile,
	}
	a := adapter.Gemini{Cfg: cfg}

	// Memory file does not exist before Apply.
	if _, err := os.Stat(memoryFile); !os.IsNotExist(err) {
		t.Fatal("memory file must not exist before first Apply")
	}

	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}

	// Memory file exists after Apply.
	if _, err := os.Stat(memoryFile); os.IsNotExist(err) {
		t.Fatal("memory file must be created after Apply")
	}

	// Memory file contains exactly one mark pair and the footer content.
	data, err := os.ReadFile(memoryFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	beginCount := strings.Count(text, "<!-- loadout:begin -->")
	endCount := strings.Count(text, "<!-- loadout:end -->")
	if beginCount != 1 || endCount != 1 {
		t.Fatalf("memory file must have exactly one mark pair, got %d begin and %d end marks", beginCount, endCount)
	}

	block, ok := adapter.ReadManagedBlock(memoryFile)
	if !ok {
		t.Fatal("managed block must be readable")
	}
	if !strings.Contains(block, "I use Go.") {
		t.Fatalf("block must contain the fact content, got %q", block)
	}
}

func TestGeminiEnabledInRegistry(t *testing.T) {
	m := vault.DefaultManifest()
	cfg, ok := m.Adapters["gemini"]
	if !ok {
		t.Fatal("gemini must be in the default manifest")
	}
	if cfg.Enabled {
		t.Fatal("gemini must be disabled by default")
	}
	if cfg.SkillsDir != "~/.gemini/skills" {
		t.Fatalf("bad skills dir: %q", cfg.SkillsDir)
	}
	if cfg.MemoryFile != "~/.gemini/GEMINI.md" {
		t.Fatalf("bad memory file: %q", cfg.MemoryFile)
	}

	// Test that Enabled returns gemini when enabled in manifest.
	v := testVault(t)
	v.Manifest.Adapters["gemini"] = vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  "~/.gemini/skills",
		MemoryFile: "~/.gemini/GEMINI.md",
	}
	got := adapter.Enabled(v)
	names := []string{}
	for _, a := range got {
		names = append(names, a.Name())
	}
	if len(names) != 3 || names[0] != "claude-code" || names[1] != "pi" || names[2] != "gemini" {
		t.Fatalf("bad registry order: %v", names)
	}
}
