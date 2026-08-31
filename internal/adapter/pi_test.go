package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestPiApplyAndCheck(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".pi", "agent", "skills"),
		MemoryFile: filepath.Join(home, ".pi", "AGENTS.md"),
	}
	a := adapter.Pi{Cfg: cfg}

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

func TestPiApplyDryRunWritesNothingAndReportsStatus(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".pi", "agent", "skills"),
		MemoryFile: filepath.Join(home, ".pi", "AGENTS.md"),
	}
	a := adapter.Pi{Cfg: cfg}

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

func TestPiApplyProjectsMemoryDespiteBlockedSkill(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".pi", "agent", "skills"),
		MemoryFile: filepath.Join(home, ".pi", "AGENTS.md"),
	}
	a := adapter.Pi{Cfg: cfg}
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

func TestPiApplyRefusesFactWithMark(t *testing.T) {
	v := testVault(t)
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\n---\nI use Go.\n<!-- loadout:end -->\n"), 0o644)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".pi", "agent", "skills"),
		MemoryFile: filepath.Join(home, ".pi", "AGENTS.md"),
	}
	a := adapter.Pi{Cfg: cfg}

	_, err := a.Apply(v, false)
	if err == nil || !strings.Contains(err.Error(), "memory/stack") {
		t.Fatalf("Apply must name the offending fact, got %v", err)
	}
	if _, statErr := os.Stat(cfg.MemoryFile); statErr == nil {
		t.Fatal("the target file must not be created")
	}
}
