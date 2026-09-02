package importer_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

// setupCursorHome copies testdata/cursor-home into a fresh temp HOME
// and adds a vault-owned symlinked skill under .cursor/skills the
// fixture cannot carry as a committed file. It returns the temp HOME
// and the injected vault skills dir.
func setupCursorHome(t *testing.T) (home, vaultSkillsDir string) {
	t.Helper()
	home = t.TempDir()
	copyDir(t, "testdata/cursor-home", home)

	vaultSkillsDir = filepath.Join(t.TempDir(), "vault-skills")
	realTarget := filepath.Join(vaultSkillsDir, "loadout-dogfood")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	cursorSkills := filepath.Join(home, ".cursor", "skills")
	if err := os.Symlink(realTarget, filepath.Join(cursorSkills, "loadout-dogfood")); err != nil {
		t.Fatal(err)
	}

	return home, vaultSkillsDir
}

// cursorAppDataDirForTest mirrors cursor.go's own unexported
// cursorAppDataDir, so a test can place (and check for) a sentinel
// file at the exact path Detect looks at, without exporting that
// helper just for tests.
func cursorAppDataDirForTest(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Cursor")
	}
	return filepath.Join(home, ".config", "Cursor")
}

func TestCursorNameIsCursor(t *testing.T) {
	if got := (importer.Cursor{}).Name(); got != "cursor" {
		t.Fatalf("want cursor, got %q", got)
	}
}

func TestCursorDetectsCursorDir(t *testing.T) {
	home, _ := setupCursorHome(t)
	src := importer.Cursor{}

	present, root := src.Detect(importer.ImportCtx{Home: home})
	if !present {
		t.Fatal("want cursor detected when ~/.cursor exists")
	}
	if root != filepath.Join(home, ".cursor") {
		t.Fatalf("want root %s, got %s", filepath.Join(home, ".cursor"), root)
	}
}

// TestCursorDetectsAppDataDirWithNoCursorDir checks the OTHER half of
// Cursor's on-disk footprint: the Electron app's OS-native app-data
// directory, present even when the CLI-managed ~/.cursor tree is not.
func TestCursorDetectsAppDataDirWithNoCursorDir(t *testing.T) {
	home := t.TempDir()
	appData := cursorAppDataDirForTest(home)
	if err := os.MkdirAll(appData, 0o755); err != nil {
		t.Fatal(err)
	}
	src := importer.Cursor{}

	present, root := src.Detect(importer.ImportCtx{Home: home})
	if !present {
		t.Fatal("want cursor detected when only the app-data dir exists")
	}
	if root != appData {
		t.Fatalf("want root %s, got %s", appData, root)
	}
}

func TestCursorDetectsAbsentDirs(t *testing.T) {
	src := importer.Cursor{}

	present, _ := src.Detect(importer.ImportCtx{Home: t.TempDir()})
	if present {
		t.Fatal("want cursor not detected when neither ~/.cursor nor the app-data dir exists")
	}
}

// TestCursorSkillsImportsSkill runs the fixture's .cursor/skills/x
// entry through the real engine (RunImport), asserting the
// end-to-end vault read-back: by: import:cursor, review: draft.
func TestCursorSkillsImportsSkill(t *testing.T) {
	home, vaultSkillsDir := setupCursorHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Cursor{}}, ctx, importer.Options{Skills: true})
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

	s, ok := byName["x"]
	if !ok {
		t.Fatalf("want x imported, got %+v", skills)
	}
	if s.By != "import:cursor" {
		t.Fatalf("want by: import:cursor, got %q", s.By)
	}
	if s.Review != "draft" {
		t.Fatalf("want review: draft, got %q", s.Review)
	}
	if !strings.Contains(s.Body, "Do the cursor thing.") {
		t.Fatalf("bad skill body: %+v", s)
	}

	if _, ok := byName["loadout-dogfood"]; ok {
		t.Fatalf("the vault-owned skill must never import, got %+v", skills)
	}

	var imported bool
	for _, ref := range result.Imported {
		if ref.Name == "x" && ref.Tool == "cursor" {
			imported = true
		}
	}
	if !imported {
		t.Fatalf("want x recorded as imported, got %+v", result.Imported)
	}
}

// TestCursorSkillsIgnoresStaleSkillsCursorDir is the CRITICAL keying
// test: a stale ~/.cursor/skills-cursor/y directory with no SKILL.md
// inside must never import, and — because scanning keys off SKILL.md
// FILE presence, never a directory-name pattern — Skills() must never
// even look at "skills-cursor" as a scope in the first place (it is
// simply not one of the two directories Skills scans).
func TestCursorSkillsIgnoresStaleSkillsCursorDir(t *testing.T) {
	home, vaultSkillsDir := setupCursorHome(t)
	src := importer.Cursor{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "y" {
			t.Fatalf("a stale skills-cursor dir with no SKILL.md must never import, got %+v", skills)
		}
	}
}

