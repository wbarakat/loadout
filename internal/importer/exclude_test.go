package importer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/importer"
)

func TestStripLoadoutBlockRemovesWellFormedBlock(t *testing.T) {
	content := "# My own rules\n\nKeep me.\n\n<!-- loadout:begin -->\nsynced stuff\n<!-- loadout:end -->\n"
	native, damaged := importer.StripLoadoutBlock(content)
	if damaged {
		t.Fatal("a well-formed block must not be damaged")
	}
	if strings.Contains(native, "synced stuff") {
		t.Fatalf("the block content must be removed, got %q", native)
	}
	if !strings.Contains(native, "Keep me.") {
		t.Fatalf("user content outside the block must survive, got %q", native)
	}
}

func TestStripLoadoutBlockNoMarksIsUnchanged(t *testing.T) {
	content := "Just my own notes.\n"
	native, damaged := importer.StripLoadoutBlock(content)
	if damaged {
		t.Fatal("content with no marks must not be damaged")
	}
	if native != content {
		t.Fatalf("content with no marks must come back unchanged, got %q", native)
	}
}

func TestStripLoadoutBlockFlagsDamagedTwoBlocks(t *testing.T) {
	content := "<!-- loadout:begin -->\na\n<!-- loadout:end -->\nmiddle\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	native, damaged := importer.StripLoadoutBlock(content)
	if !damaged {
		t.Fatal("two begin/end pairs must be flagged as damaged")
	}
	if native != content {
		t.Fatalf("a damaged file must come back unchanged, got %q", native)
	}
}

func TestStripLoadoutBlockFlagsDamagedOrphanBegin(t *testing.T) {
	_, damaged := importer.StripLoadoutBlock("<!-- loadout:begin -->\nsome text\n")
	if !damaged {
		t.Fatal("an orphan begin mark must be flagged as damaged")
	}
}

func TestIsVaultOwnedSkillTrueForSymlinkIntoVault(t *testing.T) {
	vaultSkillsDir := filepath.Join(t.TempDir(), "vault-skills")
	realSkillDir := filepath.Join(vaultSkillsDir, "deploy-checks")
	if err := os.MkdirAll(realSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "deploy-checks")
	if err := os.Symlink(realSkillDir, link); err != nil {
		t.Fatal(err)
	}

	owned, err := importer.IsVaultOwnedSkill(link, vaultSkillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Fatal("a symlink resolving into the vault skills dir must be vault-owned")
	}
}

func TestIsVaultOwnedSkillFalseForSymlinkElsewhere(t *testing.T) {
	vaultSkillsDir := filepath.Join(t.TempDir(), "vault-skills")
	if err := os.MkdirAll(vaultSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "my-own-skill")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "my-own-skill")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}

	owned, err := importer.IsVaultOwnedSkill(link, vaultSkillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("a symlink resolving outside the vault skills dir must not be vault-owned")
	}
}

func TestIsVaultOwnedSkillDanglingSymlinkErrors(t *testing.T) {
	vaultSkillsDir := filepath.Join(t.TempDir(), "vault-skills")
	if err := os.MkdirAll(vaultSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "dangling")
	if err := os.Symlink(filepath.Join(t.TempDir(), "does-not-exist"), link); err != nil {
		t.Fatal(err)
	}

	if _, err := importer.IsVaultOwnedSkill(link, vaultSkillsDir); err == nil {
		t.Fatal("a dangling symlink must be an error, for the caller to turn into skip+warn")
	}
}

func TestIsVaultOwnedSkillFalseForRealDirectory(t *testing.T) {
	vaultSkillsDir := filepath.Join(t.TempDir(), "vault-skills")
	if err := os.MkdirAll(vaultSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(t.TempDir(), "a-real-skill")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	owned, err := importer.IsVaultOwnedSkill(realDir, vaultSkillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Fatal("a plain directory, not a symlink, must never be vault-owned")
	}
}
