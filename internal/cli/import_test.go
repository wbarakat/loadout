package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

// codexSystemMarkerName is the sentinel file name Codex's own import
// Source (internal/importer/codex.go) checks for before it excludes
// the whole .system subtree as vendor content. It is unexported
// there, so this test names the literal directly rather than
// importing it.
const codexSystemMarkerName = ".codex-system-skills.marker"

// writeFixtureFile creates path (and its parent directories) holding
// content.
func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupImportFixture builds a fresh, isolated environment for one
// "loadout import" test: a temp HOME holding BOTH a claude-code tree
// and a codex tree, an empty temp project directory (so a stray
// AGENTS.md or .agents/skills in the real repo checkout can never
// leak into a scan), and a freshly initialized vault.
//
// Home injection: cmdImport calls os.UserHomeDir(), which on Unix
// reads $HOME — the same mechanism vault.ExpandPath already relies
// on. t.Setenv("HOME", ...) therefore redirects it cleanly for the
// life of this test, with no package-level variable needed; the real
// machine's home is never referenced by anything this test runs, and
// the override reverts automatically when the test ends.
//
// The fixture holds:
//   - a normal claude-code skill ("mytool") and a normal codex skill
//     ("allowed-tool")
//   - a skill shared between the two tools via a real .agents/skills
//     directory that codex reads directly and claude-code reads
//     through a same-name symlink — RunImport must dedup this to one
//     written item
//   - a skill symlinked into the vault's own skills directory
//     ("loadout-owned") — Loadout's own projected content, which must
//     never import
//   - a codex ".system" skill behind its marker file
//     ("review-agent") — vendor content, which must never import
//   - claude-code memory (CLAUDE.md, plus an auto-memory topic file
//     and its MEMORY.md index) and codex memory (AGENTS.md)
//   - a codex config.toml (and auth.json) each holding a distinct
//     sentinel string that must never be read, let alone appear in
//     any output
func setupImportFixture(t *testing.T) (home, vaultRoot, projectDir string) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	vaultRoot = filepath.Join(base, "vault")
	projectDir = filepath.Join(base, "project")

	t.Setenv("HOME", home)
	t.Setenv("LOADOUT_HOME", vaultRoot)
	// Cleared, not just unset by the test process's own environment,
	// so a real developer machine's own CLAUDE_CONFIG_DIR/CODEX_HOME
	// can never leak into this test either.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")

	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, errOut, code := run(t, "init"); code != 0 {
		t.Fatalf("init failed: %s", errOut)
	}

	// claude-code: a normal skill.
	writeFixtureFile(t, filepath.Join(home, ".claude", "skills", "mytool", "SKILL.md"),
		"---\nname: mytool\ndescription: run mytool's own checks before a commit\n---\n\nRun the mytool checks before every commit.\n")

	// claude-code: native CLAUDE.md memory (one plain paragraph, no
	// headings, imports as a single "claude-md" fact).
	writeFixtureFile(t, filepath.Join(home, ".claude", "CLAUDE.md"),
		"Keep commits atomic and focused.\n")

	// claude-code: the separate auto-memory vault, plus its MEMORY.md
	// index, which must never import as a fact of its own.
	writeFixtureFile(t, filepath.Join(home, ".claude", "projects", "proj1", "memory", "note.md"),
		"---\nname: proj1-note\ndescription: a fact about this project\ntype: project\n---\n\nThe project's build lives under ./build.\n")
	writeFixtureFile(t, filepath.Join(home, ".claude", "projects", "proj1", "memory", "MEMORY.md"),
		"# index\n\n- [proj1 note](note.md)\n")

	// The skill shared between claude-code and codex: a real directory
	// under .agents/skills (codex reads this scope directly), plus a
	// same-name symlink under .claude/skills (claude-code reads this
	// scope directly too) pointing at it.
	sharedDir := filepath.Join(home, ".agents", "skills", "shared-skill")
	writeFixtureFile(t, filepath.Join(sharedDir, "SKILL.md"),
		"---\nname: shared-skill\ndescription: seen by both claude-code and codex\n---\n\nShared skill body, one copy on disk.\n")
	if err := os.Symlink(sharedDir, filepath.Join(home, ".claude", "skills", "shared-skill")); err != nil {
		t.Fatal(err)
	}

	// A skill Loadout itself already projected: a symlink resolving
	// into the vault's own skills directory. Must never import.
	vaultOwnedTarget := filepath.Join(vaultRoot, "skills", "loadout-owned-target")
	if err := os.MkdirAll(vaultOwnedTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(vaultOwnedTarget, filepath.Join(home, ".claude", "skills", "loadout-owned")); err != nil {
		t.Fatal(err)
	}

	// codex: native AGENTS.md memory.
	writeFixtureFile(t, filepath.Join(home, ".codex", "AGENTS.md"),
		"Keep this note about how I like codex to behave.\n")

	// codex: a normal user skill.
	writeFixtureFile(t, filepath.Join(home, ".codex", "skills", "allowed-tool", "SKILL.md"),
		"---\nname: allowed-tool\ndescription: a codex-approved tool\n---\n\nDo the allowed-tool thing.\n")

	// codex: the bundled .system subtree, excluded once its marker is
	// present.
	writeFixtureFile(t, filepath.Join(home, ".codex", "skills", ".system", codexSystemMarkerName), "")
	writeFixtureFile(t, filepath.Join(home, ".codex", "skills", ".system", "review-agent", "SKILL.md"),
		"---\nname: review-agent\ndescription: bundled codex review agent\n---\n\nDo the codex system thing.\n")

	// codex: config.toml and auth.json, each holding a sentinel this
	// source must never read — Codex's own Source only ever opens
	// AGENTS.md/AGENTS.override.md and skill SKILL.md files.
	writeFixtureFile(t, filepath.Join(home, ".codex", "config.toml"),
		"# fixture only — must never be read\nsentinel = \"SENTINEL-DO-NOT-LEAK-CONFIG\"\n")
	writeFixtureFile(t, filepath.Join(home, ".codex", "auth.json"),
		"{\"api_key\": \"SENTINEL-DO-NOT-LEAK-AUTH\"}\n")

	return home, vaultRoot, projectDir
}

