package importer

import (
	"path/filepath"
	"testing"
	"time"

	"loadout.dev/loadout/internal/vault"
)

func newDedupTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	v, err := vault.Init(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDedupCandidatesDropsExactDuplicate(t *testing.T) {
	items := []item{
		{kind: "skill", name: "deploy-checks", body: "Run the checks.", tool: "claude-code"},
		{kind: "skill", name: "deploy-checks", body: "Run   the checks.\n", tool: "codex"},
	}
	kept, deduped, err := dedupCandidates(items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 {
		t.Fatalf("want 1 kept (whitespace-only difference is still an exact dupe), got %d: %+v", len(kept), kept)
	}
	if kept[0].name != "deploy-checks" || kept[0].tool != "claude-code" {
		t.Fatalf("the first item seen must be the one kept, got %+v", kept[0])
	}
	if len(deduped) != 1 || deduped[0].Tool != "codex" || deduped[0].Name != "deploy-checks" {
		t.Fatalf("want the second duplicate recorded as deduped, got %+v", deduped)
	}
}

func TestDedupCandidatesKeepsDivergentSameName(t *testing.T) {
	items := []item{
		{kind: "memory", name: "my-stack", body: "I use Go.", tool: "claude-code"},
		{kind: "memory", name: "my-stack", body: "I use Rust.", tool: "codex"},
	}
	kept, deduped, err := dedupCandidates(items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped) != 0 {
		t.Fatalf("a same-name-different-content item must never be silently dropped, got %+v", deduped)
	}
	if len(kept) != 2 {
		t.Fatalf("want both distinct-content items kept, got %d: %+v", len(kept), kept)
	}
	names := map[string]bool{}
	for _, it := range kept {
		names[it.name] = true
	}
	if !names["my-stack"] {
		t.Fatalf("the first item must keep its original name, got %+v", kept)
	}
	if !names["my-stack-codex"] {
		t.Fatalf("the second item must get a name disambiguated by tool, got %+v", kept)
	}
}

func TestDedupCandidatesDisambiguatesFurtherCollision(t *testing.T) {
	items := []item{
		{kind: "memory", name: "my-stack", body: "I use Go.", tool: "claude-code"},
		{kind: "memory", name: "my-stack", body: "I use Rust.", tool: "codex"},
		{kind: "memory", name: "my-stack-codex", body: "totally unrelated content", tool: "hermes"},
	}
	kept, _, err := dedupCandidates(items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 3 {
		t.Fatalf("want 3 kept, got %d: %+v", len(kept), kept)
	}
	names := map[string]bool{}
	for _, it := range kept {
		names[it.name] = true
	}
	if !names["my-stack"] || !names["my-stack-codex"] || !names["my-stack-codex-2"] {
		t.Fatalf("want a further -2 disambiguated name once -<tool> is itself taken, got %+v", kept)
	}
}

// TestDedupCandidatesDisambiguationDoesNotStealAnotherGroupsRealName
// is the dedup disambiguation minor's regression test: a genuine
// skill already named "shared-skill-codex" must keep that name, even
// though a divergent "shared-skill" group (with a codex-tool member)
// is processed first and, before this fix, would invent that exact
// name for itself via "<name>-<tool>" disambiguation — bumping the
// real skill to "shared-skill-codex-2" instead. Both must survive
// under distinct names, and neither one may silently take the
// other's identity.
func TestDedupCandidatesDisambiguationDoesNotStealAnotherGroupsRealName(t *testing.T) {
	items := []item{
		{kind: "skill", name: "shared-skill", body: "variant A", tool: "claude-code"},
		{kind: "skill", name: "shared-skill", body: "variant B", tool: "codex"},
		{kind: "skill", name: "shared-skill-codex", body: "a genuine, unrelated skill", tool: "hermes"},
	}
	kept, deduped, err := dedupCandidates(items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped) != 0 {
		t.Fatalf("every item here has distinct content, none must be dropped as a duplicate, got %+v", deduped)
	}
	byName := map[string]item{}
	for _, it := range kept {
		byName[it.name] = it
	}
	if it, ok := byName["shared-skill-codex"]; !ok || it.tool != "hermes" {
		t.Fatalf("want the genuine shared-skill-codex skill to keep its own real name, got %+v", byName)
	}
	if it, ok := byName["shared-skill-codex-2"]; !ok || it.tool != "codex" {
		t.Fatalf("want the divergent codex variant of shared-skill pushed to shared-skill-codex-2 rather than stealing the real name, got %+v", byName)
	}
	if _, ok := byName["shared-skill"]; !ok {
		t.Fatalf("want the claude-code variant to keep the plain shared-skill name, got %+v", byName)
	}
}

// TestDedupCandidatesFoldsSupportFilesIntoContentHash is the second
// half of the C1 fix: a skill's dedup key must not ignore Files. Two
// candidates here share byte-identical SKILL.md bodies but carry
// different helper.sh content — without Files folded into the hash,
// this reads as one exact duplicate and the second copy (and its
// different helper.sh) is silently dropped.
func TestDedupCandidatesFoldsSupportFilesIntoContentHash(t *testing.T) {
	items := []item{
		{kind: "skill", name: "foo", body: "Same body.", tool: "claude-code", files: map[string][]byte{"helper.sh": []byte("v1")}},
		{kind: "skill", name: "foo", body: "Same body.", tool: "codex", files: map[string][]byte{"helper.sh": []byte("v2")}},
	}
	kept, deduped, err := dedupCandidates(items, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped) != 0 {
		t.Fatalf("the same body with different support files must not be treated as an exact duplicate, got %+v", deduped)
	}
	if len(kept) != 2 {
		t.Fatalf("want both distinct-files items kept, got %d: %+v", len(kept), kept)
	}
}

// TestDedupCandidatesDisambiguationAvoidsVaultNameCollision covers
// the dedup disambiguation minor: a divergent candidate's invented
// name must not collide with a name the vault already owns for an
// unrelated item. The vault already holds "foo-codex" (crafted here
// to collide with what plain "<name>-<tool>" disambiguation would
// pick); a divergent "foo" pair (one candidate genuinely from codex)
// must still both land, under distinct names, neither one silently
// skipped because its chosen name collided with the vault's own
// unrelated foo-codex.
func TestDedupCandidatesDisambiguationAvoidsVaultNameCollision(t *testing.T) {
	v := newDedupTestVault(t)
	if _, _, err := vault.WriteSkillContent(v, "foo-codex", "an unrelated, pre-existing skill", "crafted vault content", "human", time.Now(), nil); err != nil {
		t.Fatal(err)
	}

	items := []item{
		{kind: "skill", name: "foo", body: "I use Go.", tool: "claude-code"},
		{kind: "skill", name: "foo", body: "I use Rust.", tool: "codex"},
	}
	kept, deduped, err := dedupCandidates(items, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped) != 0 {
		t.Fatalf("both items have distinct content, neither must be dropped as a duplicate, got %+v", deduped)
	}
	if len(kept) != 2 {
		t.Fatalf("want both distinct-content items kept, got %d: %+v", len(kept), kept)
	}
	names := map[string]bool{}
	for _, it := range kept {
		names[it.name] = true
	}
	if !names["foo"] {
		t.Fatalf("the first item must keep its original name, got %+v", kept)
	}
	if names["foo-codex"] {
		t.Fatalf("the disambiguated name must not collide with the vault's own pre-existing foo-codex skill, got %+v", kept)
	}
	if !names["foo-codex-2"] {
		t.Fatalf("want the disambiguated item pushed past the vault-owned name to foo-codex-2, got %+v", kept)
	}
}

func TestDedupAgainstVaultSkipsExistingSameContent(t *testing.T) {
	v := newDedupTestVault(t)
	if _, err := vault.WriteFactContent(v, "my-stack", "the stack I use", "user", "I use Go and Postgres.", "human", time.Now()); err != nil {
		t.Fatal(err)
	}

	items := []item{{kind: "memory", name: "my-stack", body: "I use Go and Postgres.", tool: "claude-code"}}
	kept, deduped, err := dedupAgainstVault(items, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Fatalf("an item already in the vault with the same content must be skipped, got %+v", kept)
	}
	if len(deduped) != 1 || deduped[0].Name != "my-stack" || deduped[0].Kind != "memory" {
		t.Fatalf("want it recorded as deduped, got %+v", deduped)
	}
}

func TestDedupAgainstVaultKeepsDifferentContent(t *testing.T) {
	v := newDedupTestVault(t)
	if _, err := vault.WriteFactContent(v, "my-stack", "the stack I use", "user", "I use Go and Postgres.", "human", time.Now()); err != nil {
		t.Fatal(err)
	}

	items := []item{{kind: "memory", name: "my-stack", body: "I use Rust now.", tool: "claude-code"}}
	kept, deduped, err := dedupAgainstVault(items, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped) != 0 {
		t.Fatalf("different content under the same name must not be silently deduped, got %+v", deduped)
	}
	if len(kept) != 1 {
		t.Fatalf("want it kept, so write.go reports the real name collision, got %+v", kept)
	}
}

func TestDedupAgainstVaultSkipsExistingSkill(t *testing.T) {
	v := newDedupTestVault(t)
	if _, _, err := vault.WriteSkillContent(v, "deploy-checks", "run checks", "Run the checks.", "human", time.Now(), nil); err != nil {
		t.Fatal(err)
	}

	items := []item{{kind: "skill", name: "deploy-checks", body: "Run the checks.", tool: "claude-code"}}
	kept, deduped, err := dedupAgainstVault(items, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 || len(deduped) != 1 {
		t.Fatalf("an existing skill with the same content must be skipped, kept=%+v deduped=%+v", kept, deduped)
	}
}
