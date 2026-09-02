package importer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

// newClaudeCodeTestVault opens a fresh vault under a temp HOME, the
// same shape newEngineTestVault in engine_test.go uses.
func newClaudeCodeTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// copyDir copies every regular file under src into dst, recreating
// the directory structure. The checked-in testdata/claude-home
// fixture holds no symlinks — a test adds those at run time, since a
// symlink's target (the vault skills dir, a dangling path) only
// exists once the test has its own temp dirs.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, s, d)
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// setupClaudeHome copies testdata/claude-home into a fresh temp HOME
// and adds the three symlinks the fixture cannot carry as committed
// files: a skill pointing into an injected vault skills dir (must be
// excluded as Loadout's own), a skill pointing into the fixture's own
// plugins/ tree (must be excluded as vendor content), and a dangling
// skill link (must skip with a warning, not abort the run). It
// returns the temp HOME and the injected vault skills dir.
func setupClaudeHome(t *testing.T) (home, vaultSkillsDir string) {
	t.Helper()
	home = t.TempDir()
	root := filepath.Join(home, ".claude")
	copyDir(t, "testdata/claude-home", root)

	vaultSkillsDir = filepath.Join(t.TempDir(), "vault-skills")
	dogfoodTarget := filepath.Join(vaultSkillsDir, "loadout-dogfood")
	if err := os.MkdirAll(dogfoodTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dogfoodTarget, filepath.Join(root, "skills", "loadout-dogfood")); err != nil {
		t.Fatal(err)
	}

	pluginSkillDir := filepath.Join(root, "plugins", "marketplaces", "x", "plugins", "y", "skills", "z")
	if err := os.Symlink(pluginSkillDir, filepath.Join(root, "skills", "pluginskill")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(root, "skills", "does-not-exist"), filepath.Join(root, "skills", "dangling")); err != nil {
		t.Fatal(err)
	}

	return home, vaultSkillsDir
}

func TestClaudeCodeDetectsHomeDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := setupClaudeHome(t)
	src := importer.ClaudeCode{}

	present, root := src.Detect(importer.ImportCtx{Home: home})
	if !present {
		t.Fatal("want claude-code detected when ~/.claude exists")
	}
	if root != filepath.Join(home, ".claude") {
		t.Fatalf("want root %s, got %s", filepath.Join(home, ".claude"), root)
	}
}

func TestClaudeCodeDetectsAbsentHomeDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	src := importer.ClaudeCode{}

	present, _ := src.Detect(importer.ImportCtx{Home: t.TempDir()})
	if present {
		t.Fatal("want claude-code not detected when ~/.claude is absent")
	}
}

func TestClaudeCodeNameIsClaudeCode(t *testing.T) {
	if got := (importer.ClaudeCode{}).Name(); got != "claude-code" {
		t.Fatalf("want claude-code, got %q", got)
	}
}

