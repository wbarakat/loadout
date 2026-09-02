package importer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

// setupCodexHome copies testdata/codex-home into a fresh temp HOME
// (the fixture holds .codex/ and .agents/ directly, the way a real
// home directory would) and adds a vault-owned symlinked skill under
// .agents/skills the fixture cannot carry as a committed file. It
// returns the temp HOME and the injected vault skills dir. It reuses
// copyDir, introduced in claudecode_test.go, rather than duplicating
// a directory-copy helper.
func setupCodexHome(t *testing.T) (home, vaultSkillsDir string) {
	t.Helper()
	t.Setenv("CODEX_HOME", "")
	home = t.TempDir()
	copyDir(t, "testdata/codex-home", home)

	vaultSkillsDir = filepath.Join(t.TempDir(), "vault-skills")
	realTarget := filepath.Join(vaultSkillsDir, "loadout-dogfood")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	agentsSkills := filepath.Join(home, ".agents", "skills")
	if err := os.Symlink(realTarget, filepath.Join(agentsSkills, "loadout-dogfood")); err != nil {
		t.Fatal(err)
	}

	return home, vaultSkillsDir
}

func newCodexTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	return newClaudeCodeTestVault(t)
}

func TestCodexNameIsCodex(t *testing.T) {
	if got := (importer.Codex{}).Name(); got != "codex" {
		t.Fatalf("want codex, got %q", got)
	}
}

func TestCodexDetectsHomeDir(t *testing.T) {
	home, _ := setupCodexHome(t)
	src := importer.Codex{}

	present, root := src.Detect(importer.ImportCtx{Home: home})
	if !present {
		t.Fatal("want codex detected when ~/.codex exists")
	}
	if root != filepath.Join(home, ".codex") {
		t.Fatalf("want root %s, got %s", filepath.Join(home, ".codex"), root)
	}
}

func TestCodexDetectsAbsentHomeDir(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	src := importer.Codex{}

	present, _ := src.Detect(importer.ImportCtx{Home: t.TempDir()})
	if present {
		t.Fatal("want codex not detected when ~/.codex is absent")
	}
}

func TestCodexHomeOverridesHome(t *testing.T) {
	codexHome := t.TempDir()
	skillDir := filepath.Join(codexHome, "skills", "overridetool")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: overridetool\ndescription: found via CODEX_HOME\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	src := importer.Codex{}

	present, root := src.Detect(importer.ImportCtx{Home: filepath.Join(t.TempDir(), "unused-home")})
	if !present || root != codexHome {
		t.Fatalf("want CODEX_HOME to override Home, got present=%v root=%q", present, root)
	}
	skills, _, err := src.Skills(importer.ImportCtx{Home: filepath.Join(t.TempDir(), "unused-home"), VaultSkillsDir: filepath.Join(t.TempDir(), "vault-skills")})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "overridetool" {
		t.Fatalf("want the skill found under CODEX_HOME, got %+v", skills)
	}
}

// TestCodexSkillsImportsAgentsSkillsUserSkill runs the fixture's
// .agents/skills/mycodexskill entry through the real engine
// (RunImport), asserting the end-to-end vault read-back: the skill
// lands with Tool codex, by: import:codex, review: draft.
func TestCodexSkillsImportsAgentsSkillsUserSkill(t *testing.T) {
	home, vaultSkillsDir := setupCodexHome(t)
	v := newCodexTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Codex{}}, ctx, importer.Options{Skills: true})
	if err != nil {
		t.Fatal(err)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]vault.Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}

	s, ok := byName["mycodexskill"]
	if !ok {
		t.Fatalf("want mycodexskill imported, got %+v", skills)
	}
	if s.By != "import:codex" {
		t.Fatalf("want by: import:codex, got %q", s.By)
	}
	if s.Review != "draft" {
		t.Fatalf("want review: draft, got %q", s.Review)
	}
	if !strings.Contains(s.Body, "Do the codex thing.") {
		t.Fatalf("bad skill body: %+v", s)
	}

	if _, ok := byName["review-agent"]; ok {
		t.Fatalf("the bundled .system skill must never import, got %+v", skills)
	}
	if _, ok := byName["loadout-dogfood"]; ok {
		t.Fatalf("the vault-owned skill must never import, got %+v", skills)
	}

	var imported bool
	for _, ref := range result.Imported {
		if ref.Name == "mycodexskill" && ref.Tool == "codex" {
			imported = true
		}
	}
	if !imported {
		t.Fatalf("want mycodexskill recorded as imported, got %+v", result.Imported)
	}
}

