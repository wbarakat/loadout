package importer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

// setupDroidHome copies testdata/droid-home into a fresh temp HOME
// and adds a vault-owned symlinked skill under .factory/skills the
// fixture cannot carry as a committed file. It returns the temp HOME
// and the injected vault skills dir.
func setupDroidHome(t *testing.T) (home, vaultSkillsDir string) {
	t.Helper()
	home = t.TempDir()
	copyDir(t, "testdata/droid-home", home)

	vaultSkillsDir = filepath.Join(t.TempDir(), "vault-skills")
	realTarget := filepath.Join(vaultSkillsDir, "loadout-dogfood")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	droidSkills := filepath.Join(home, ".factory", "skills")
	if err := os.Symlink(realTarget, filepath.Join(droidSkills, "loadout-dogfood")); err != nil {
		t.Fatal(err)
	}

	return home, vaultSkillsDir
}

func TestDroidNameIsDroid(t *testing.T) {
	if got := (importer.Droid{}).Name(); got != "droid" {
		t.Fatalf("want droid, got %q", got)
	}
}

func TestDroidDetectsHomeDir(t *testing.T) {
	home, _ := setupDroidHome(t)
	src := importer.Droid{}

	present, root := src.Detect(importer.ImportCtx{Home: home})
	if !present {
		t.Fatal("want droid detected when ~/.factory exists")
	}
	if root != filepath.Join(home, ".factory") {
		t.Fatalf("want root %s, got %s", filepath.Join(home, ".factory"), root)
	}
}

func TestDroidDetectsAbsentHomeDir(t *testing.T) {
	src := importer.Droid{}

	present, _ := src.Detect(importer.ImportCtx{Home: t.TempDir()})
	if present {
		t.Fatal("want droid not detected when ~/.factory is absent")
	}
}

