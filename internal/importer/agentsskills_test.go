package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentsSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanAgentsSkillsFindsSkillAndTagsTool(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".agents", "skills")
	writeAgentsSkill(t, dir, "mytool", "---\nname: mytool\ndescription: a shared skill\n---\n\nBody text.\n")

	skills, warnings := scanAgentsSkills([]string{dir}, "codex", ImportCtx{VaultSkillsDir: filepath.Join(t.TempDir(), "vault-skills")})
	if len(warnings) != 0 {
		t.Fatalf("want no warnings, got %+v", warnings)
	}
	if len(skills) != 1 {
		t.Fatalf("want 1 skill, got %+v", skills)
	}
	s := skills[0]
	if s.Name != "mytool" || s.Description != "a shared skill" {
		t.Fatalf("bad name/description: %+v", s)
	}
	if !strings.Contains(s.Body, "Body text.") {
		t.Fatalf("bad body: %+v", s)
	}
	if s.Tool != "codex" {
		t.Fatalf("want Tool codex, got %q", s.Tool)
	}
}

func TestScanAgentsSkillsExcludesVaultOwnedSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	vaultSkillsDir := filepath.Join(t.TempDir(), "vault-skills")
	realDir := filepath.Join(vaultSkillsDir, "loadout-owned")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(dir, "loadout-owned")); err != nil {
		t.Fatal(err)
	}

	skills, warnings := scanAgentsSkills([]string{dir}, "codex", ImportCtx{VaultSkillsDir: vaultSkillsDir})
	if len(warnings) != 0 {
		t.Fatalf("a vault-owned skill is excluded silently, want no warnings, got %+v", warnings)
	}
	if len(skills) != 0 {
		t.Fatalf("a symlink into the vault's own skills dir must be excluded, got %+v", skills)
	}
}

func TestScanAgentsSkillsDanglingLinkWarnsAndContinues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAgentsSkill(t, dir, "real", "---\nname: real\ndescription: d\n---\n\nBody.\n")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}

	skills, warnings := scanAgentsSkills([]string{dir}, "codex", ImportCtx{VaultSkillsDir: filepath.Join(t.TempDir(), "vault-skills")})
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Path, "dangling") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a warning naming the dangling link, got %+v", warnings)
	}
	if len(skills) != 1 || skills[0].Name != "real" {
		t.Fatalf("a dangling link elsewhere must not stop the real skill from being found, got %+v", skills)
	}
}

func TestScanAgentsSkillsMissingDirIsNotAProblem(t *testing.T) {
	skills, warnings := scanAgentsSkills([]string{filepath.Join(t.TempDir(), "does-not-exist")}, "codex", ImportCtx{})
	if len(skills) != 0 || len(warnings) != 0 {
		t.Fatalf("a missing dir must produce neither a skill nor a warning, got skills=%+v warnings=%+v", skills, warnings)
	}
}

func TestScanAgentsSkillsSkipsEntryWithoutFrontmatter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".agents", "skills")
	skillDir := filepath.Join(dir, "no-frontmatter")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("Just plain text.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, warnings := scanAgentsSkills([]string{dir}, "codex", ImportCtx{VaultSkillsDir: filepath.Join(t.TempDir(), "vault-skills")})
	if len(skills) != 0 {
		t.Fatalf("a SKILL.md without frontmatter must not import, got %+v", skills)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning, got %+v", warnings)
	}
}

func TestScanAgentsSkillsDedupesRepeatedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".agents", "skills")
	writeAgentsSkill(t, dir, "mytool", "---\nname: mytool\ndescription: d\n---\n\nBody.\n")

	skills, _ := scanAgentsSkills([]string{dir, dir}, "codex", ImportCtx{VaultSkillsDir: filepath.Join(t.TempDir(), "vault-skills")})
	if len(skills) != 1 {
		t.Fatalf("the same dir listed twice must only be scanned once, got %+v", skills)
	}
}
