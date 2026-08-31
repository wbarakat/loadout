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
	if err := a.Apply(v); err != nil {
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

	err := a.Apply(v)
	if err == nil || !strings.Contains(err.Error(), blockedPath) {
		t.Fatalf("Apply must name the blocked path, got %v", err)
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
