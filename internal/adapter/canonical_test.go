package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

// TestOwnershipSurvivesPathSpelling proves that a Loadout-owned link
// still counts as owned when the vault is addressed through a
// different, but equivalent, path spelling — for example /tmp versus
// /private/tmp on macOS, where /tmp is itself a symlink.
func TestOwnershipSurvivesPathSpelling(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "loadout-canon-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)

	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if realBase == base {
		t.Skip("this filesystem has no alternate spelling for /tmp")
	}

	vaultSkillsDir := filepath.Join(base, "vault", "skills")
	dst := filepath.Join(base, "tool", "skills")
	skillDir := filepath.Join(vaultSkillsDir, "a")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := []vault.Skill{{Name: "a", Dir: skillDir}}
	if _, _, _, err := LinkSkills(skills, vaultSkillsDir, dst, false); err != nil {
		t.Fatal(err)
	}

	// Re-address everything through the canonical spelling.
	realVaultSkillsDir := filepath.Join(realBase, "vault", "skills")
	realSkillDir := filepath.Join(realBase, "vault", "skills", "a")
	realDst := filepath.Join(realBase, "tool", "skills")
	realSkills := []vault.Skill{{Name: "a", Dir: realSkillDir}}

	_, _, blocked, err := LinkSkills(realSkills, realVaultSkillsDir, realDst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 0 {
		t.Fatalf("the link must count as owned across a path spelling difference, got blocked=%v", blocked)
	}

	if ps := checkLinks("test", realSkills, realVaultSkillsDir, realDst); len(ps) != 0 {
		t.Fatalf("checkLinks must not flag a spelling difference as foreign or unlinked: %+v", ps)
	}
}

// TestPruneRemovesStaleLinkThroughDanglingSpelling proves that a
// genuinely stale, vault-owned link is still pruned when the vault is
// addressed through a symlink-indirected path and the link's own
// target directory no longer exists (so canonicalPath cannot resolve
// it directly, and must fall back to the deepest existing ancestor).
func TestPruneRemovesStaleLinkThroughDanglingSpelling(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	link := filepath.Join(tmp, "link")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	vaultSkillsDir := filepath.Join(link, "skills")
	dst := filepath.Join(tmp, "tool-skills")
	aDir := filepath.Join(vaultSkillsDir, "a")
	bDir := filepath.Join(vaultSkillsDir, "b")
	if err := os.MkdirAll(filepath.Join(real, "skills", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(real, "skills", "b"), 0o755); err != nil {
		t.Fatal(err)
	}

	skills := []vault.Skill{{Name: "a", Dir: aDir}, {Name: "b", Dir: bDir}}
	if _, _, _, err := LinkSkills(skills, vaultSkillsDir, dst, false); err != nil {
		t.Fatal(err)
	}

	// A foreign link, unrelated to the vault, must survive untouched.
	foreignTarget := filepath.Join(tmp, "user-owned")
	if err := os.MkdirAll(foreignTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreignTarget, filepath.Join(dst, "foreign")); err != nil {
		t.Fatal(err)
	}

	// Delete skill "a" from the vault: its link's target now dangles.
	if err := os.RemoveAll(filepath.Join(real, "skills", "a")); err != nil {
		t.Fatal(err)
	}

	// Re-run with only "b" listed, still addressed through the link.
	if _, _, _, err := LinkSkills(skills[1:], vaultSkillsDir, dst, false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(dst, "a")); !os.IsNotExist(err) {
		t.Fatalf("the stale link a must be pruned, err=%v", err)
	}
	if _, err := os.Readlink(filepath.Join(dst, "b")); err != nil {
		t.Fatal("the surviving link b must stay intact")
	}
	if _, err := os.Readlink(filepath.Join(dst, "foreign")); err != nil {
		t.Fatal("the foreign link must survive untouched")
	}
}