// snapshotTree hashes every regular file under root, keyed by its
// path relative to root, so a before/after comparison can prove a run
// wrote (or did not write) anything at all — not just that the item
// counts came out the same. It skips loadout.lock: taking the vault
// lock (which cmdImport does even under --dry-run, the same as
// cmdSync) leaves that empty, gitignored file behind as a harmless
// side effect, never a content write worth comparing.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "loadout.lock" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		snap[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func skillNamed(skills []vault.Skill, name string) (vault.Skill, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	return vault.Skill{}, false
}

func factNamed(facts []vault.Fact, name string) (vault.Fact, bool) {
	for _, f := range facts {
		if f.Name == name {
			return f, true
		}
	}
	return vault.Fact{}, false
}

func TestImportDryRunWritesNothing(t *testing.T) {
	_, vaultRoot, projectDir := setupImportFixture(t)
	before := snapshotTree(t, vaultRoot)

	out, errOut, code := run(t, "import", "--project", projectDir, "--dry-run")
	if code != 0 {
		t.Fatalf("import --dry-run failed: %s", errOut)
	}

	after := snapshotTree(t, vaultRoot)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("a dry run must write nothing to the vault\nbefore=%v\nafter=%v", before, after)
	}

	for _, want := range []string{"mytool", "shared-skill", "allowed-tool"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want the dry-run preview to name %q, got %q", want, out)
		}
	}
	if !strings.Contains(out, "nothing was written") {
		t.Fatalf("want the report to note nothing was written, got %q", out)
	}
}

