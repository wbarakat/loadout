package importer_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"loadout.dev/loadout/internal/importer"
	"loadout.dev/loadout/internal/vault"
)

// fakeSource is a canned Source for the engine's own tests: no real
// tool, no real filesystem scan, just fixed candidates and warnings
// handed back for whatever RunImport asks of it.
type fakeSource struct {
	name          string
	present       bool
	skills        []importer.CandidateSkill
	facts         []importer.CandidateFact
	skillWarnings []importer.Warning
	factWarnings  []importer.Warning
	skillErr      error
	factErr       error
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Detect(ctx importer.ImportCtx) (bool, string) {
	return f.present, ""
}

func (f *fakeSource) Skills(ctx importer.ImportCtx) ([]importer.CandidateSkill, []importer.Warning, error) {
	return f.skills, f.skillWarnings, f.skillErr
}

func (f *fakeSource) Memory(ctx importer.ImportCtx) ([]importer.CandidateFact, []importer.Warning, error) {
	return f.facts, f.factWarnings, f.factErr
}

func newEngineTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

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

func TestRunImportWritesFakeSourceCandidatesAsDraft(t *testing.T) {
	v := newEngineTestVault(t)
	modTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	src := &fakeSource{
		name:    "faketool",
		present: true,
		skills: []importer.CandidateSkill{
			{Name: "deploy-checks", Description: "run checks before a deploy", Body: "Run the checks.", Tool: "faketool", ModTime: modTime},
		},
		facts: []importer.CandidateFact{
			{Name: "my-stack", Description: "the stack I use", Type: "project", Body: "I use Go and Postgres.", Tool: "faketool", ModTime: modTime},
		},
	}
	ctx := importer.ImportCtx{Home: v.Root, VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	result, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Skills: true, Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 2 {
		t.Fatalf("want 2 imported, got %+v", result)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("want 1 skill written, got %+v", skills)
	}
	s := skills[0]
	if s.Name != "deploy-checks" || s.Description != "run checks before a deploy" {
		t.Fatalf("bad skill name/description: %+v", s)
	}
	if !strings.Contains(s.Body, "Run the checks.") {
		t.Fatalf("bad skill body: %+v", s)
	}
	if s.By != "import:faketool" {
		t.Fatalf("want by: import:faketool, got %q", s.By)
	}
	if s.Review != "draft" {
		t.Fatalf("want review: draft for a non-human by, got %q", s.Review)
	}
	if s.At != "2026-01-02T03:04:05Z" {
		t.Fatalf("want at carried from ModTime, got %q", s.At)
	}

	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("want 1 fact written, got %+v", facts)
	}
	fact := facts[0]
	if fact.Name != "my-stack" || fact.Description != "the stack I use" || fact.Type != "project" {
		t.Fatalf("bad fact name/description/type: %+v", fact)
	}
	if !strings.Contains(fact.Body, "I use Go and Postgres.") {
		t.Fatalf("bad fact body: %+v", fact)
	}
	if fact.By != "import:faketool" || fact.Review != "draft" {
		t.Fatalf("bad fact provenance: %+v", fact)
	}
}

func TestRunImportSkipsAbsentSource(t *testing.T) {
	v := newEngineTestVault(t)
	src := &fakeSource{
		name:    "faketool",
		present: false,
		skills:  []importer.CandidateSkill{{Name: "should-not-import", Body: "x", Tool: "faketool"}},
	}
	ctx := importer.ImportCtx{VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	result, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Skills: true, Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 0 {
		t.Fatalf("an absent source must contribute nothing, got %+v", result)
	}
}

func TestRunImportSkipsBadName(t *testing.T) {
	v := newEngineTestVault(t)
	src := &fakeSource{
		name:    "faketool",
		present: true,
		skills:  []importer.CandidateSkill{{Name: "Not A Good Name", Description: "d", Body: "x", Tool: "faketool"}},
	}
	ctx := importer.ImportCtx{VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	result, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Skills: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 0 {
		t.Fatalf("a bad name must not be imported, got %+v", result)
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("a bad name must be recorded as skipped, got %+v", result)
	}
	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("a bad name must not reach disk, got %+v", skills)
	}
}

func TestRunImportStripsLoadoutBlockFromMemory(t *testing.T) {
	v := newEngineTestVault(t)
	body := "# My own notes\n\nKeep me.\n\n<!-- loadout:begin -->\nsynced junk\n<!-- loadout:end -->\n"
	src := &fakeSource{
		name:    "faketool",
		present: true,
		facts:   []importer.CandidateFact{{Name: "notes", Description: "d", Type: "user", Body: body, Tool: "faketool"}},
	}
	ctx := importer.ImportCtx{VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	if _, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Memory: true}); err != nil {
		t.Fatal(err)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("want 1 fact, got %+v", facts)
	}
	if strings.Contains(facts[0].Body, "synced junk") {
		t.Fatalf("the loadout block must be stripped before writing, got %q", facts[0].Body)
	}
	if !strings.Contains(facts[0].Body, "Keep me.") {
		t.Fatalf("native content outside the block must survive, got %q", facts[0].Body)
	}
}

func TestRunImportSkipsDamagedLoadoutBlock(t *testing.T) {
	v := newEngineTestVault(t)
	body := "<!-- loadout:begin -->\na\n<!-- loadout:end -->\nmid\n<!-- loadout:begin -->\nb\n<!-- loadout:end -->\n"
	src := &fakeSource{
		name:    "faketool",
		present: true,
		facts:   []importer.CandidateFact{{Name: "notes", Description: "d", Type: "user", Body: body, Tool: "faketool"}},
	}
	ctx := importer.ImportCtx{VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	result, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 0 {
		t.Fatalf("a damaged block must not import, got %+v", result)
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("a damaged block must be recorded as skipped, got %+v", result)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("nothing must be written for a damaged block, got %+v", facts)
	}
}

func TestRunImportDedupsAcrossSources(t *testing.T) {
	v := newEngineTestVault(t)
	srcA := &fakeSource{
		name:    "toola",
		present: true,
		facts:   []importer.CandidateFact{{Name: "my-stack", Description: "d", Type: "user", Body: "I use Go.", Tool: "toola"}},
	}
	srcB := &fakeSource{
		name:    "toolb",
		present: true,
		facts:   []importer.CandidateFact{{Name: "my-stack", Description: "d", Type: "user", Body: "I use Go.", Tool: "toolb"}},
	}
	ctx := importer.ImportCtx{VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	result, err := importer.RunImport(v, []importer.Source{srcA, srcB}, ctx, importer.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 || len(result.Deduped) != 1 {
		t.Fatalf("want 1 imported + 1 deduped across sources, got %+v", result)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("only one fact must be written, got %+v", facts)
	}
}

func TestRunImportDryRunWritesNothing(t *testing.T) {
	v := newEngineTestVault(t)
	src := &fakeSource{
		name:    "faketool",
		present: true,
		skills:  []importer.CandidateSkill{{Name: "deploy-checks", Description: "d", Body: "Run the checks.", Tool: "faketool"}},
		facts:   []importer.CandidateFact{{Name: "my-stack", Description: "d", Type: "user", Body: "I use Go.", Tool: "faketool"}},
	}
	ctx := importer.ImportCtx{VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}
	before := snapshotTree(t, v.Root)

	result, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Skills: true, Memory: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, v.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("a dry run must not write anything to the vault:\nbefore=%v\nafter=%v", before, after)
	}
	if len(result.Imported) != 2 {
		t.Fatalf("a dry run must still report the preview it would have written, got %+v", result)
	}

	skills, err := vault.ListSkills(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("a dry run must not create any skill, got %+v", skills)
	}
	facts, err := vault.ListFacts(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("a dry run must not create any fact, got %+v", facts)
	}
}

// TestRunImportDryRunSkipsInvalidNameLikeARealRun is the I1
// regression test: a real write refuses an invalid vault name (via
// vault.ValidName), but the old DryRun preview applied no such check
// and would have listed it as importable anyway.
func TestRunImportDryRunSkipsInvalidNameLikeARealRun(t *testing.T) {
	v := newEngineTestVault(t)
	src := &fakeSource{
		name:    "faketool",
		present: true,
		skills:  []importer.CandidateSkill{{Name: "skill/../../escape", Description: "d", Body: "x", Tool: "faketool"}},
	}
	ctx := importer.ImportCtx{VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	result, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Skills: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 0 {
		t.Fatalf("an invalid name must not be previewed as importable, got %+v", result.Imported)
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("an invalid name must be recorded as skipped in a dry run, the same as a real run, got %+v", result)
	}
}

// TestRunImportDryRunSkipsVaultNameCollisionLikeARealRun is the other
// half of the I1 fix: a name that collides with an existing,
// different-content vault item must be skipped in the preview too,
// not shown as importable only to be refused by a real run.
func TestRunImportDryRunSkipsVaultNameCollisionLikeARealRun(t *testing.T) {
	v := newEngineTestVault(t)
	if _, err := vault.WriteFactContent(v, "my-stack", "d", "user", "existing content", "human", time.Now()); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{
		name:    "faketool",
		present: true,
		facts:   []importer.CandidateFact{{Name: "my-stack", Description: "d", Type: "user", Body: "different content", Tool: "faketool"}},
	}
	ctx := importer.ImportCtx{VaultRoot: v.Root, VaultSkillsDir: v.SkillsDir()}

	result, err := importer.RunImport(v, []importer.Source{src}, ctx, importer.Options{Memory: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 0 {
		t.Fatalf("a name colliding with an existing, different-content vault item must not be previewed as importable, got %+v", result.Imported)
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("want the collision recorded as skipped in the dry run, the same as a real run, got %+v", result)
	}
}
