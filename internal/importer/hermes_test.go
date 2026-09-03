package importer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

// setupHermesHome copies testdata/hermes-home into a fresh temp HOME
// (the fixture holds .hermes directly, the way a real home directory
// would) and adds a vault-owned symlinked skill under .hermes/skills
// the fixture cannot carry as a committed file. It returns the temp
// HOME and the injected vault skills dir.
func setupHermesHome(t *testing.T) (home, vaultSkillsDir string) {
	t.Helper()
	home = t.TempDir()
	copyDir(t, "testdata/hermes-home", home)

	vaultSkillsDir = filepath.Join(t.TempDir(), "vault-skills")
	realTarget := filepath.Join(vaultSkillsDir, "loadout-dogfood")
	if err := os.MkdirAll(realTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	hermesSkills := filepath.Join(home, ".hermes", "skills")
	if err := os.Symlink(realTarget, filepath.Join(hermesSkills, "loadout-dogfood")); err != nil {
		t.Fatal(err)
	}

	return home, vaultSkillsDir
}

func TestHermesNameIsHermes(t *testing.T) {
	if got := (importer.Hermes{}).Name(); got != "hermes" {
		t.Fatalf("want hermes, got %q", got)
	}
}

func TestHermesDetectsHomeDir(t *testing.T) {
	home, _ := setupHermesHome(t)
	src := importer.Hermes{}

	present, root := src.Detect(importer.ImportCtx{Home: home})
	if !present {
		t.Fatal("want hermes detected when ~/.hermes exists")
	}
	if root != filepath.Join(home, ".hermes") {
		t.Fatalf("want root %s, got %s", filepath.Join(home, ".hermes"), root)
	}
}

func TestHermesDetectsAbsentHomeDir(t *testing.T) {
	src := importer.Hermes{}

	present, _ := src.Detect(importer.ImportCtx{Home: t.TempDir()})
	if present {
		t.Fatal("want hermes not detected when ~/.hermes is absent")
	}
}

// TestHermesSkillsImportsUserSkill runs the fixture's
// .hermes/skills/mine entry through the real engine (RunImport),
// asserting the end-to-end vault read-back: the skill lands with by:
// import:hermes, review: draft.
func TestHermesSkillsImportsUserSkill(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	result, err := importer.RunImport(v, []importer.Source{importer.Hermes{}}, ctx, importer.Options{Skills: true})
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

	s, ok := byName["mine"]
	if !ok {
		t.Fatalf("want mine imported, got %+v", skills)
	}
	if s.By != "import:hermes" {
		t.Fatalf("want by: import:hermes, got %q", s.By)
	}
	if s.Review != "draft" {
		t.Fatalf("want review: draft, got %q", s.Review)
	}
	if !strings.Contains(s.Body, "Do the hermes thing.") {
		t.Fatalf("bad skill body: %+v", s)
	}

	var imported bool
	for _, ref := range result.Imported {
		if ref.Name == "mine" && ref.Tool == "hermes" {
			imported = true
		}
	}
	if !imported {
		t.Fatalf("want mine recorded as imported, got %+v", result.Imported)
	}
}

// TestHermesSkillsExcludesBundledManifestSkill checks the vendor-
// exclusion signal itself: .bundled_manifest names "dogfood", so the
// dogfood skill directory must never come back as a candidate.
func TestHermesSkillsExcludesBundledManifestSkill(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	src := importer.Hermes{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "dogfood" {
			t.Fatalf("a skill listed in .bundled_manifest must be excluded, got %+v", skills)
		}
	}
}

// TestHermesSkillsExcludesArchiveSkill checks that the whole
// .archive subtree of retired bundled skills is excluded, not just
// names in .bundled_manifest.
func TestHermesSkillsExcludesArchiveSkill(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	src := importer.Hermes{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "old" {
			t.Fatalf("a skill under .archive must be excluded, got %+v", skills)
		}
	}
}

// TestHermesSkillsExcludesCategoryGroupingDir proves a Hermes category
// grouping directory — one with no SKILL.md of its own, only skill
// subfolders (skills/creative/comfyui/SKILL.md) — is excluded whole and
// silently, never scanned as a skill and never warned about, while a
// real top-level user skill beside it still imports. The bundled
// library is organized this way and the manifest names only part of it,
// so descending would flood the vault with vendor drafts.
func TestHermesSkillsExcludesCategoryGroupingDir(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".hermes", "skills")

	writeHermesSkill(t, filepath.Join(skillsDir, "mine"), "mine", "a real user skill")
	// A populated category grouping: no SKILL.md at creative/, a real
	// skill one level down at creative/comfyui/. comfyui is NOT in any
	// manifest, so descending would import it as an unwanted draft.
	writeHermesSkill(t, filepath.Join(skillsDir, "creative", "comfyui"), "comfyui", "a bundled vendor skill")
	if err := os.WriteFile(filepath.Join(skillsDir, "creative", "DESCRIPTION.md"), []byte("the creative category\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An emptied category shell: only a DESCRIPTION.md, its skills long
	// archived. It must not warn "no readable SKILL.md" either.
	if err := os.MkdirAll(filepath.Join(skillsDir, "gifs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "gifs", "DESCRIPTION.md"), []byte("the gifs category\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, warnings, err := (importer.Hermes{}).Skills(importer.ImportCtx{Home: home, VaultSkillsDir: filepath.Join(t.TempDir(), "v")})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "comfyui" {
			t.Fatalf("a nested bundled category skill must not import, got %+v", skills)
		}
	}
	var sawMine bool
	for _, s := range skills {
		if s.Name == "mine" {
			sawMine = true
		}
	}
	if !sawMine {
		t.Fatalf("a real top-level user skill must still import, got %+v", skills)
	}
	for _, w := range warnings {
		if strings.Contains(w.Path, "creative") || strings.Contains(w.Path, "gifs") {
			t.Fatalf("a category grouping dir must be excluded silently, got warning %+v", w)
		}
	}
}

// writeHermesSkill writes a minimal SKILL.md skill folder at dir.
func writeHermesSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n\nDo the " + name + " thing.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHermesSkillsExcludesVaultOwnedSkill is the direct unit check
// for the symlink setupHermesHome injects: a skill symlinked into the
// vault's own skills dir must never import.
func TestHermesSkillsExcludesVaultOwnedSkill(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	src := importer.Hermes{}

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

// TestHermesSkillsBundledManifestAbsentImportsAll checks the
// graceful-degradation rule: with no .bundled_manifest at all, every
// top-level skill imports — there is nothing named to exclude.
func TestHermesSkillsBundledManifestAbsentImportsAll(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".hermes", "skills", "onlyskill")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: onlyskill\ndescription: no manifest here\n---\n\nDo the only thing.\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Hermes{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range skills {
		if s.Name == "onlyskill" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want onlyskill imported with no .bundled_manifest present, got %+v", skills)
	}
}

// TestHermesSkillsMalformedManifestFailsClosed is the IMPORTANT B
// regression test: a .bundled_manifest that EXISTS but parses to zero
// usable skill names (garbage content, no valid "name:hash" line)
// must fail CLOSED — no top-level skill imports at all — rather than
// degrade to "no manifest" and sweep in every bundled vendor skill
// alongside the user's own.
func TestHermesSkillsMalformedManifestFailsClosed(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".hermes", "skills")
	mineDir := filepath.Join(skillsDir, "mine")
	if err := os.MkdirAll(mineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: mine\ndescription: the user's own skill\n---\n\nDo the hermes thing.\n"
	if err := os.WriteFile(filepath.Join(mineDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, ".bundled_manifest"), []byte("{{garbage}}\nnot a valid line either\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Hermes{}

	skills, warnings, err := src.Skills(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("a present-but-malformed manifest must fail closed, importing NO top-level skills, got %+v", skills)
	}
	var sawWarning bool
	for _, w := range warnings {
		if strings.Contains(w.Reason, "present but unreadable") && strings.Contains(w.Reason, "skipping top-level skills") {
			sawWarning = true
		}
	}
	if !sawWarning {
		t.Fatalf("want the fail-closed warning, got %+v", warnings)
	}
}

// TestHermesSkillsWellFormedManifestExcludesBundledImportsUser is the
// companion IMPORTANT B check: a well-formed manifest (real
// "name:hash" entries) must behave exactly as before — the named
// bundled skill excluded, an unrelated user skill still imported, no
// fail-closed warning.
func TestHermesSkillsWellFormedManifestExcludesBundledImportsUser(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".hermes", "skills")
	mineDir := filepath.Join(skillsDir, "mine")
	if err := os.MkdirAll(mineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: mine\ndescription: the user's own skill\n---\n\nDo the hermes thing.\n"
	if err := os.WriteFile(filepath.Join(mineDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	bundledDir := filepath.Join(skillsDir, "bundledone")
	if err := os.MkdirAll(bundledDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundledDir, "SKILL.md"), []byte("---\nname: bundledone\ndescription: vendor skill\n---\n\nVendor body.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, ".bundled_manifest"), []byte("bundledone:ae6e92c2cd27c3da8a0587f089d19fe3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Hermes{}

	skills, warnings, err := src.Skills(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["mine"] {
		t.Fatalf("want the user skill imported, got %+v", skills)
	}
	if names["bundledone"] {
		t.Fatalf("want the manifest-named bundled skill excluded, got %+v", skills)
	}
	for _, w := range warnings {
		if strings.Contains(w.Reason, "present but unreadable") {
			t.Fatalf("a well-formed manifest must never trigger the fail-closed warning, got %+v", warnings)
		}
	}
}

// TestHermesProfileNameKebabified is the MINOR D regression test: a
// profile named "Brain_2" is not itself a valid vault item name
// (uppercase, underscore). Its skill and its facts must still import,
// under the KEBABIFIED provenance tag "import:hermes:brain-2".
func TestHermesProfileNameKebabified(t *testing.T) {
	home := t.TempDir()
	profileDir := filepath.Join(home, ".hermes", "profiles", "Brain_2")

	memoriesDir := filepath.Join(profileDir, "memories")
	if err := os.MkdirAll(memoriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoriesDir, "USER.md"), []byte("Brain_2 profile user note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	skillDir := filepath.Join(profileDir, "skills", "brainskill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := "---\nname: brainskill\ndescription: a brain-2 profile skill\n---\n\nDo the brain thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, ProjectMemory: true}

	_, err := importer.RunImport(v, []importer.Source{importer.Hermes{}}, ctx, importer.Options{Skills: true, Memory: true})
	if err != nil {
		t.Fatal(err)
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	var foundFact *vault.Fact
	for i := range facts {
		if strings.Contains(facts[i].Body, "Brain_2 profile user note.") {
			foundFact = &facts[i]
		}
	}
	if foundFact == nil {
		t.Fatalf("want the Brain_2 profile's fact imported, got %+v", facts)
	}
	if foundFact.By != "import:hermes:brain-2" {
		t.Fatalf("want by: import:hermes:brain-2, got %q", foundFact.By)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	var foundSkill *vault.Skill
	for i := range skills {
		if skills[i].Name == "brainskill" {
			foundSkill = &skills[i]
		}
	}
	if foundSkill == nil {
		t.Fatalf("want the Brain_2 profile's skill imported, got %+v", skills)
	}
	if foundSkill.By != "import:hermes:brain-2" {
		t.Fatalf("want by: import:hermes:brain-2, got %q", foundSkill.By)
	}
}

// TestHermesSkillsProfileSkillsOnlyUnderProjectMemory checks the
// per-project/opt-in rule for a profile's own skills: by default the
// profile "brain" skill "ps" must not appear; with ProjectMemory
// true it must, namespaced Tool: "hermes:brain".
func TestHermesSkillsProfileSkillsOnlyUnderProjectMemory(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	src := importer.Hermes{}

	skills, _, err := src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "ps" {
			t.Fatalf("a profile skill must not import by default, got %+v", skills)
		}
	}

	skills, _, err = src.Skills(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	var found *importer.CandidateSkill
	for i := range skills {
		if skills[i].Name == "ps" {
			found = &skills[i]
		}
	}
	if found == nil {
		t.Fatalf("want the profile skill ps imported with ProjectMemory true, got %+v", skills)
	}
	if found.Tool != "hermes:brain" {
		t.Fatalf("want Tool hermes:brain, got %q", found.Tool)
	}
}

// TestHermesMemoryImportsMemoryAndUserByDefault runs the fixture's
// memories/MEMORY.md and memories/USER.md through the real engine,
// asserting the §-split of MEMORY.md into two project facts, the
// USER.md whole-file fact (no § present) typed user, and by:
// import:hermes / review: draft on every one of them.
func TestHermesMemoryImportsMemoryAndUserByDefault(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir}

	_, err := importer.RunImport(v, []importer.Source{importer.Hermes{}}, ctx, importer.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}

	var sawFirstChunk, sawSecondChunk, sawUser bool
	for _, f := range facts {
		if strings.Contains(f.Body, "First hermes memory fact about the project.") {
			sawFirstChunk = true
			if f.Type != "project" {
				t.Fatalf("want a MEMORY.md chunk typed project, got %q", f.Type)
			}
		}
		if strings.Contains(f.Body, "Second hermes memory fact about deployment.") {
			sawSecondChunk = true
			if f.Type != "project" {
				t.Fatalf("want a MEMORY.md chunk typed project, got %q", f.Type)
			}
		}
		if strings.Contains(f.Body, "The user prefers dark mode everywhere.") {
			sawUser = true
			if f.Type != "user" {
				t.Fatalf("want the USER.md fact typed user, got %q", f.Type)
			}
		}
		if f.By != "import:hermes" {
			t.Fatalf("want every hermes fact by: import:hermes, got %q on %+v", f.By, f)
		}
		if f.Review != "draft" {
			t.Fatalf("want every hermes fact review: draft, got %q on %+v", f.Review, f)
		}
	}
	if !sawFirstChunk || !sawSecondChunk {
		t.Fatalf("want both §-split MEMORY.md chunks imported, got %+v", facts)
	}
	if !sawUser {
		t.Fatalf("want the whole-file USER.md fact imported (no § present), got %+v", facts)
	}
}

// TestHermesMemoryLockSkipsWithWarningOtherFileStillImports checks
// the .lock sidecar rule directly against Memory: a MEMORY.md.lock
// file means MEMORY.md is skipped with a warning, but USER.md, with
// no lock of its own, still imports.
func TestHermesMemoryLockSkipsWithWarningOtherFileStillImports(t *testing.T) {
	home := t.TempDir()
	memories := filepath.Join(home, ".hermes", "memories")
	if err := os.MkdirAll(memories, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memories, "MEMORY.md"), []byte("Locked content that must not import.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memories, "MEMORY.md.lock"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memories, "USER.md"), []byte("Unlocked user content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := importer.Hermes{}

	facts, warnings, err := src.Memory(importer.ImportCtx{Home: home})
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range facts {
		if strings.Contains(f.Body, "Locked content that must not import.") {
			t.Fatalf("a locked MEMORY.md must never import, got %+v", facts)
		}
	}
	var sawUser bool
	for _, f := range facts {
		if strings.Contains(f.Body, "Unlocked user content.") {
			sawUser = true
		}
	}
	if !sawUser {
		t.Fatalf("want USER.md, with no lock, still imported, got %+v", facts)
	}

	var sawLockWarning bool
	for _, w := range warnings {
		if strings.Contains(w.Reason, "try the import again") {
			sawLockWarning = true
		}
	}
	if !sawLockWarning {
		t.Fatalf("want a warning naming the lock, got %+v", warnings)
	}
}

// TestHermesMemoryNeverImportsSoulMd checks the SOUL.md exclusion at
// both scopes: the top-level ~/.hermes/SOUL.md and the profile's own
// ~/.hermes/profiles/brain/SOUL.md. ProjectMemory is set to true so
// the profile scan path runs too, giving SOUL.md every chance to leak
// in if the exclusion were not real.
func TestHermesMemoryNeverImportsSoulMd(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	src := importer.Hermes{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if strings.Contains(f.Body, "SOUL-SENTINEL-MUST-NOT-IMPORT") {
			t.Fatalf("the top-level SOUL.md must never import, got %+v", facts)
		}
		if strings.Contains(f.Body, "BRAIN-SOUL-SENTINEL-MUST-NOT-IMPORT") {
			t.Fatalf("a profile's SOUL.md must never import, got %+v", facts)
		}
	}
}

// TestHermesMemoryProfileOnlyUnderProjectMemory checks the per-
// project/opt-in rule for a profile's own memory: by default the
// profile "brain" USER.md fact must not appear; with ProjectMemory
// true it must, namespaced Tool: "hermes:brain" — which write.go
// turns into by: import:hermes:brain once written to the vault.
func TestHermesMemoryProfileOnlyUnderProjectMemory(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	src := importer.Hermes{}

	facts, _, err := src.Memory(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		if strings.Contains(f.Body, "Profile brain user note.") {
			t.Fatalf("a profile fact must not import by default, got %+v", facts)
		}
	}

	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir, ProjectMemory: true}
	_, err = importer.RunImport(v, []importer.Source{importer.Hermes{}}, ctx, importer.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	vaultFacts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	var found *vault.Fact
	for i := range vaultFacts {
		if strings.Contains(vaultFacts[i].Body, "Profile brain user note.") {
			found = &vaultFacts[i]
		}
	}
	if found == nil {
		t.Fatalf("want the profile fact imported with ProjectMemory true, got %+v", vaultFacts)
	}
	if found.By != "import:hermes:brain" {
		t.Fatalf("want by: import:hermes:brain, got %q", found.By)
	}
	if found.Review != "draft" {
		t.Fatalf("want review: draft, got %q", found.Review)
	}
}

// TestHermesMemoryDefaultWarnsProfilesSkipped is the parity test for
// the fix noted in Task 4's own review: Gemini's and Droid's Memory
// methods already warn "N per-project memory sources skipped; pass
// --project-memory to include them" when ProjectMemory is off and a
// per-project source exists anyway. Hermes's own profiles are the
// same kind of off-by-default surface, so its Memory method must warn
// the same way whenever ~/.hermes/profiles holds at least one profile
// (the fixture's own "brain") and ProjectMemory is false.
func TestHermesMemoryDefaultWarnsProfilesSkipped(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	src := importer.Hermes{}

	_, warnings, err := src.Memory(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir})
	if err != nil {
		t.Fatal(err)
	}
	var sawSkipNote bool
	for _, w := range warnings {
		if strings.Contains(w.Reason, "per-project memory") && strings.Contains(w.Reason, "--project-memory") {
			sawSkipNote = true
		}
	}
	if !sawSkipNote {
		t.Fatalf("want a warning naming --project-memory when a profile exists but is skipped, got %+v", warnings)
	}

	// With ProjectMemory true, the same warning must not appear —
	// nothing was skipped, the profile scan actually ran.
	_, warnings, err = src.Memory(importer.ImportCtx{Home: home, VaultSkillsDir: vaultSkillsDir, ProjectMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		if strings.Contains(w.Reason, "per-project memory") && strings.Contains(w.Reason, "--project-memory") {
			t.Fatalf("want no skipped-profile warning once ProjectMemory is true, got %+v", warnings)
		}
	}
}

// TestHermesNeverReadsConfigYaml is the secret-safety check: the
// fixture's config.yaml carries a sentinel token nowhere else in the
// fixture. A full RunImport, skills and memory both, with
// ProjectMemory true so every scan path this source has runs, must
// never let that sentinel reach the vault — proof this source never
// opened config.yaml for its content.
func TestHermesNeverReadsConfigYaml(t *testing.T) {
	home, vaultSkillsDir := setupHermesHome(t)
	v := newClaudeCodeTestVault(t)
	ctx := importer.ImportCtx{Home: home, VaultRoot: v.Root, VaultSkillsDir: vaultSkillsDir, ProjectMemory: true}

	_, err := importer.RunImport(v, []importer.Source{importer.Hermes{}}, ctx, importer.Options{Skills: true, Memory: true})
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.Walk(v.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "SENTINEL-CONFIG-TOKEN-MUST-NOT-BE-READ") {
			t.Fatalf("config.yaml's content must never reach the vault, found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