// TestImportWritesDraftItems passes --project-memory so its existing
// assertion that proj1-note (per-project auto-memory) imports still
// holds: FIX 4 moved per-project memory behind that flag, opt-in
// rather than the default. See TestImportMemoryDefaultIsGlobalOnly
// for the default-off behavior this test used to (accidentally)
// exercise, and TestImportProjectMemoryFlagIncludesPerProjectMemory
// for the flag's own dedicated test.
func TestImportWritesDraftItems(t *testing.T) {
	_, vaultRoot, projectDir := setupImportFixture(t)

	out, errOut, code := run(t, "import", "--project", projectDir, "--project-memory")
	if code != 0 {
		t.Fatalf("import failed: %s", errOut)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mytool", "shared-skill", "allowed-tool"} {
		s, ok := skillNamed(skills, want)
		if !ok {
			t.Fatalf("want skill %q imported, got %+v", want, skills)
		}
		if !strings.HasPrefix(s.By, "import:") {
			t.Fatalf("want %q's by to be import:<tool>, got %q", want, s.By)
		}
		if s.Review != "draft" {
			t.Fatalf("want %q review: draft, got %q", want, s.Review)
		}
	}
	if _, ok := skillNamed(skills, "loadout-owned"); ok {
		t.Fatalf("the vault-owned skill must never import, got %+v", skills)
	}
	if _, ok := skillNamed(skills, "review-agent"); ok {
		t.Fatalf("the codex .system skill must never import, got %+v", skills)
	}
	count := 0
	for _, s := range skills {
		if s.Name == "shared-skill" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want the shared .agents/skills skill written exactly once, got %d copies", count)
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claude-md", "proj1-note"} {
		f, ok := factNamed(facts, want)
		if !ok {
			t.Fatalf("want fact %q imported, got %+v", want, facts)
		}
		if !strings.HasPrefix(f.By, "import:") || f.Review != "draft" {
			t.Fatalf("bad provenance for fact %q: %+v", want, f)
		}
	}
	if _, ok := factNamed(facts, "memory"); ok {
		t.Fatalf("the MEMORY.md index must never import as a fact, got %+v", facts)
	}

	if !strings.Contains(out, "draft") {
		t.Fatalf("want the report to name drafts, got %q", out)
	}
	if !strings.Contains(out, "loadout review") {
		t.Fatalf("want the report to name the review next step, got %q", out)
	}
	if !strings.Contains(out, "loadout sync --remote") {
		t.Fatalf("want the report to name the sync next step, got %q", out)
	}
}

