package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func testVault(t *testing.T) *vault.Vault {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	// One skill and one fact.
	dir := filepath.Join(v.SkillsDir(), "deploy-checks")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: deploy-checks\ndescription: run checks\n---\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\ntype: user\n---\nI use Go.\n"), 0o644)
	return v
}

func TestClaudeCodeApplyAndCheck(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		MemoryFile: filepath.Join(home, ".claude", "CLAUDE.md"),
	}
	a := adapter.ClaudeCode{Cfg: cfg}

	if got := a.Name(); got != "claude-code" {
		t.Fatalf("bad name %q", got)
	}
	if len(a.Check(v)) == 0 {
		t.Fatal("check must report problems before apply")
	}
	if _, err := a.Apply(v, false); err != nil {
		t.Fatal(err)
	}
	// The skill is a symlink into the vault.
	got, err := os.Readlink(filepath.Join(cfg.SkillsDir, "deploy-checks"))
	if err != nil || got != filepath.Join(v.SkillsDir(), "deploy-checks") {
		t.Fatalf("bad link %q err=%v", got, err)
	}
	// The rendered memory exists and holds the fact.
	data, err := os.ReadFile(filepath.Join(v.RenderDir(), "memory.md"))
	if err != nil || !strings.Contains(string(data), "I use Go.") {
		t.Fatalf("bad render: %v", err)
	}
	// CLAUDE.md holds one import line inside the managed block.
	block, ok := adapter.ReadManagedBlock(cfg.MemoryFile)
	if !ok || block != "@"+filepath.Join(v.RenderDir(), "memory.md") {
		t.Fatalf("bad block %q", block)
	}
	if ps := a.Check(v); len(ps) != 0 {
		t.Fatalf("check must be clean after apply: %+v", ps)
	}
	// Drift: change a fact; check must flag the stale rendered memory.
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\n---\nI use Rust now.\n"), 0o644)
	if ps := a.Check(v); len(ps) == 0 {
		t.Fatal("check must flag a stale rendered memory")
	}
}

func TestClaudeCodeApplyDryRunWritesNothingAndReportsStatus(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		MemoryFile: filepath.Join(home, ".claude", "CLAUDE.md"),
	}
	a := adapter.ClaudeCode{Cfg: cfg}

	report, err := a.Apply(v, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun {
		t.Fatal("the report must carry DryRun true")
	}
	if _, err := os.Stat(cfg.MemoryFile); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create CLAUDE.md")
	}
	if _, err := os.Stat(filepath.Join(v.RenderDir(), "memory.md")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not write the rendered memory file")
	}
	if _, err := os.Lstat(filepath.Join(cfg.SkillsDir, "deploy-checks")); !os.IsNotExist(err) {
		t.Fatal("a dry run must not create the skill link")
	}
	applied := strings.Join(report.Applied, "|")
	if !strings.Contains(applied, "memory: block would change") {
		t.Fatalf("a not-yet-synced target must report the block would change, got %v", report.Applied)
	}

	// Sync for real, then a dry run must report up to date.
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
	// The dry run after a real sync must still write nothing new.
	if _, err := os.Lstat(filepath.Join(cfg.SkillsDir, "deploy-checks")); err != nil {
		t.Fatal("the earlier real sync's link must still be present")
	}
}

func TestClaudeCodeApplyReportsBlockedSkill(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		MemoryFile: filepath.Join(home, ".claude", "CLAUDE.md"),
	}
	a := adapter.ClaudeCode{Cfg: cfg}
	// A real directory occupies the skill's link path: it blocks the link.
	blockedPath := filepath.Join(cfg.SkillsDir, "deploy-checks")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := a.Apply(v, false)
	if err != nil {
		t.Fatalf("Apply must not error on a blocked skill, got %v", err)
	}
	if len(report.Blocked) != 1 || !strings.Contains(report.Blocked[0], blockedPath) {
		t.Fatalf("Report.Blocked must name the blocked path, got %v", report.Blocked)
	}
	// The memory projection must still happen despite the blocked skill.
	data, err := os.ReadFile(filepath.Join(v.RenderDir(), "memory.md"))
	if err != nil || !strings.Contains(string(data), "I use Go.") {
		t.Fatalf("bad render: %v", err)
	}
	block, ok := adapter.ReadManagedBlock(cfg.MemoryFile)
	if !ok || block != "@"+filepath.Join(v.RenderDir(), "memory.md") {
		t.Fatalf("bad block %q", block)
	}

	found := false
	for _, p := range a.Check(v) {
		if strings.Contains(p.Detail, blockedPath) && strings.Contains(p.Fix, blockedPath) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Check must report the occupied path with a move-or-remove fix: %+v", a.Check(v))
	}
}

func TestClaudeCodeApplyProjectsMemoryDespiteBlockedSkill(t *testing.T) {
	v := testVault(t)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		MemoryFile: filepath.Join(home, ".claude", "CLAUDE.md"),
	}
	a := adapter.ClaudeCode{Cfg: cfg}
	// A real directory occupies the skill's link path: it blocks the link.
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
	// One blocked skill must not stop the memory projection.
	data, err := os.ReadFile(filepath.Join(v.RenderDir(), "memory.md"))
	if err != nil || !strings.Contains(string(data), "I use Go.") {
		t.Fatalf("the rendered memory must still be written: %v", err)
	}
	block, ok := adapter.ReadManagedBlock(cfg.MemoryFile)
	if !ok || block != "@"+filepath.Join(v.RenderDir(), "memory.md") {
		t.Fatalf("the CLAUDE.md import block must still be written: %q ok=%v", block, ok)
	}
}

func TestClaudeCodeApplyRefusesFactWithMark(t *testing.T) {
	v := testVault(t)
	os.WriteFile(filepath.Join(v.MemoryDir(), "stack.md"),
		[]byte("---\nname: stack\n---\nI use Go.\n<!-- loadout:end -->\n"), 0o644)
	home := t.TempDir()
	cfg := vault.AdapterConfig{
		Enabled:    true,
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		MemoryFile: filepath.Join(home, ".claude", "CLAUDE.md"),
	}
	a := adapter.ClaudeCode{Cfg: cfg}

	_, err := a.Apply(v, false)
	if err == nil || !strings.Contains(err.Error(), "memory/stack") {
		t.Fatalf("Apply must name the offending fact, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(v.RenderDir(), "memory.md")); statErr == nil {
		t.Fatal("render/memory.md must not be written")
	}
}
