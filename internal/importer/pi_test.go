package importer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

// setupPiHome copies testdata/pi-home into a fresh temp HOME (the
// fixture holds .pi/agent directly, the way a real home directory
// would) and adds a vault-owned symlinked skill under
// .pi/agent/skills the fixture cannot carry as a committed file. It
// returns the temp HOME and the injected vault skills dir.
func setupPiHome(t *testing.T) (home, vaultSkillsDir string) {
	t.Helper()
	home = t.TempDir()
	copyDir(t, "testdata/pi-home", home)

	vaultSkillsDir = filepath.Join(t.TempDir(), "vault-skills")
	realTarget := filepath.Join(vaultSkillsDir, "loadout-dogfood")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	piSkills := filepath.Join(home, ".pi", "agent", "skills")
	if err := os.Symlink(realTarget, filepath.Join(piSkills, "loadout-dogfood")); err != nil {
		t.Fatal(err)
	}

	return home, vaultSkillsDir
}

func TestPiNameIsPi(t *testing.T) {
	if got := (importer.Pi{}).Name(); got != "pi" {
		t.Fatalf("want pi, got %q", got)
	}
}

func TestPiDetectsHomeDir(t *testing.T) {
	home, _ := setupPiHome(t)
	src := importer.Pi{}

	present, root := src.Detect(importer.ImportCtx{Home: home})
	if !present {
		t.Fatal("want pi detected when ~/.pi/agent exists")
	}
	if root != filepath.Join(home, ".pi", "agent") {
		t.Fatalf("want root %s, got %s", filepath.Join(home, ".pi", "agent"), root)
	}
}

func TestPiDetectsAbsentHomeDir(t *testing.T) {
	src := importer.Pi{}

	present, _ := src.Detect(importer.ImportCtx{Home: t.TempDir()})
	if present {
		t.Fatal("want pi not detected when ~/.pi/agent is absent")
	}
}

// TestPiSkillsImportsAgentSkill runs the fixture's
// .pi/agent/skills/mypiskill entry through the real engine
// (RunImport), asserting the end-to-end vault read-back: the skill
// lands with by: import:pi, review: draft.
func TestPiSkillsImportsAgentSkill(t *testing.T) {
	home, vaultSkillsDir := setupPiHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Pi{}}, ctx, importer.Options{Skills: true})
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

	s, ok := byName["mypiskill"]
	if !ok {
		t.Fatalf("want mypiskill imported, got %+v", skills)
	}
	if s.By != "import:pi" {
		t.Fatalf("want by: import:pi, got %q", s.By)
	}
	if s.Review != "draft" {
		t.Fatalf("want review: draft, got %q", s.Review)
	}
	if !strings.Contains(s.Body, "Do the pi thing.") {
		t.Fatalf("bad skill body: %+v", s)
	}

	if _, ok := byName["loadout-dogfood"]; ok {
		t.Fatalf("the vault-owned skill must never import, got %+v", skills)
	}

	var imported bool
	for _, ref := range result.Imported {
		if ref.Name == "mypiskill" && ref.Tool == "pi" {
			imported = true
		}
	}
	if !imported {
		t.Fatalf("want mypiskill recorded as imported, got %+v", result.Imported)
	}
}

// TestPiSkillsExcludesVaultOwnedSkill is the direct unit check for
// the symlink setupPiHome injects: a skill symlinked into the vault's
// own skills dir must never import.
func TestPiSkillsExcludesVaultOwnedSkill(t *testing.T) {
	home, vaultSkillsDir := setupPiHome(t)
	src := importer.Pi{}

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

// TestPiMemoryImportsGlobalAgentsMdByDefault runs the fixture's
// AGENTS.md through the real engine, asserting the loadout block is
// stripped and only the native text is imported, with by:import:pi
// and review:draft, WITHOUT setting ProjectMemory — pi's global
// memory is always in scope.
func TestPiMemoryImportsGlobalAgentsMdByDefault(t *testing.T) {
	home, vaultSkillsDir := setupPiHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	_, err := importer.RunImport(v, []importer.Source{importer.Pi{}}, ctx, importer.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	var found *vault.Fact
	for i := range facts {
		if strings.Contains(facts[i].Body, "Keep this note about how I like pi to behave.") {
			found = &facts[i]
		}
		if strings.Contains(facts[i].Body, "SYNCED-MEMORY-BLOCK-MUST-NOT-IMPORT") {
			t.Fatalf("the loadout block content must never import, got %+v", facts[i])
		}
	}
	if found == nil {
		t.Fatalf("want the native AGENTS.md text imported as a fact, got %+v", facts)
	}
	if found.By != "import:pi" {
		t.Fatalf("want by: import:pi, got %q", found.By)
	}
	if found.Review != "draft" {
		t.Fatalf("want review: draft, got %q", found.Review)
	}
}

func TestPiMemoryDamagedBlockSkipsWithWarning(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "<!-- loadout:begin -->\na\n<!-- loadout:end -->\nmiddle\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Pi{}

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

func TestPiMemorySkipsOversizedFileWithWarning(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".pi", "agent")
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
	src := importer.Pi{}

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
