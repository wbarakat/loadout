package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestParseAddress(t *testing.T) {
	kind, name, err := vault.ParseAddress("memory/my-stack")
	if err != nil || kind != "memory" || name != "my-stack" {
		t.Fatalf("bad parse: kind=%q name=%q err=%v", kind, name, err)
	}
	kind, name, err = vault.ParseAddress("skill/deploy-checks")
	if err != nil || kind != "skill" || name != "deploy-checks" {
		t.Fatalf("bad parse: kind=%q name=%q err=%v", kind, name, err)
	}
}

func TestParseAddressRejectsBadInput(t *testing.T) {
	for _, s := range []string{
		"deploy-checks", "widget/foo", "skill/", "/foo", "",
		"memory/../x", "memory/foo/bar", "skill/.hidden", "memory/foo\\bar",
	} {
		_, _, err := vault.ParseAddress(s)
		if err == nil {
			t.Fatalf("%q must be rejected", s)
		}
		want := s + ": not an address. Fix: use kind/name, for example memory/my-stack."
		if err.Error() != want {
			t.Fatalf("bad error for %q: got %q want %q", s, err.Error(), want)
		}
	}
}

func TestItemPath(t *testing.T) {
	v := newVault(t)
	fact := "---\nname: my-stack\ndescription: the stack I use\n---\n\nI use Go.\n"
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "my-stack.md"), []byte(fact), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, v, "deploy-checks", "run checks before a deploy")

	path, err := vault.ItemPath(v, "memory", "my-stack")
	if err != nil || path != filepath.Join(v.MemoryDir(), "my-stack.md") {
		t.Fatalf("bad memory path: %q err=%v", path, err)
	}
	path, err = vault.ItemPath(v, "skill", "deploy-checks")
	if err != nil || path != filepath.Join(v.SkillsDir(), "deploy-checks", "SKILL.md") {
		t.Fatalf("bad skill path: %q err=%v", path, err)
	}
}

func TestItemPathMissing(t *testing.T) {
	v := newVault(t)
	_, err := vault.ItemPath(v, "memory", "nope")
	if err == nil {
		t.Fatal("must error for a missing item")
	}
	want := "memory/nope: no such item. Fix: run loadout list."
	if err.Error() != want {
		t.Fatalf("bad error: got %q want %q", err.Error(), want)
	}
	_, err = vault.ItemPath(v, "skill", "nope")
	if err == nil {
		t.Fatal("must error for a missing item")
	}
	want = "skill/nope: no such item. Fix: run loadout list."
	if err.Error() != want {
		t.Fatalf("bad error: got %q want %q", err.Error(), want)
	}
}
