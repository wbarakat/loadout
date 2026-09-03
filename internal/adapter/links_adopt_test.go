package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/adapter"
	"loadout.dev/loadout/internal/vault"
)

// writeSourceSkill builds a real skill folder at dir: a SKILL.md with
// the given frontmatter and body, plus any extra files given as a
// relative path -> content map.
func writeSourceSkill(t *testing.T, dir, frontmatter, body string, extra map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\n" + frontmatter + "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range extra {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLinkSkillsAdoptsCapturedSourceFolder is the adoption case that
// makes an import usable in the tool it came FROM. The vault's copy was
// imported from this very folder, so it holds every byte of it, with
// only the frontmatter rewritten (by:/at:/review: added). Linking must
// replace the folder rather than report it blocked forever.
func TestLinkSkillsAdoptsCapturedSourceFolder(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()

	vaultSkill := filepath.Join(vaultSkillsDir, "a")
	writeSourceSkill(t, vaultSkill,
		"name: a\ndescription: d\nby: import:claude-code\nat: 2026-01-01T00:00:00Z\nreview: draft\n",
		"Do the thing.", map[string]string{"helper.sh": "#!/bin/sh\necho hi\n"})

	srcFolder := filepath.Join(dst, "a")
	writeSourceSkill(t, srcFolder, "name: a\ndescription: d\n",
		"Do the thing.", map[string]string{"helper.sh": "#!/bin/sh\necho hi\n"})

	_, adopted, _, blocked, err := adapter.LinkSkills(
		[]vault.Skill{{Name: "a", Dir: vaultSkill}}, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 0 {
		t.Fatalf("a fully captured source folder must not be blocked, got %v", blocked)
	}
	if len(adopted) != 1 {
		t.Fatalf("want the source folder adopted, got %v", adopted)
	}
	fi, err := os.Lstat(srcFolder)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the source folder must be replaced by a link into the vault")
	}
	target, _ := os.Readlink(srcFolder)
	if target != vaultSkill {
		t.Fatalf("link points at %q, want %q", target, vaultSkill)
	}
}

// TestLinkSkillsRefusesFolderWithUncapturedFile is the no-data-loss
// gate: a source folder holding even one file the vault does not have
// (an import skips a .git tree, an oversized file, and more) must be
// left exactly as it is, with every byte still on disk.
func TestLinkSkillsRefusesFolderWithUncapturedFile(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()

	vaultSkill := filepath.Join(vaultSkillsDir, "a")
	writeSourceSkill(t, vaultSkill,
		"name: a\ndescription: d\nby: import:claude-code\nreview: draft\n",
		"Do the thing.", nil)

	srcFolder := filepath.Join(dst, "a")
	writeSourceSkill(t, srcFolder, "name: a\ndescription: d\n", "Do the thing.",
		map[string]string{"notes/private.txt": "the vault never got this"})

	_, adopted, _, blocked, err := adapter.LinkSkills(
		[]vault.Skill{{Name: "a", Dir: vaultSkill}}, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 0 {
		t.Fatalf("a folder holding an uncaptured file must never be adopted, got %v", adopted)
	}
	if len(blocked) != 1 {
		t.Fatalf("want the folder reported blocked, got %v", blocked)
	}
	// The whole folder, and the file the vault lacked, must survive.
	got, err := os.ReadFile(filepath.Join(srcFolder, "notes", "private.txt"))
	if err != nil {
		t.Fatalf("the uncaptured file must still exist: %v", err)
	}
	if string(got) != "the vault never got this" {
		t.Fatalf("the uncaptured file changed: %q", got)
	}
}

// TestLinkSkillsRefusesFolderWithDifferentBody proves the SKILL.md body
// must match: a folder that merely shares a name with a vault skill is
// someone else's content and is never replaced.
func TestLinkSkillsRefusesFolderWithDifferentBody(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()

	vaultSkill := filepath.Join(vaultSkillsDir, "a")
	writeSourceSkill(t, vaultSkill, "name: a\nreview: draft\n", "Vault version.", nil)

	srcFolder := filepath.Join(dst, "a")
	writeSourceSkill(t, srcFolder, "name: a\n", "A completely different skill.", nil)

	_, adopted, _, blocked, err := adapter.LinkSkills(
		[]vault.Skill{{Name: "a", Dir: vaultSkill}}, vaultSkillsDir, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 0 || len(blocked) != 1 {
		t.Fatalf("a differing folder must be blocked, adopted=%v blocked=%v", adopted, blocked)
	}
	got, err := os.ReadFile(filepath.Join(srcFolder, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "A completely different skill.") {
		t.Fatalf("the user's SKILL.md was modified: %q", got)
	}
}

// TestLinkSkillsDryRunDoesNotAdoptSourceFolder proves a dry run reports
// the folder adoption it would make without touching the folder. The
// foreign-link equivalent is TestLinkSkillsDryRunAdoptsNothingOnDisk.
func TestLinkSkillsDryRunDoesNotAdoptSourceFolder(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dst := t.TempDir()

	vaultSkill := filepath.Join(vaultSkillsDir, "a")
	writeSourceSkill(t, vaultSkill, "name: a\nreview: draft\n", "Do the thing.", nil)
	srcFolder := filepath.Join(dst, "a")
	writeSourceSkill(t, srcFolder, "name: a\n", "Do the thing.", nil)

	_, adopted, _, _, err := adapter.LinkSkills(
		[]vault.Skill{{Name: "a", Dir: vaultSkill}}, vaultSkillsDir, dst, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 1 {
		t.Fatalf("a dry run must still report the adoption, got %v", adopted)
	}
	fi, err := os.Lstat(srcFolder)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("a dry run must not replace the folder on disk")
	}
}