// TestCodexSkillsIncludesCodexNativeUserSkill checks the second,
// Codex-specific skills scope — <root>/skills/*/SKILL.md — imports a
// normal user skill placed there directly, alongside (not instead
// of) the generic .agents/skills scope.
func TestCodexSkillsIncludesCodexNativeUserSkill(t *testing.T) {
	home, vaultSkillsDir := setupCodexHome(t)
	src := importer.Codex{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["allowed-tool"] {
		t.Fatalf("want the .codex/skills user skill found, got %+v", skills)
	}
	if !names["mycodexskill"] {
		t.Fatalf("want the .agents/skills user skill also found, got %+v", skills)
	}
}

// TestCodexSkillsExcludesSystemSubtreeWhenMarkerPresent is the direct
// unit check for the vendor-exclusion rule: with the marker file
// present, nothing under .codex/skills/.system — however many skills
// it holds — is ever returned, while a sibling real user skill still
// is.
func TestCodexSkillsExcludesSystemSubtreeWhenMarkerPresent(t *testing.T) {
	home, vaultSkillsDir := setupCodexHome(t)
	src := importer.Codex{}

	skills, warnings, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "review-agent" {
			t.Fatalf("the whole .system subtree must be excluded when its marker is present, got %+v", skills)
		}
	}
	for _, w := range warnings {
		if strings.Contains(w.Path, ".system") {
			t.Fatalf(".system must be excluded silently, not warned about, got %+v", warnings)
		}
	}
}

// TestCodexSkillsExcludesVaultOwnedSkillInAgentsSkills checks the
// real symlink setupCodexHome injects: a skill symlinked into the
// vault's own skills dir must never import, matching Claude Code's
// own IsVaultOwnedSkill behavior.
func TestCodexSkillsExcludesVaultOwnedSkillInAgentsSkills(t *testing.T) {
	home, vaultSkillsDir := setupCodexHome(t)
	src := importer.Codex{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "loadout-dogfood" {
			t.Fatalf("a skill symlinked into the vault's own skills dir must be excluded, got %+v", skills)
		}
	}
}

// TestCodexMemoryStripsLoadoutBlockKeepsNativeText checks the
// fixture's AGENTS.md: the file wraps a loadout:begin/end block
// around native user text; only the native text must import.
func TestCodexMemoryStripsLoadoutBlockKeepsNativeText(t *testing.T) {
	home, _ := setupCodexHome(t)
	src := importer.Codex{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("want no warnings for a well-formed block, got %+v", warnings)
	}
	var found bool
	for _, f := range facts {
		if strings.Contains(f.Body, "SYNCED-MEMORY-BLOCK-MUST-NOT-IMPORT") {
			t.Fatalf("the loadout block content must never import, got %+v", f)
		}
		if strings.Contains(f.Body, "Keep this note about how I like codex to behave.") {
			found = true
			if f.Tool != "codex" {
				t.Fatalf("want Tool codex, got %q", f.Tool)
			}
		}
	}
	if !found {
		t.Fatalf("want the native AGENTS.md text to import as a fact, got %+v", facts)
	}
}

func TestCodexMemoryDamagedBlockSkipsWithWarning(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	root := filepath.Join(home, ".codex")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "<!-- loadout:begin -->\na\n<!-- loadout:end -->\nmiddle\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Codex{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("a damaged block must produce no fact, got %+v", facts)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning for the damaged block, got %+v", warnings)
	}
}

