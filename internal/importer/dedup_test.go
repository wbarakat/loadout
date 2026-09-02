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
	kept, deduped := dedupCandidates(items)
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
	kept, deduped := dedupCandidates(items)
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
	kept, _ := dedupCandidates(items)
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
	if _, err := vault.WriteSkillContent(v, "deploy-checks", "run checks", "Run the checks.", "human", time.Now(), nil); err != nil {
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
