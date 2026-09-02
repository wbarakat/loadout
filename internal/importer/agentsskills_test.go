package importer

import (
	"fmt"
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

// keysOf returns the keys of a files map, for a readable failure
// message (the values are raw file bytes, not worth printing).
func keysOf(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	return keys
}

// realTempDir returns a fresh temp dir with every symlink in its own
// path already resolved — macOS aliases /tmp (and t.TempDir()'s own
// /var/folders) to /private/..., so a raw t.TempDir() does not equal
// its own filepath.EvalSymlinks result. collectSkillFiles is always
// called by production code with an already-resolved skill folder
// path (scanSkillEntry/scanSkillsDir both resolve via EvalSymlinks
// before calling it); a test calling it directly must do the same, or
// its own containment check (isWithinDir) sees a false escape.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCollectSkillFilesExcludesVCSAndBuildDirs is the FIX 1
// regression test, at the collectSkillFiles level: a skill folder
// holding SKILL.md (never collected — collectSkillFiles always
// excludes it), a real support file, a .git dir, a .venv dir, and a
// node_modules dir. Only the real support file must be collected;
// none of the excluded dirs' files, silently — no warning either,
// since pruning a VCS/build dir is expected, not a problem.
func TestCollectSkillFilesExcludesVCSAndBuildDirs(t *testing.T) {
	dir := realTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: d\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.sh"), []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "objects", "pack-data"), []byte("git internals"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".venv", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".venv", "lib", "somelib.py"), []byte("venv contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "x"), []byte("npm dep"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, warnings := collectSkillFiles(dir, "test")
	if len(warnings) != 0 {
		t.Fatalf("excluding a VCS/build dir is silent, want no warnings, got %+v", warnings)
	}
	if len(files) != 1 {
		t.Fatalf("want only helper.sh collected, got %+v", keysOf(files))
	}
	if _, ok := files["helper.sh"]; !ok {
		t.Fatalf("want helper.sh collected, got %+v", keysOf(files))
	}
	for rel := range files {
		if strings.Contains(rel, ".git") || strings.Contains(rel, ".venv") || strings.Contains(rel, "node_modules") {
			t.Fatalf("an excluded dir's file must never be collected, got key %q", rel)
		}
	}
}

// TestCollectSkillFilesSkipsOversizedSupportFileWithWarning is the
// per-file half of FIX 2: a single support file over the per-file
// limit is dropped with its own warning — never copied into the
// vault — while a normal-size sibling file still collects.
func TestCollectSkillFilesSkipsOversizedSupportFileWithWarning(t *testing.T) {
	dir := realTempDir(t)
	big := make([]byte, maxSkillSupportFileBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "huge.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("fits fine"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, warnings := collectSkillFiles(dir, "test")
	if _, ok := files["huge.bin"]; ok {
		t.Fatalf("an oversized support file must not be collected, got %+v", keysOf(files))
	}
	if _, ok := files["small.txt"]; !ok {
		t.Fatalf("a normal support file must still be collected, got %+v", keysOf(files))
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning for the oversized file, got %+v", warnings)
	}
}

// TestScanSkillEntrySkipsSkillOverSizeCapWithWarning is the
// whole-skill half of FIX 2: a real "skill" (hallmark) was 27MB and
// imported wholesale. Several support files, none individually over
// the per-file limit, whose SUM exceeds the per-skill cap must skip
// the whole skill, with a "too large" warning, rather than import it.
func TestScanSkillEntrySkipsSkillOverSizeCapWithWarning(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: too-big\ndescription: d\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 800*1024)
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("part%d.bin", i)), chunk, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s, warnings := scanSkillEntry(dir, "test", filepath.Join(t.TempDir(), "vault-skills"))
	if s != nil {
		t.Fatalf("a skill over the total size cap must be skipped whole, got %+v", s)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Reason, "too large") && strings.Contains(w.Reason, "too-big") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a 'too large' warning naming the skill, got %+v", warnings)
	}
}

// TestScanSkillEntryNormalSmallSkillStillImports checks the other
// half of FIX 2's own test requirement: a normal small skill, well
// under both caps, must import cleanly with no warnings.
func TestScanSkillEntryNormalSmallSkillStillImports(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: normal\ndescription: d\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.sh"), []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, warnings := scanSkillEntry(dir, "test", filepath.Join(t.TempDir(), "vault-skills"))
	if len(warnings) != 0 {
		t.Fatalf("want no warnings for a normal small skill, got %+v", warnings)
	}
	if s == nil || s.Name != "normal" {
		t.Fatalf("want the normal skill imported, got %+v", s)
	}
}
