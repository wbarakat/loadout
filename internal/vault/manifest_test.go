package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestManifestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loadout.toml")
	if err := vault.SaveManifest(path, vault.DefaultManifest()); err != nil {
		t.Fatal(err)
	}
	m, warnings, err := vault.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("a default manifest must not warn, got %v", warnings)
	}
	cc := m.Adapters["claude-code"]
	if !cc.Enabled || cc.SkillsDir != "~/.claude/skills" || cc.MemoryFile != "~/.claude/CLAUDE.md" {
		t.Fatalf("bad claude-code config: %+v", cc)
	}
	if m.Adapters["agents-md"].Enabled {
		t.Fatal("agents-md must start disabled")
	}
}

func TestLoadManifestWarnsOnUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loadout.toml")
	if err := os.WriteFile(path, []byte("version = 1\nenable = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, warnings, err := vault.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 {
		t.Fatalf("the manifest must still load, got %+v", m)
	}
	want := "the manifest key enable is unknown; loadout ignores it."
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings = %v, want [%q]", warnings, want)
	}
}

func TestLoadManifestRejectsFutureVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loadout.toml")
	if err := os.WriteFile(path, []byte("version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := vault.LoadManifest(path)
	if err == nil {
		t.Fatal("a future manifest version must fail")
	}
	want := "the vault manifest is version 2; this loadout build understands version 1. Fix: upgrade loadout."
	if err.Error() != want {
		t.Fatalf("error text = %q, want %q", err.Error(), want)
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