func TestCursorSkillsExcludesVaultOwnedSkill(t *testing.T) {
	home, vaultSkillsDir := setupCursorHome(t)
	src := importer.Cursor{}

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

// TestCursorSkillsAlsoScansProjectDir checks the second skills scope:
// <project>/.cursor/skills, only when ctx.ProjectDir is set.
func TestCursorSkillsAlsoScansProjectDir(t *testing.T) {
	home, vaultSkillsDir := setupCursorHome(t)
	project := t.TempDir()
	projectSkillDir := filepath.Join(project, ".cursor", "skills", "myprojectskill")
	if err := os.MkdirAll(projectSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: myprojectskill\ndescription: a project-scoped cursor skill\n---\n\nDo the project thing.\n"
	if err := os.WriteFile(filepath.Join(projectSkillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Cursor{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range skills {
		if s.Name == "myprojectskill" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want the project .cursor/skills entry found, got %+v", skills)
	}
}

// TestCursorDefaultImportSkillsOnlyAlwaysWarnsUserRules is the DEFAULT
// behavior test: with ProjectMemory false, RunImport for both skills
// and memory imports the skill but NO memory fact at all (Cursor has
// no importable global memory), and ALWAYS emits the one User-Rules
// warning, since Cursor is present.
func TestCursorDefaultImportSkillsOnlyAlwaysWarnsUserRules(t *testing.T) {
	home, vaultSkillsDir := setupCursorHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Cursor{}}, ctx, importer.Options{Skills: true, Memory: true})
	if err != nil {
		t.Fatal(err)
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("want no memory imported by default, got %+v", facts)
	}

	var sawUserRulesWarning bool
	for _, w := range result.Warnings {
		if strings.Contains(w.Reason, "User Rules") && strings.Contains(w.Reason, "internal database") {
			sawUserRulesWarning = true
		}
	}
	if !sawUserRulesWarning {
		t.Fatalf("want the User Rules warning always emitted when cursor is present, got %+v", result.Warnings)
	}
}

// TestCursorMemoryDefaultProjectMemoryFalseImportsNothing is the
// direct unit check: calling Memory directly with ProjectMemory false
// (even with ProjectDir set and real .cursor/rules content present)
// must import no facts at all.
func TestCursorMemoryDefaultProjectMemoryFalseImportsNothing(t *testing.T) {
	project := t.TempDir()
	rulesDir := filepath.Join(project, ".cursor", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: a project rule\nglobs: *.ts\n---\n\nUse the internal RPC pattern.\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "a.mdc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Cursor{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: t.TempDir(), ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("want no facts when ProjectMemory is false, got %+v", facts)
	}
}

// TestCursorMemoryRulesGlobsAndAlwaysApply is the core .mdc test: a
// rule scoped by globs imports type:project with the glob pattern as
// plain text in the body; a rule with alwaysApply: true imports
// type:user.
func TestCursorMemoryRulesGlobsAndAlwaysApply(t *testing.T) {
	project := t.TempDir()
	rulesDir := filepath.Join(project, ".cursor", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aContent := "---\ndescription: RPC service boilerplate\nglobs: *.controller.ts\nalwaysApply: false\n---\n\nUse the internal RPC pattern for every controller.\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "a.mdc"), []byte(aContent), 0o644); err != nil {
		t.Fatal(err)
	}
	bContent := "---\ndescription: always follow this\nalwaysApply: true\n---\n\nAlways write tests first.\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "b.mdc"), []byte(bContent), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Cursor{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: t.TempDir(), ProjectDir: project, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("want no warnings for well-formed rules, got %+v", warnings)
	}

	var a, b *importer.CandidateFact
	for i := range facts {
		f := facts[i]
		if f.Name == "a" {
			a = &f
		}
		if f.Name == "b" {
			b = &f
		}
	}
	if a == nil {
		t.Fatalf("want a fact named a from a.mdc, got %+v", facts)
	}
	if a.Type != "project" {
		t.Fatalf("want a.mdc typed project (globs set), got %q", a.Type)
	}
	if !strings.Contains(a.Body, "internal RPC pattern") {
		t.Fatalf("want a.mdc's rule body imported, got %q", a.Body)
	}
	if !strings.Contains(a.Body, "*.controller.ts") {
		t.Fatalf("want the glob pattern present as plain text in the body, got %q", a.Body)
	}

	if b == nil {
		t.Fatalf("want a fact named b from b.mdc, got %+v", facts)
	}
	if b.Type != "user" {
		t.Fatalf("want b.mdc typed user (alwaysApply: true), got %q", b.Type)
	}
	if !strings.Contains(b.Body, "Always write tests first.") {
		t.Fatalf("want b.mdc's rule body imported, got %q", b.Body)
	}
}

// TestCursorMemoryMalformedMdcSkipsWithWarningRunContinues checks that
// one malformed .mdc file (an unterminated frontmatter fence) is
// skipped with a warning, WITHOUT aborting the rest of the scan: a
// second, well-formed .mdc file in the same directory must still
// import.
func TestCursorMemoryMalformedMdcSkipsWithWarningRunContinues(t *testing.T) {
	project := t.TempDir()
	rulesDir := filepath.Join(project, ".cursor", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	malformed := "---\ndescription: never closed\nglobs: *.go\n\nThis frontmatter fence never closes.\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "broken.mdc"), []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}
	good := "---\ndescription: a fine rule\nglobs: *.md\n---\n\nWrite clear docs.\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "good.mdc"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Cursor{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: t.TempDir(), ProjectDir: project, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	var sawGood bool
	for _, f := range facts {
		if f.Name == "broken" {
			t.Fatalf("a malformed .mdc must never import as a fact, got %+v", f)
		}
		if f.Name == "good" {
			sawGood = true
		}
	}
	if !sawGood {
		t.Fatalf("want the well-formed .mdc still imported despite the malformed one, got %+v", facts)
	}
	if len(warnings) != 1 {
		t.Fatalf("want exactly 1 warning for the malformed .mdc, got %+v", warnings)
	}
}

// TestCursorMemoryCursorrulesFileImportsOneFact checks the plain-file
// shape of the legacy .cursorrules convention.
func TestCursorMemoryCursorrulesFileImportsOneFact(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".cursorrules"), []byte("Keep functions short.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Cursor{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: t.TempDir(), ProjectDir: project, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range facts {
		if strings.Contains(f.Body, "Keep functions short.") {
			found = true
			if f.Type != "project" {
				t.Fatalf("want the .cursorrules fact typed project, got %q", f.Type)
			}
		}
	}
	if !found {
		t.Fatalf("want .cursorrules imported as one fact, got %+v", facts)
	}
}

// TestCursorMemoryCursorrulesDirectoryImportsEachFile checks the
// real-world non-standard-reuse shape: a .cursorrules DIRECTORY
// (confirmed on-disk in the source map at
// ~/Desktop/Projects/voltly/.cursorrules) holding loose docs, each of
// which must import as its own fact.
func TestCursorMemoryCursorrulesDirectoryImportsEachFile(t *testing.T) {
	project := t.TempDir()
	cursorrulesDir := filepath.Join(project, ".cursorrules")
	if err := os.MkdirAll(cursorrulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cursorrulesDir, "sentry.md"), []byte("Always wrap handlers in Sentry.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Cursor{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: t.TempDir(), ProjectDir: project, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range facts {
		if strings.Contains(f.Body, "Always wrap handlers in Sentry.") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want sentry.md, inside the .cursorrules directory, imported as its own fact, got %+v", facts)
	}
}

// TestCursorNeverReadsAppDataSQLite is the secret-safety test: a
// sentinel "state.vscdb" file sits where Cursor's real, undocumented
// SQLite database would live. Running Skills, Memory, and Detect must
// never surface its content anywhere — proving this source never
// opens it.
func TestCursorNeverReadsAppDataSQLite(t *testing.T) {
	home, vaultSkillsDir := setupCursorHome(t)
	appData := cursorAppDataDirForTest(home)
	globalStorage := filepath.Join(appData, "User", "globalStorage")
	if err := os.MkdirAll(globalStorage, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := "STATE-VSCDB-SENTINEL-MUST-NEVER-BE-READ"
	dbPath := filepath.Join(globalStorage, "state.vscdb")
	if err := os.WriteFile(dbPath, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make it unreadable too, as a second line of defense: if this
	// source ever tried to open it, the run would fail loudly rather
	// than silently succeed with content that happens not to get
	// asserted on.
	if err := os.Chmod(dbPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0o644) })

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".cursorrules"), []byte("Some project rule.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := importer.Cursor{}
	ctx := importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir, ProjectDir: project, ProjectMemory: true}

	present, _ := src.Detect(ctx)
	if !present {
		t.Fatal("want cursor still detected")
	}

	skills, skillWarnings, err := src.Skills(ctx)
	if err != nil {
		t.Fatal(err)
	}
	facts, memWarnings, err := src.Memory(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range skills {
		if strings.Contains(s.Body, sentinel) {
			t.Fatalf("the sqlite sentinel content leaked into a skill body, got %+v", s)
		}
	}
	for _, f := range facts {
		if strings.Contains(f.Body, sentinel) {
			t.Fatalf("the sqlite sentinel content leaked into a fact body, got %+v", f)
		}
	}
	for _, w := range append(skillWarnings, memWarnings...) {
		if strings.Contains(w.Reason, sentinel) || strings.Contains(w.Path, dbPath) {
			t.Fatalf("the sqlite file must never even be named in a warning, got %+v", w)
		}
	}
}
