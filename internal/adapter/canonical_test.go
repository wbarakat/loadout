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
	if _, err := LinkSkills(skills, vaultSkillsDir, dst); err != nil {
		t.Fatal(err)
	}

	// Re-address everything through the canonical spelling.
	realVaultSkillsDir := filepath.Join(realBase, "vault", "skills")
	realSkillDir := filepath.Join(realBase, "vault", "skills", "a")
	realDst := filepath.Join(realBase, "tool", "skills")
	realSkills := []vault.Skill{{Name: "a", Dir: realSkillDir}}

	blocked, err := LinkSkills(realSkills, realVaultSkillsDir, realDst)
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