// TestCodexMemorySkipsOversizedAgentsFileWithWarning is the I2
// regression test: codex.go's AGENTS.md read had no size cap, unlike
// claudecode.go's CLAUDE.md read, which already skips a file over
// 4MiB (the same limit Claude Code itself applies) with a warning.
func TestCodexMemorySkipsOversizedAgentsFileWithWarning(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	root := filepath.Join(home, ".codex")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 4*1024*1024+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Codex{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("an oversized AGENTS.md must not import as one giant fact, got %+v", facts)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning for the oversized file, got %+v", warnings)
	}
}

func TestCodexMemoryOverrideTakesPrecedenceOverPlain(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	root := filepath.Join(home, ".codex")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Plain content, must not be used.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.override.md"), []byte("Override content wins.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Codex{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || !strings.Contains(facts[0].Body, "Override content wins.") {
		t.Fatalf("want the override file's content, got %+v", facts)
	}
	for _, f := range facts {
		if strings.Contains(f.Body, "must not be used") {
			t.Fatalf("the plain AGENTS.md must be ignored when an override exists, got %+v", facts)
		}
	}
}

func TestCodexMemoryProjectChainWalksGitRootToProjectDir(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "AGENTS.md"), []byte("Root project note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repoRoot, "pkg", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("Nested project note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Codex{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home, ProjectDir: sub})
	if err != nil {
		t.Fatal(err)
	}
	var sawRoot, sawNested bool
	for _, f := range facts {
		if strings.Contains(f.Body, "Root project note.") {
			sawRoot = true
		}
		if strings.Contains(f.Body, "Nested project note.") {
			sawNested = true
			if f.Type != "project" {
				t.Fatalf("want project-chain facts typed project, got %q", f.Type)
			}
		}
	}
	if !sawRoot || !sawNested {
		t.Fatalf("want facts from both the repo root and the nested project dir, got %+v", facts)
	}
}

// TestCodexNeverReadsConfigTomlOrAuthJson is the secret-safety test:
// the fixture's config.toml and auth.json each hold a distinct
// sentinel string. A full RunImport, with skills and memory both on,
// must never surface either sentinel anywhere in the result.
func TestCodexNeverReadsConfigTomlOrAuthJson(t *testing.T) {
	home, vaultSkillsDir := setupCodexHome(t)
	v := newCodexTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Codex{}}, ctx, importer.Options{Skills: true, Memory: true})
	if err != nil {
		t.Fatal(err)
	}

	sentinels := []string{"SENTINEL-DO-NOT-READ-CONFIG", "SENTINEL-DO-NOT-READ-AUTH"}

	assertNoSentinel := func(label, text string) {
		for _, s := range sentinels {
			if strings.Contains(text, s) {
				t.Fatalf("%s must never contain %q", label, s)
			}
		}
	}

	for _, w := range result.Warnings {
		assertNoSentinel("a warning", w.Tool+" "+w.Path+" "+w.Reason)
	}
	for _, w := range result.Skipped {
		assertNoSentinel("a skipped warning", w.Tool+" "+w.Path+" "+w.Reason)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		assertNoSentinel("a skill body", s.Body)
		assertNoSentinel("a skill description", s.Description)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		assertNoSentinel("a fact body", f.Body)
		assertNoSentinel("a fact description", f.Description)
	}

	// Belt and braces: config.toml and auth.json must still exist,
	// untouched, at their fixture paths — a passing test above could
	// otherwise be explained by the files having been deleted rather
	// than never opened.
	for _, name := range []string{"config.toml", "auth.json"} {
		if _, err := os.Stat(filepath.Join(home, ".codex", name)); err != nil {
			t.Fatalf("fixture file %s must still exist: %v", name, err)
		}
	}
}