// TestClaudeCodeFullImportViaEngine runs the fixture through the real
// engine (RunImport), the way the CLI will, and checks the vault items
// it produces: the one native skill, the one native CLAUDE.md fact,
// and the one auto-memory fact — each with by: import:claude-code and
// review: draft. It also checks the three exclusions and the dangling
// symlink all land as expected: silently for the two exclusions,
// skip+warn for the dangling link.
//
// ProjectMemory: true is set here because of FIX 4: the auto-memory
// fact this test checks (proj1-note) is per-project memory, which is
// now opt-in rather than imported by default. This test's own purpose
// is exercising the engine's full behavior end to end, not the
// default-scope question — see
// TestClaudeCodeMemoryDefaultExcludesProjectAndAutoMemory for that.
func TestClaudeCodeFullImportViaEngine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, vaultSkillsDir := setupClaudeHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir, ProjectMemory: true}
	src := importer.ClaudeCode{}

	result, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Skills: true, Memory: true})
	if err != nil {
		t.Fatal(err)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("want 1 skill (mytool only — the vault-owned, plugin, and dangling entries must not reach the vault), got %+v", skills)
	}
	s := skills[0]
	if s.Name != "mytool" || s.Description != "run mytool's own checks before a commit" {
		t.Fatalf("bad skill name/description: %+v", s)
	}
	if !strings.Contains(s.Body, "Run the mytool checks before every commit.") {
		t.Fatalf("bad skill body: %+v", s)
	}
	if s.By != "import:claude-code" {
		t.Fatalf("want by: import:claude-code, got %q", s.By)
	}
	if s.Review != "draft" {
		t.Fatalf("want review: draft, got %q", s.Review)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "reference.md")); err != nil {
		t.Fatalf("want the skill's supporting file copied alongside SKILL.md: %v", err)
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]vault.Fact{}
	for _, f := range facts {
		byName[f.Name] = f
	}

	claudeMD, ok := byName["claude-md"]
	if !ok {
		t.Fatalf("want a fact for the native CLAUDE.md content, got %+v", facts)
	}
	if !strings.Contains(claudeMD.Body, "Keep this note about how I like commits formatted.") {
		t.Fatalf("bad CLAUDE.md fact body: %+v", claudeMD)
	}
	if strings.Contains(claudeMD.Body, "loadout:begin") || strings.Contains(claudeMD.Body, "render/memory.md") {
		t.Fatalf("the loadout block must be stripped, got %+v", claudeMD)
	}
	if claudeMD.By != "import:claude-code" || claudeMD.Review != "draft" {
		t.Fatalf("bad CLAUDE.md fact provenance: %+v", claudeMD)
	}

	note, ok := byName["proj1-note"]
	if !ok {
		t.Fatalf("want a fact for the auto-memory topic file, got %+v", facts)
	}
	if note.Type != "project" {
		t.Fatalf("want the auto-memory fact's frontmatter type carried through as project, got %q", note.Type)
	}
	if !strings.Contains(note.Body, "The project's build lives under ./build") {
		t.Fatalf("bad auto-memory fact body: %+v", note)
	}
	if note.By != "import:claude-code" || note.Review != "draft" {
		t.Fatalf("bad auto-memory fact provenance: %+v", note)
	}

	if _, ok := byName["memory"]; ok {
		t.Fatalf("the MEMORY.md index must never import as a fact, got %+v", facts)
	}
	for name := range byName {
		if strings.EqualFold(name, "MEMORY") {
			t.Fatalf("the MEMORY.md index must never import as a fact, got %+v", facts)
		}
	}

	var danglingWarned bool
	for _, w := range result.Warnings {
		if strings.Contains(w.Path, "dangling") {
			danglingWarned = true
		}
	}
	if !danglingWarned {
		t.Fatalf("want a warning naming the dangling skill link, got %+v", result.Warnings)
	}
	if len(result.Imported) != 3 {
		t.Fatalf("want 3 imported items (1 skill + 2 facts), got %+v", result.Imported)
	}
}

func TestClaudeCodeSkillsExcludesVaultOwnedSkill(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, vaultSkillsDir := setupClaudeHome(t)
	src := importer.ClaudeCode{}

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

func TestClaudeCodeSkillsExcludesPluginSkill(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, vaultSkillsDir := setupClaudeHome(t)
	src := importer.ClaudeCode{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "z" {
			t.Fatalf("a skill resolving into <root>/plugins/ must be excluded as vendor content, got %+v", skills)
		}
	}
}

func TestClaudeCodeSkillsDanglingLinkSkipsWithWarningAndRunContinues(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, vaultSkillsDir := setupClaudeHome(t)
	src := importer.ClaudeCode{}

	skills, warnings, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Path, "dangling") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a warning naming the dangling skill link, got %+v", warnings)
	}
	// A dangling link must not abort the scan: the real skill is still
	// found.
	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["mytool"] {
		t.Fatalf("a dangling link elsewhere must not stop the real skill from being found, got %+v", skills)
	}
}

func TestClaudeCodeSkillsIncludesProjectSkills(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	skillDir := filepath.Join(project, ".claude", "skills", "projtool")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: projtool\ndescription: a project-scoped skill\n---\n\nDo the project thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

	skills, warnings, err := src.Skills(importer.ImportCtx{Home: home, ProjectDir: project, VaultSkillsDir: filepath.Join(t.TempDir(), "vault-skills")})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("want no warnings, got %+v", warnings)
	}
	if len(skills) != 1 || skills[0].Name != "projtool" {
		t.Fatalf("want the project-scoped skill found, got %+v", skills)
	}
}

func TestClaudeCodeSkillsSkipsSkillWithoutFrontmatter(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	dir := filepath.Join(root, "skills", "no-frontmatter")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("Just plain text, no frontmatter.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

	skills, warnings, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: filepath.Join(t.TempDir(), "vault-skills")})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("a SKILL.md without frontmatter must not import, got %+v", skills)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning for the missing frontmatter, got %+v", warnings)
	}
}

