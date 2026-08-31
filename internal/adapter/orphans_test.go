package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestOrphanLinksReportsStaleVaultOwnedLink(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dir := t.TempDir()
	aDir := filepath.Join(vaultSkillsDir, "a")
	if err := os.MkdirAll(aDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := []vault.Skill{{Name: "a", Dir: aDir}}
	if _, _, _, err := LinkSkills(skills, vaultSkillsDir, dir, false); err != nil {
		t.Fatal(err)
	}

	// The skill is gone from the vault's current list (deleted), but
	// its link in dir still exists because sync has not re-run.
	ps := orphanLinks(nil, vaultSkillsDir, dir)
	if len(ps) != 1 {
		t.Fatalf("want one orphan problem, got %v", ps)
	}
	want := "stale link " + filepath.Join(dir, "a")
	if ps[0].Detail != want {
		t.Fatalf("detail=%q want=%q", ps[0].Detail, want)
	}
	if ps[0].Fix != "run: loadout sync" {
		t.Fatalf("fix=%q", ps[0].Fix)
	}
}

func TestOrphanLinksSkipsSkillsStillListed(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dir := t.TempDir()
	aDir := filepath.Join(vaultSkillsDir, "a")
	if err := os.MkdirAll(aDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := []vault.Skill{{Name: "a", Dir: aDir}}
	if _, _, _, err := LinkSkills(skills, vaultSkillsDir, dir, false); err != nil {
		t.Fatal(err)
	}
	if ps := orphanLinks(skills, vaultSkillsDir, dir); len(ps) != 0 {
		t.Fatalf("a link matching a current skill must not be flagged, got %v", ps)
	}
}

func TestOrphanLinksIgnoresForeignLinksAndRealFiles(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dir := t.TempDir()
	foreignTarget := t.TempDir()
	if err := os.Symlink(foreignTarget, filepath.Join(dir, "foreign")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ps := orphanLinks(nil, vaultSkillsDir, dir); len(ps) != 0 {
		t.Fatalf("must not flag a foreign link or a real file, got %v", ps)
	}
}

func TestOrphanLinksMissingDirIsNotAnError(t *testing.T) {
	vaultSkillsDir := t.TempDir()
	dir := filepath.Join(t.TempDir(), "missing")
	if ps := orphanLinks(nil, vaultSkillsDir, dir); len(ps) != 0 {
		t.Fatalf("a missing dir must report no problems, got %v", ps)
	}
}