// TestCrossSourceDedupImportsSharedAgentsSkillOnce is the
// cross-source dedup test: the same .agents/skills skill is visible
// to claude-code (via a ~/.claude/skills symlink into it) and to
// codex (directly, via .agents/skills). Running RunImport with both
// sources must import it once, not twice.
func TestCrossSourceDedupImportsSharedAgentsSkillOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	realSkillDir := filepath.Join(home, ".agents", "skills", "shared-skill")
	if err := os.MkdirAll(realSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: shared-skill\ndescription: seen by both claude-code and codex\n---\n\nShared body.\n"
	if err := os.WriteFile(filepath.Join(realSkillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeSkills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSkillDir, filepath.Join(claudeSkills, "shared-skill")); err != nil {
		t.Fatal(err)
	}

	vaultSkillsDir := filepath.Join(t.TempDir(), "vault-skills")
	if err := os.MkdirAll(vaultSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	v := newCodexTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.ClaudeCode{}, importer.Codex{}}, ctx, importer.Options{Skills: true})
	if err != nil {
		t.Fatal(err)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, s := range skills {
		if s.Name == "shared-skill" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want shared-skill written exactly once, got %d copies: %+v", count, skills)
	}

	var importedCount, dedupedCount int
	for _, ref := range result.Imported {
		if ref.Name == "shared-skill" {
			importedCount++
		}
	}
	for _, ref := range result.Deduped {
		if ref.Name == "shared-skill" {
			dedupedCount++
		}
	}
	if importedCount != 1 {
		t.Fatalf("want shared-skill imported exactly once, got %d: %+v", importedCount, result.Imported)
	}
	if dedupedCount != 1 {
		t.Fatalf("want shared-skill's second sighting recorded as deduped, got %d: %+v", dedupedCount, result.Deduped)
	}
}

// TestCrossSourceDedupPreservesSupportFilesAcrossSymlinkedSkill is the
// C1 regression test. A real skill folder holds SKILL.md plus a
// helper.sh support file. Claude Code sees it only through a
// symlinked skill folder (the common ~/.claude/skills/foo ->
// ~/.agents/skills/foo shape); Codex sees the very same folder
// directly, at its real path. Before the fix, the file walk behind a
// symlinked root collects zero support files, and the dedup key
// ignores CandidateSkill.Files entirely — so the two candidates hash
// as identical and dedup keeps whichever one was seen first,
// silently losing helper.sh whenever the symlinked (files-less) copy
// is seen before the real (files-carrying) one. Source order below
// puts Claude Code (the symlinked, files-less-before-the-fix side)
// first, to reproduce exactly that ordering.
func TestCrossSourceDedupPreservesSupportFilesAcrossSymlinkedSkill(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	realSkillDir := filepath.Join(home, ".agents", "skills", "foo")
	if err := os.MkdirAll(realSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: foo\ndescription: seen by both claude-code and codex\n---\n\nShared body.\n"
	if err := os.WriteFile(filepath.Join(realSkillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSkillDir, "helper.sh"), []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudeSkills := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSkillDir, filepath.Join(claudeSkills, "foo")); err != nil {
		t.Fatal(err)
	}

	vaultSkillsDir := filepath.Join(t.TempDir(), "vault-skills")
	if err := os.MkdirAll(vaultSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	v := newCodexTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	// Claude Code (symlinked root) first, Codex (real path) second —
	// the exact ordering that loses helper.sh before the fix.
	result, err := importer.RunImport(v, []importer.Source{importer.ClaudeCode{}, importer.Codex{}}, ctx, importer.Options{Skills: true})
	if err != nil {
		t.Fatal(err)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	var foo *vault.Skill
	count := 0
	for i := range skills {
		if skills[i].Name == "foo" {
			foo = &skills[i]
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want foo imported exactly once, got %d copies: %+v (imported=%+v deduped=%+v)", count, skills, result.Imported, result.Deduped)
	}
	if foo == nil {
		t.Fatalf("want a foo skill in the vault, got %+v", skills)
	}
	if _, err := os.Stat(filepath.Join(foo.Dir, "helper.sh")); err != nil {
		t.Fatalf("want helper.sh preserved in the imported skill (seen by one tool via a symlinked skill folder, another via the real path): %v", err)
	}
}
