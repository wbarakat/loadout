package vault_test

import (
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestManifestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loadout.toml")
	if err := vault.SaveManifest(path, vault.DefaultManifest()); err != nil {
		t.Fatal(err)
	}
	m, err := vault.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	cc := m.Adapters["claude-code"]
	if !cc.Enabled || cc.SkillsDir != "~/.claude/skills" || cc.MemoryFile != "~/.claude/CLAUDE.md" {
		t.Fatalf("bad claude-code config: %+v", cc)
	}
	if m.Adapters["agents-md"].Enabled {
		t.Fatal("agents-md must start disabled")
	}
}

func TestExpandPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/fakehome")
	if got := vault.ExpandPath("~/x"); got != "/tmp/fakehome/x" {
		t.Fatalf("got %q", got)
	}
	if got := vault.ExpandPath("/abs/x"); got != "/abs/x" {
		t.Fatalf("got %q", got)
	}
}
