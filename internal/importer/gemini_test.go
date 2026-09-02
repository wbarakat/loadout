package importer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

// setupGeminiHome copies testdata/gemini-home into a fresh temp HOME
// and adds a vault-owned symlinked skill under .gemini/skills the
// fixture cannot carry as a committed file. It returns the temp HOME
// and the injected vault skills dir.
func setupGeminiHome(t *testing.T) (home, vaultSkillsDir string) {
	t.Helper()
	home = t.TempDir()
	copyDir(t, "testdata/gemini-home", home)

	vaultSkillsDir = filepath.Join(t.TempDir(), "vault-skills")
	realTarget := filepath.Join(vaultSkillsDir, "loadout-dogfood")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	geminiSkills := filepath.Join(home, ".gemini", "skills")
	if err := os.Symlink(realTarget, filepath.Join(geminiSkills, "loadout-dogfood")); err != nil {
		t.Fatal(err)
	}

	return home, vaultSkillsDir
}

func TestGeminiNameIsGemini(t *testing.T) {
	if got := (importer.Gemini{}).Name(); got != "gemini" {
		t.Fatalf("want gemini, got %q", got)
	}
}

func TestGeminiDetectsHomeDir(t *testing.T) {
	home, _ := setupGeminiHome(t)
	src := importer.Gemini{}

	present, root := src.Detect(importer.ImportCtx{Home: home})
	if !present {
		t.Fatal("want gemini detected when ~/.gemini exists")
	}
	if root != filepath.Join(home, ".gemini") {
		t.Fatalf("want root %s, got %s", filepath.Join(home, ".gemini"), root)
	}
}

func TestGeminiDetectsAbsentHomeDir(t *testing.T) {
	src := importer.Gemini{}

	present, _ := src.Detect(importer.ImportCtx{Home: t.TempDir()})
	if present {
		t.Fatal("want gemini not detected when ~/.gemini is absent")
	}
}

// TestGeminiSkillsImportsSkill runs the fixture's
// .gemini/skills/mygeminiskill entry through the real engine
// (RunImport), asserting the end-to-end vault read-back: by:
// import:gemini, review: draft.
func TestGeminiSkillsImportsSkill(t *testing.T) {
	home, vaultSkillsDir := setupGeminiHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Gemini{}}, ctx, importer.Options{Skills: true})
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

	s, ok := byName["mygeminiskill"]
	if !ok {
		t.Fatalf("want mygeminiskill imported, got %+v", skills)
	}
	if s.By != "import:gemini" {
		t.Fatalf("want by: import:gemini, got %q", s.By)
	}
	if s.Review != "draft" {
		t.Fatalf("want review: draft, got %q", s.Review)
	}
	if !strings.Contains(s.Body, "Do the gemini thing.") {
		t.Fatalf("bad skill body: %+v", s)
	}

	if _, ok := byName["loadout-dogfood"]; ok {
		t.Fatalf("the vault-owned skill must never import, got %+v", skills)
	}

	var imported bool
	for _, ref := range result.Imported {
		if ref.Name == "mygeminiskill" && ref.Tool == "gemini" {
			imported = true
		}
	}
	if !imported {
		t.Fatalf("want mygeminiskill recorded as imported, got %+v", result.Imported)
	}
}

func TestGeminiSkillsExcludesVaultOwnedSkill(t *testing.T) {
	home, vaultSkillsDir := setupGeminiHome(t)
	src := importer.Gemini{}

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

// TestGeminiMemoryImportsGlobalByDefaultNotProject checks RULING 2 for
// gemini: the global GEMINI.md imports by default (loadout block
// stripped), while a project GEMINI.md sitting in ctx.ProjectDir does
// NOT import unless ctx.ProjectMemory is set.
func TestGeminiMemoryImportsGlobalByDefaultNotProject(t *testing.T) {
	home, _ := setupGeminiHome(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "GEMINI.md"), []byte("Project gemini note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Gemini{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	var sawGlobal, sawProject, sawLoadoutBlock bool
	for _, f := range facts {
		if strings.Contains(f.Body, "Keep this note about how I like gemini to behave.") {
			sawGlobal = true
		}
		if strings.Contains(f.Body, "Project gemini note.") {
			sawProject = true
		}
		if strings.Contains(f.Body, "SYNCED-MEMORY-BLOCK-MUST-NOT-IMPORT") {
			sawLoadoutBlock = true
		}
	}
	if !sawGlobal {
		t.Fatalf("want the global GEMINI.md imported by default, got %+v", facts)
	}
	if sawLoadoutBlock {
		t.Fatalf("the loadout block content must never import, got %+v", facts)
	}
	if sawProject {
		t.Fatalf("the default must not import the project GEMINI.md, got %+v", facts)
	}
	var sawSkipNote bool
	for _, w := range warnings {
		if strings.Contains(w.Reason, "per-project memory") && strings.Contains(w.Reason, "--project-memory") {
			sawSkipNote = true
		}
	}
	if !sawSkipNote {
		t.Fatalf("want a warning naming --project-memory when a project source exists but is skipped, got %+v", warnings)
	}
}

// TestGeminiMemoryProjectMemoryTrueImportsProjectFile is the
// counterpart: with ProjectMemory: true, the project GEMINI.md must
// import too, typed "project".
func TestGeminiMemoryProjectMemoryTrueImportsProjectFile(t *testing.T) {
	home, _ := setupGeminiHome(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "GEMINI.md"), []byte("Project gemini note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Gemini{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home, ProjectDir: project, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range facts {
		if strings.Contains(f.Body, "Project gemini note.") {
			found = true
			if f.Type != "project" {
				t.Fatalf("want the project fact typed project, got %q", f.Type)
			}
		}
	}
	if !found {
		t.Fatalf("want the project GEMINI.md imported with --project-memory, got %+v", facts)
	}
}
