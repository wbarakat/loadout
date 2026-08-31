package adapter_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestLinkSkills(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "tool", "skills")
	skillDir := filepath.Join(src, "deploy-checks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := []vault.Skill{{Name: "deploy-checks", Dir: skillDir}}

	blocked, err := adapter.LinkSkills(skills, dst)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("err=%v blocked=%v", err, blocked)
	}
	got, err := os.Readlink(filepath.Join(dst, "deploy-checks"))
	if err != nil || got != skillDir {
		t.Fatalf("bad link: %q err=%v", got, err)
	}
	// A second run must not fail (idempotent).
	if _, err := adapter.LinkSkills(skills, dst); err != nil {
		t.Fatal(err)
	}
}

func TestLinkSkillsRepairsWrongLink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(src, "a")
	os.MkdirAll(skillDir, 0o755)
	os.Symlink("/wrong/place", filepath.Join(dst, "a"))
	if _, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.Readlink(filepath.Join(dst, "a"))
	if got != skillDir {
		t.Fatalf("link was not repaired: %q", got)
	}
}

func TestLinkSkillsRefusesRealDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(src, "a")
	os.MkdirAll(skillDir, 0o755)
	os.MkdirAll(filepath.Join(dst, "a"), 0o755) // a real dir owned by the user
	blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0] != "a" {
		t.Fatalf("must report the blocked name, got %v", blocked)
	}
	if fi, _ := os.Lstat(filepath.Join(dst, "a")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("must not replace a real directory")
	}
}