// TestClaudeCodeSkillsExcludesSupportFileSymlinkOutsideSkillFolder is
// the I3 regression test. A skill folder holds an ordinary support
// file plus creds.json, a symlink pointing OUTSIDE the skill folder
// at a file holding a sentinel secret (standing in for a real
// credential such as ~/.codex/auth.json). Before the fix,
// collectSkillFiles follows every file symlink inside a skill folder
// with no containment check, so the secret's content is copied into
// the vault as a support file under the skill's own name.
func TestClaudeCodeSkillsExcludesSupportFileSymlinkOutsideSkillFolder(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	skillDir := filepath.Join(root, "skills", "leaky")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: leaky\ndescription: has a symlinked support file\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "normal.txt"), []byte("an ordinary support file"), 0o644); err != nil {
		t.Fatal(err)
	}

	secretFile := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(secretFile, []byte("SENTINEL-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secretFile, filepath.Join(skillDir, "creds.json")); err != nil {
		t.Fatal(err)
	}

	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	if _, err := importer.RunImport(v, []importer.Source{importer.ClaudeCode{}}, ctx, importer.Options{Skills: true}); err != nil {
		t.Fatal(err)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	var leaky *vault.Skill
	for i := range skills {
		if skills[i].Name == "leaky" {
			leaky = &skills[i]
		}
	}
	if leaky == nil {
		t.Fatalf("want the leaky skill imported, got %+v", skills)
	}
	if _, err := os.Stat(filepath.Join(leaky.Dir, "creds.json")); err == nil {
		t.Fatal("a support file symlink pointing outside the skill folder must never be copied into the vault")
	}
	if _, err := os.Stat(filepath.Join(leaky.Dir, "normal.txt")); err != nil {
		t.Fatalf("an ordinary in-folder support file must still be copied: %v", err)
	}

	walkErr := filepath.WalkDir(v.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(data), "SENTINEL-SECRET") {
			t.Fatalf("the secret must appear nowhere in the vault, found in %s", path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
}

func TestClaudeCodeMemoryClaudeMDBlockOnlyProducesNoFact(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "<!-- loadout:begin -->\n@/Users/example/.loadout/render/memory.md\n<!-- loadout:end -->\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("a CLAUDE.md that is only the loadout block must produce no fact, got %+v", facts)
	}
	if len(warnings) != 0 {
		t.Fatalf("a block-only file is not a problem, want no warning, got %+v", warnings)
	}
}

func TestClaudeCodeMemoryDamagedBlockSkipsWithWarning(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "<!-- loadout:begin -->\na\n<!-- loadout:end -->\nmiddle\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

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

func TestClaudeCodeMemorySplitsTopLevelHeadingsIntoFacts(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "Intro text.\n\n## First topic\n\nFirst body.\n\n## Second topic\n\nSecond body.\n"
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 3 {
		t.Fatalf("want 3 facts (intro + 2 sections), got %+v", facts)
	}
	names := map[string]bool{}
	for _, f := range facts {
		names[f.Name] = true
	}
	for _, want := range []string{"claude-md-intro", "claude-md-first-topic", "claude-md-second-topic"} {
		if !names[want] {
			t.Fatalf("want a fact named %q, got %+v", want, facts)
		}
	}
}

func TestClaudeCodeMemoryUnstructuredFileIsOneFact(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("Just one plain paragraph, no headings.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Name != "claude-md" {
		t.Fatalf("want 1 fact named claude-md, got %+v", facts)
	}
}

func TestClaudeCodeMemorySkipsOversizedFileWithWarning(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 4*1024*1024+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("an oversized file must not import, got %+v", facts)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning for the oversized file, got %+v", warnings)
	}
}

// TestClaudeCodeMemoryProjectClaudeMDHierarchy sets ProjectMemory:
// true, since FIX 4 made the project CLAUDE.md hierarchy per-project
// memory — opt-in, not imported by default. See
// TestClaudeCodeMemoryDefaultExcludesProjectAndAutoMemory for the
// default-off case this test used to (accidentally) exercise.
func TestClaudeCodeMemoryProjectClaudeMDHierarchy(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("Root project note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "CLAUDE.md"), []byte("Nested project note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "CLAUDE.local.md"), []byte("Local-only note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home, ProjectDir: project, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]importer.CandidateFact{}
	for _, f := range facts {
		byName[f.Name] = f
	}
	for _, want := range []string{"claude-md-project", "claude-md-project-claude", "claude-md-local"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("want a fact named %q from the project CLAUDE.md hierarchy, got %+v", want, facts)
		}
	}
}

// TestClaudeCodeMemoryAutoMemoryCarriesTypeAndSkipsIndex sets
// ProjectMemory: true, since FIX 4 made the auto-memory vault
// per-project memory — opt-in, not scanned by default. See
// TestClaudeCodeMemoryDefaultExcludesProjectAndAutoMemory for the
// default-off case.
func TestClaudeCodeMemoryAutoMemoryCarriesTypeAndSkipsIndex(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := setupClaudeHome(t)
	src := importer.ClaudeCode{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	var note *importer.CandidateFact
	for i := range facts {
		if facts[i].Name == "proj1-note" {
			note = &facts[i]
		}
		if facts[i].Name == "memory" || strings.Contains(facts[i].Body, "[proj1 note](note.md)") {
			t.Fatalf("the MEMORY.md index file must never import as a fact, got %+v", facts[i])
		}
	}
	if note == nil {
		t.Fatalf("want a fact for the auto-memory topic file, got %+v", facts)
	}
	if note.Type != "project" {
		t.Fatalf("want the frontmatter type carried through unchanged as project, got %q", note.Type)
	}
}

func TestClaudeCodeConfigDirOverridesHome(t *testing.T) {
	configDir := t.TempDir()
	skillDir := filepath.Join(configDir, "skills", "overridetool")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: overridetool\ndescription: found via CLAUDE_CONFIG_DIR\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	src := importer.ClaudeCode{}

	present, root := src.Detect(importer.ImportCtx{Home: filepath.Join(t.TempDir(), "unused-home")})
	if !present || root != configDir {
		t.Fatalf("want CLAUDE_CONFIG_DIR to override Home, got present=%v root=%q", present, root)
	}
	skills, _, err := src.Skills(importer.ImportCtx{Home: filepath.Join(t.TempDir(), "unused-home"), VaultSkillsDir: filepath.Join(t.TempDir(), "vault-skills")})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "overridetool" {
		t.Fatalf("want the skill found under CLAUDE_CONFIG_DIR, got %+v", skills)
	}
}

// TestClaudeCodeSkillsExcludesVCSAndBuildDirsEndToEnd is the FIX 1
// regression test: a real "skill" was a symlink to a source REPO, so
// its .git (11MB) and .venv both got copied wholesale into the vault,
// and the vault's own nested .git broke its own git history. A skill
// folder holding SKILL.md, a real support file, a .git dir, a .venv
// dir, and a node_modules dir must import with only the real support
// file — none of the excluded dirs' content anywhere in the vault.
func TestClaudeCodeSkillsExcludesVCSAndBuildDirsEndToEnd(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")
	skillDir := filepath.Join(root, "skills", "reposkill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: reposkill\ndescription: a skill folder that is really a repo checkout\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "helper.sh"), []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillDir, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, ".git", "HEAD"), []byte("SENTINEL-GIT-HEAD-CONTENT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillDir, ".venv", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, ".venv", "lib", "somelib.py"), []byte("venv contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillDir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "node_modules", "x"), []byte("npm dep"), 0o644); err != nil {
		t.Fatal(err)
	}

	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	if _, err := importer.RunImport(v, []importer.Source{importer.ClaudeCode{}}, ctx, importer.Options{Skills: true}); err != nil {
		t.Fatal(err)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	var reposkill *vault.Skill
	for i := range skills {
		if skills[i].Name == "reposkill" {
			reposkill = &skills[i]
		}
	}
	if reposkill == nil {
		t.Fatalf("want reposkill imported, got %+v", skills)
	}
	if _, err := os.Stat(filepath.Join(reposkill.Dir, "helper.sh")); err != nil {
		t.Fatalf("want helper.sh copied alongside SKILL.md: %v", err)
	}
	for _, excluded := range []string{".git", ".venv", "node_modules"} {
		if _, err := os.Stat(filepath.Join(reposkill.Dir, excluded)); err == nil {
			t.Fatalf("a nested %s directory must never be copied into the vault", excluded)
		}
	}

	// Belt and braces: none of the excluded content's own bytes must
	// appear anywhere in the vault (the vault has its own top-level
	// .git from vault.Init, so this checks content, not directory
	// names, to avoid mistaking that for a violation).
	sentinels := []string{"venv contents", "npm dep", "SENTINEL-GIT-HEAD-CONTENT"}
	walkErr := filepath.WalkDir(v.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, s := range sentinels {
			if strings.Contains(string(data), s) {
				t.Fatalf("excluded dir content leaked into the vault at %s", path)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
}

// TestClaudeCodeSkillsSkipsOversizedSkillWithWarning is the FIX 2
// regression test: a 27MB "skill" (hallmark) imported wholesale. A
// skill whose total collected content — SKILL.md plus every support
// file, after FIX 1's exclusions — exceeds the per-skill cap must be
// skipped WHOLE, with a "too large" warning, never imported; a normal
// small skill must still import fine in the very same run.
func TestClaudeCodeSkillsSkipsOversizedSkillWithWarning(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".claude")

	bigDir := filepath.Join(root, "skills", "hallmark")
	if err := os.MkdirAll(bigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bigDir, "SKILL.md"), []byte("---\nname: hallmark\ndescription: a skill folder much too large to import\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 800*1024)
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(bigDir, fmt.Sprintf("part%d.bin", i)), chunk, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	smallDir := filepath.Join(root, "skills", "normal")
	if err := os.MkdirAll(smallDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smallDir, "SKILL.md"), []byte("---\nname: normal\ndescription: an ordinary small skill\n---\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	result, err := importer.RunImport(v, []importer.Source{importer.ClaudeCode{}}, ctx, importer.Options{Skills: true})
	if err != nil {
		t.Fatal(err)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]bool{}
	for _, s := range skills {
		byName[s.Name] = true
	}
	if byName["hallmark"] {
		t.Fatalf("an oversized skill must never import, got %+v", skills)
	}
	if !byName["normal"] {
		t.Fatalf("a normal small skill must still import in the same run, got %+v", skills)
	}

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w.Reason, "too large") && strings.Contains(w.Reason, "hallmark") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a 'too large' warning naming hallmark, got %+v", result.Warnings)
	}
}

// TestClaudeCodeMemoryDefaultExcludesProjectAndAutoMemory is the FIX
// 4 regression test: "loadout import" (memory) used to glob ALL
// projects' auto-memory plus project CLAUDE.md/AGENTS.md by default,
// flooding the vault with per-project work notes. The default must
// now import GLOBAL memory only (the user's own CLAUDE.md); the
// per-project CLAUDE.md hierarchy and the auto-memory vault must
// import only with ProjectMemory: true.
func TestClaudeCodeMemoryDefaultExcludesProjectAndAutoMemory(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := setupClaudeHome(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("Root project note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.ClaudeCode{}

	// Default: ProjectMemory is false (the zero value).
	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if f.Name == "proj1-note" {
			t.Fatalf("the default must not import per-project auto-memory, got %+v", facts)
		}
		if f.Name == "claude-md-project" {
			t.Fatalf("the default must not import the project CLAUDE.md, got %+v", facts)
		}
	}
	var sawGlobal bool
	for _, f := range facts {
		if f.Name == "claude-md" {
			sawGlobal = true
		}
	}
	if !sawGlobal {
		t.Fatalf("the default must still import the GLOBAL CLAUDE.md, got %+v", facts)
	}
	var sawSkipNote bool
	for _, w := range warnings {
		if strings.Contains(w.Reason, "per-project memory") && strings.Contains(w.Reason, "--project-memory") {
			sawSkipNote = true
		}
	}
	if !sawSkipNote {
		t.Fatalf("want a warning naming --project-memory when per-project sources exist but are skipped, got %+v", warnings)
	}

	// --project-memory: both the global and the per-project sources
	// import.
	facts, _, err = src.Memory(importer.ImportCtx{Home: home, ProjectDir: project, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]bool{}
	for _, f := range facts {
		byName[f.Name] = true
	}
	for _, want := range []string{"claude-md", "proj1-note", "claude-md-project"} {
		if !byName[want] {
			t.Fatalf("with --project-memory want a fact named %q, got %+v", want, facts)
		}
	}
}