// TestImportMemoryDefaultIsGlobalOnly is the FIX 4 regression test:
// "loadout import" (memory) used to glob ALL projects' auto-memory by
// default, flooding the vault with per-project work notes. Without
// --project-memory, the default must import only the global CLAUDE.md
// fact, never the fixture's proj1-note auto-memory fact.
func TestImportMemoryDefaultIsGlobalOnly(t *testing.T) {
	_, vaultRoot, projectDir := setupImportFixture(t)

	if _, errOut, code := run(t, "import", "--project", projectDir); code != 0 {
		t.Fatalf("import failed: %s", errOut)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := factNamed(facts, "claude-md"); !ok {
		t.Fatalf("want the global CLAUDE.md fact imported by default, got %+v", facts)
	}
	if _, ok := factNamed(facts, "proj1-note"); ok {
		t.Fatalf("want per-project auto-memory NOT imported by default (no --project-memory), got %+v", facts)
	}
}

// TestImportProjectMemoryFlagIncludesPerProjectMemory is the other
// half of the FIX 4 test: --project-memory pulls in the per-project
// auto-memory fact alongside the global one.
func TestImportProjectMemoryFlagIncludesPerProjectMemory(t *testing.T) {
	_, vaultRoot, projectDir := setupImportFixture(t)

	if _, errOut, code := run(t, "import", "--project", projectDir, "--project-memory"); code != 0 {
		t.Fatalf("import --project-memory failed: %s", errOut)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := factNamed(facts, "claude-md"); !ok {
		t.Fatalf("want the global CLAUDE.md fact imported, got %+v", facts)
	}
	if _, ok := factNamed(facts, "proj1-note"); !ok {
		t.Fatalf("want the per-project auto-memory fact imported with --project-memory, got %+v", facts)
	}
}

func TestImportCodexLimitsToCodex(t *testing.T) {
	_, vaultRoot, projectDir := setupImportFixture(t)

	if _, errOut, code := run(t, "import", "codex", "--project", projectDir); code != 0 {
		t.Fatalf("import codex failed: %s", errOut)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := skillNamed(skills, "mytool"); ok {
		t.Fatalf("import codex must not pull claude-code-only content, got %+v", skills)
	}
	if _, ok := skillNamed(skills, "allowed-tool"); !ok {
		t.Fatalf("want the codex skill imported, got %+v", skills)
	}
	for _, s := range skills {
		if s.By != "import:codex" {
			t.Fatalf("want every imported skill tagged import:codex, got %+v", s)
		}
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := factNamed(facts, "claude-md"); ok {
		t.Fatalf("import codex must not pull claude-code-only memory, got %+v", facts)
	}
}

func TestImportUnknownSourceIsUsageError(t *testing.T) {
	_, _, projectDir := setupImportFixture(t)

	_, errOut, code := run(t, "import", "bogus-tool", "--project", projectDir)
	if code != 2 {
		t.Fatalf("want an unknown source to be a usage-shaped error (exit 2), got %d %q", code, errOut)
	}
	if !strings.Contains(errOut, "bogus-tool") {
		t.Fatalf("want the error to name the bad source, got %q", errOut)
	}
	if !strings.Contains(errOut, "claude-code") || !strings.Contains(errOut, "codex") {
		t.Fatalf("want the error to name the valid sources, got %q", errOut)
	}
}

func TestImportJSON(t *testing.T) {
	_, _, projectDir := setupImportFixture(t)

	out, errOut, code := run(t, "import", "--project", projectDir, "--json")
	if code != 0 {
		t.Fatalf("import --json failed: %s", errOut)
	}

	var result struct {
		Imported []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Tool string `json:"tool"`
		} `json:"imported"`
		Deduped  []json.RawMessage `json:"deduped"`
		Skipped  []json.RawMessage `json:"skipped"`
		Warnings []json.RawMessage `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("want parseable JSON, got %v: %s", err, out)
	}
	if len(result.Imported) == 0 {
		t.Fatalf("want a non-empty imported list, got %q", out)
	}
	var foundShared bool
	for _, item := range result.Imported {
		if item.Name == "shared-skill" {
			foundShared = true
		}
	}
	if !foundShared {
		t.Fatalf("want shared-skill in the JSON imported list, got %+v", result.Imported)
	}
	if len(result.Deduped) == 0 {
		t.Fatalf("want the cross-source dedup of shared-skill recorded, got %q", out)
	}
}

func TestImportNoSecretInOutput(t *testing.T) {
	_, vaultRoot, projectDir := setupImportFixture(t)

	out, errOut, code := run(t, "import", "--project", projectDir, "--json")
	if code != 0 {
		t.Fatalf("import failed: %s", errOut)
	}

	sentinels := []string{"SENTINEL-DO-NOT-LEAK-CONFIG", "SENTINEL-DO-NOT-LEAK-AUTH"}
	for _, s := range sentinels {
		if strings.Contains(out, s) {
			t.Fatalf("sentinel %q must never appear in stdout, got %q", s, out)
		}
		if strings.Contains(errOut, s) {
			t.Fatalf("sentinel %q must never appear in stderr, got %q", s, errOut)
		}
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		for _, sentinel := range sentinels {
			if strings.Contains(s.Body, sentinel) || strings.Contains(s.Description, sentinel) {
				t.Fatalf("sentinel leaked into skill %q", s.Name)
			}
		}
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range facts {
		for _, sentinel := range sentinels {
			if strings.Contains(f.Body, sentinel) || strings.Contains(f.Description, sentinel) {
				t.Fatalf("sentinel leaked into fact %q", f.Name)
			}
		}
	}

	// Belt and braces: the fixture files must still exist, untouched —
	// a passing test above could otherwise be explained by the files
	// having been deleted rather than never opened.
	for _, name := range []string{"config.toml", "auth.json"} {
		home := os.Getenv("HOME")
		if _, err := os.Stat(filepath.Join(home, ".codex", name)); err != nil {
			t.Fatalf("fixture file %s must still exist: %v", name, err)
		}
	}
}

func TestImportDryRunFlagIsUsageError(t *testing.T) {
	_, _, projectDir := setupImportFixture(t)
	if _, errOut, code := run(t, "import", "--project"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("--project without a value must be a usage error, got %d %q", code, errOut)
	}
	if _, errOut, code := run(t, "import", "--nope", "--project", projectDir); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("an unknown flag must be a usage error, got %d %q", code, errOut)
	}
}

func TestHelpListsImport(t *testing.T) {
	out, _, code := run(t, "help")
	if code != 0 {
		t.Fatalf("help failed with code %d", code)
	}
	if !strings.Contains(out, "import") {
		t.Fatalf("want loadout help to list the import verb, got %q", out)
	}
}