// TestDroidSkillsImportsFactorySkillDropsExtraFrontmatter runs the
// fixture's .factory/skills/mydroidskill entry — whose SKILL.md
// carries Droid's richer frontmatter (allowed-tools, enabled,
// user-invocable, disable-model-invocation, license, compatibility,
// version, metadata) — through the real engine, asserting the vault
// read-back keeps only name/description/body: by:import:droid,
// review:draft, and none of the extra keys leak into the written
// file.
func TestDroidSkillsImportsFactorySkillDropsExtraFrontmatter(t *testing.T) {
	home, vaultSkillsDir := setupDroidHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Droid{}}, ctx, importer.Options{Skills: true})
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

	s, ok := byName["mydroidskill"]
	if !ok {
		t.Fatalf("want mydroidskill imported, got %+v", skills)
	}
	if s.By != "import:droid" {
		t.Fatalf("want by: import:droid, got %q", s.By)
	}
	if s.Review != "draft" {
		t.Fatalf("want review: draft, got %q", s.Review)
	}
	if !strings.Contains(s.Body, "Do the droid thing.") {
		t.Fatalf("bad skill body: %+v", s)
	}

	raw, err := os.ReadFile(filepath.Join(s.Dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{"allowed-tools", "enabled", "user-invocable", "disable-model-invocation", "license", "compatibility", "version", "metadata"} {
		if strings.Contains(string(raw), extra) {
			t.Fatalf("want Droid's extra frontmatter field %q dropped, got:\n%s", extra, raw)
		}
	}

	if _, ok := byName["loadout-dogfood"]; ok {
		t.Fatalf("the vault-owned skill must never import, got %+v", skills)
	}

	var imported bool
	for _, ref := range result.Imported {
		if ref.Name == "mydroidskill" && ref.Tool == "droid" {
			imported = true
		}
	}
	if !imported {
		t.Fatalf("want mydroidskill recorded as imported, got %+v", result.Imported)
	}
}

func TestDroidSkillsExcludesVaultOwnedSkill(t *testing.T) {
	home, vaultSkillsDir := setupDroidHome(t)
	src := importer.Droid{}

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

// TestDroidSkillsAlsoScansGenericAgentsSkills checks Droid's other
// skill scope: the generic ~/.agents/skills directory (source map
// §6), not just ~/.factory/skills.
func TestDroidSkillsAlsoScansGenericAgentsSkills(t *testing.T) {
	home, vaultSkillsDir := setupDroidHome(t)
	agentsSkillDir := filepath.Join(home, ".agents", "skills", "sharedagentskill")
	if err := os.MkdirAll(agentsSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: sharedagentskill\ndescription: seen via the generic .agents/skills scope\n---\n\nShared agent body.\n"
	if err := os.WriteFile(filepath.Join(agentsSkillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Droid{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range skills {
		if s.Name == "sharedagentskill" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want the generic .agents/skills entry found, got %+v", skills)
	}
}

// TestDroidSkillsScansFactoryCompatScopes is the MINOR E regression
// test (source map §6): Droid also reads <repo>/.factory/skills, one
// of the compat scopes Factory's own docs list alongside the generic
// .agents/skills convention.
func TestDroidSkillsScansFactoryCompatScopes(t *testing.T) {
	home, vaultSkillsDir := setupDroidHome(t)
	project := t.TempDir()
	skillDir := filepath.Join(project, ".factory", "skills", "projectfactoryskill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: projectfactoryskill\ndescription: seen via the project's own .factory/skills scope\n---\n\nDo the project factory thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Droid{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range skills {
		if s.Name == "projectfactoryskill" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want the project .factory/skills entry found, got %+v", skills)
	}
}

// TestDroidMemoryImportsGlobalByDefaultNotProject checks RULING 2 for
// droid: the global AGENTS.md imports by default (loadout block
// stripped), while a project AGENTS.md chain does NOT import unless
// ctx.ProjectMemory is set.
func TestDroidMemoryImportsGlobalByDefaultNotProject(t *testing.T) {
	home, _ := setupDroidHome(t)

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
	src := importer.Droid{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home, ProjectDir: sub})
	if err != nil {
		t.Fatal(err)
	}
	var sawGlobal, sawRoot, sawNested, sawLoadoutBlock bool
	for _, f := range facts {
		if strings.Contains(f.Body, "Keep this note about how I like droid to behave.") {
			sawGlobal = true
		}
		if strings.Contains(f.Body, "Root project note.") {
			sawRoot = true
		}
		if strings.Contains(f.Body, "Nested project note.") {
			sawNested = true
		}
		if strings.Contains(f.Body, "SYNCED-MEMORY-BLOCK-MUST-NOT-IMPORT") {
			sawLoadoutBlock = true
		}
	}
	if !sawGlobal {
		t.Fatalf("want the global AGENTS.md imported by default, got %+v", facts)
	}
	if sawLoadoutBlock {
		t.Fatalf("the loadout block content must never import, got %+v", facts)
	}
	if sawRoot || sawNested {
		t.Fatalf("the default must not import the project AGENTS.md chain, got %+v", facts)
	}
	var sawSkipNote bool
	for _, w := range warnings {
		if strings.Contains(w.Reason, "per-project memory") && strings.Contains(w.Reason, "--project-memory") {
			sawSkipNote = true
		}
	}
	if !sawSkipNote {
		t.Fatalf("want a warning naming --project-memory when project sources exist but are skipped, got %+v", warnings)
	}

	facts, _, err = src.Memory(importer.ImportCtx{Home: home, ProjectDir: sub, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	sawRoot, sawNested = false, false
	for _, f := range facts {
		if strings.Contains(f.Body, "Root project note.") {
			sawRoot = true
		}
		if strings.Contains(f.Body, "Nested project note.") {
			sawNested = true
			if f.Type != "project" {
				t.Fatalf("want the nested project fact typed project, got %q", f.Type)
			}
		}
	}
	if !sawRoot || !sawNested {
		t.Fatalf("with --project-memory want facts from both the repo root and the nested project dir, got %+v", facts)
	}
}

// TestCrossToolDedupDroidAndCodexImportSharedProjectAgentsMdOnce is
// the IMPORTANT C regression test: under --project-memory, Codex and
// Droid both read the SAME project AGENTS.md at the repo root,
// producing byte-identical bodies. Before the fix, they named the
// resulting fact differently ("agents-project" for Droid,
// "agents-md-project" for Codex — see codex.go's readCodexAgentsFile),
// so name+content-hash dedup could not collapse the duplicate and the
// same content imported twice. After the fix, both tools name it
// "agents-md-project", and the content imports exactly once.
func TestCrossToolDedupDroidAndCodexImportSharedProjectAgentsMdOnce(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".factory"), 0o755); err != nil {
		t.Fatal(err)
	}

	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "AGENTS.md"), []byte("Shared project note seen by both tools.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, ProjectDir: repoRoot, ProjectMemory: true}

	result, err := importer.RunImport(v, []importer.Source{importer.Droid{}, importer.Codex{}}, ctx, importer.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, f := range facts {
		if strings.Contains(f.Body, "Shared project note seen by both tools.") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want the shared project AGENTS.md content written exactly once, got %d copies: %+v", count, facts)
	}

	var importedCount, dedupedCount int
	for _, ref := range result.Imported {
		if ref.Name == "agents-md-project" {
			importedCount++
		}
	}
	for _, ref := range result.Deduped {
		if ref.Name == "agents-md-project" {
			dedupedCount++
		}
	}
	if importedCount != 1 {
		t.Fatalf("want agents-md-project imported exactly once, got %d: %+v", importedCount, result.Imported)
	}
	if dedupedCount != 1 {
		t.Fatalf("want the second sighting recorded as deduped, got %d: %+v", dedupedCount, result.Deduped)
	}
}

// TestCrossToolDedupDroidAndCodexImportSharedAgentsSkillOnce is the
// cross-tool dedup test: the same .agents/skills/shared-skill/
// SKILL.md is visible to both Droid (its generic scan) and Codex
// (its own generic scan of the same directory). Running RunImport
// with both sources must import it once, by name+content hash, not
// twice.
func TestCrossToolDedupDroidAndCodexImportSharedAgentsSkillOnce(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".factory"), 0o755); err != nil {
		t.Fatal(err)
	}
	sharedSkillDir := filepath.Join(home, ".agents", "skills", "shared-skill")
	if err := os.MkdirAll(sharedSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: shared-skill\ndescription: seen by both droid and codex\n---\n\nShared body.\n"
	if err := os.WriteFile(filepath.Join(sharedSkillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	vaultSkillsDir := filepath.Join(t.TempDir(), "vault-skills")
	if err := os.MkdirAll(vaultSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Droid{}, importer.Codex{}}, ctx, importer.Options{Skills: true})
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
