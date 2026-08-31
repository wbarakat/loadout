package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func writeSkill(t *testing.T, v *vault.Vault, name, description string) {
	t.Helper()
	dir := filepath.Join(v.SkillsDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListSkills(t *testing.T) {
	v := newVault(t)
	writeSkill(t, v, "deploy-checks", "run checks before a deploy")
	if err := os.MkdirAll(filepath.Join(v.SkillsDir(), "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("want 1 skill, got %d", len(skills))
	}
	s := skills[0]
	if s.Name != "deploy-checks" || s.Description != "run checks before a deploy" {
		t.Fatalf("bad skill: %+v", s)
	}
	if s.Dir != filepath.Join(v.SkillsDir(), "deploy-checks") {
		t.Fatalf("bad dir: %q", s.Dir)
	}
	bad, err := vault.InvalidSkillDirs(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || filepath.Base(bad[0]) != "broken" {
		t.Fatalf("bad invalid list: %v", bad)
	}
}

func TestListSkillsFollowsSymlinkedSkillDir(t *testing.T) {
	v := newVault(t)
	writeSkill(t, v, "deploy-checks", "run checks before a deploy")
	realDir := filepath.Join(v.SkillsDir(), "deploy-checks")
	aliasDir := filepath.Join(v.SkillsDir(), "deploy-checks-alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Fatal(err)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["deploy-checks"] || !names["deploy-checks-alias"] {
		t.Fatalf("both the real dir and the symlinked alias must list: %v", skills)
	}

	bad, err := vault.InvalidSkillDirs(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("the symlinked alias must not be invalid: %v", bad)
	}
}
