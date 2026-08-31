package adapter_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

func TestLinkSkills(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := filepath.Join(t.TempDir(), "tool", "skills")
	skillDir := filepath.Join(vaultSkillsDir, "deploy-checks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := []vault.Skill{{Name: "deploy-checks", Dir: skillDir}}

	blocked, err := adapter.LinkSkills(skills, vaultSkillsDir, dst)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("err=%v blocked=%v", err, blocked)
	}
	got, err := os.Readlink(filepath.Join(dst, "deploy-checks"))
	if err != nil || got != skillDir {
		t.Fatalf("bad link: %q err=%v", got, err)
	}
	// A second run must not fail (idempotent).
	if _, err := adapter.LinkSkills(skills, vaultSkillsDir, dst); err != nil {
		t.Fatal(err)
	}
}

func TestLinkSkillsRepairsWrongLink(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(vaultSkillsDir, "a")
	os.MkdirAll(skillDir, 0o755)
	// A stale Loadout-owned link: it points inside the vault skills
	// directory, but at the wrong skill.
	oldDir := filepath.Join(vaultSkillsDir, "old-a")
	os.MkdirAll(oldDir, 0o755)
	os.Symlink(oldDir, filepath.Join(dst, "a"))
	if _, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, vaultSkillsDir, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.Readlink(filepath.Join(dst, "a"))
	if got != skillDir {
		t.Fatalf("link was not repaired: %q", got)
	}
}

func TestLinkSkillsRefusesRealDir(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(vaultSkillsDir, "a")
	os.MkdirAll(skillDir, 0o755)
	os.MkdirAll(filepath.Join(dst, "a"), 0o755) // a real dir owned by the user
	blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, vaultSkillsDir, dst)
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

func TestLinkSkillsProtectsForeignSymlink(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	skillDir := filepath.Join(vaultSkillsDir, "a")
	os.MkdirAll(skillDir, 0o755)
	// A symlink the user made, pointing outside the vault entirely.
	foreignTarget := filepath.Join(t.TempDir(), "user-owned")
	os.MkdirAll(foreignTarget, 0o755)
	if err := os.Symlink(foreignTarget, filepath.Join(dst, "a")); err != nil {
		t.Fatal(err)
	}

	blocked, err := adapter.LinkSkills([]vault.Skill{{Name: "a", Dir: skillDir}}, vaultSkillsDir, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0] != "a" {
		t.Fatalf("must report the blocked name, got %v", blocked)
	}
	got, err := os.Readlink(filepath.Join(dst, "a"))
	if err != nil || got != foreignTarget {
		t.Fatalf("the foreign symlink must survive unchanged: got %q err=%v", got, err)
	}
}

func TestLinkSkillsPrunesStaleLinks(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()
	aDir := filepath.Join(vaultSkillsDir, "a")
	bDir := filepath.Join(vaultSkillsDir, "b")
	os.MkdirAll(aDir, 0o755)
	os.MkdirAll(bDir, 0o755)
	skills := []vault.Skill{{Name: "a", Dir: aDir}, {Name: "b", Dir: bDir}}
	if _, err := adapter.LinkSkills(skills, vaultSkillsDir, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(filepath.Join(dst, "b")); err != nil {
		t.Fatal("b must be linked before the prune step")
	}

	// Sync again with only "a" listed: the "b" link must go away.
	if _, err := adapter.LinkSkills(skills[:1], vaultSkillsDir, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "b")); !os.IsNotExist(err) {
		t.Fatalf("the stale link b must be pruned, err=%v", err)
	}
	if _, err := os.Readlink(filepath.Join(dst, "a")); err != nil {
		t.Fatal("a must stay linked")
	}
}
