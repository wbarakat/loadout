package vault_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

// runGitIn runs git in root and fails the test on error. It mirrors
// the inline helper vault_test.go uses to build a legacy vault.
func runGitIn(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
	return string(out)
}

// legacyVault builds a vault the way Init leaves one, then
// force-tracks loadout.toml, to simulate a vault made before the
// manifest split, when the manifest still went into history.
func legacyVault(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, root, "add", "-f", "loadout.toml")
	runGitIn(t, root, "-c", "user.name=x", "-c", "user.email=x@x",
		"commit", "-q", "-m", "legacy: track the manifest")
	return root
}

// TestInitNeverTracksManifest proves a fresh vault never puts
// loadout.toml into history: Decision 13 (spec v3.1 §16) keeps the
// manifest device-local.
func TestInitNeverTracksManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root); err != nil {
		t.Fatal(err)
	}
	out := runGitIn(t, root, "ls-files", "loadout.toml")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("Init must not track loadout.toml, ls-files gave %q", out)
	}
}

// TestOpenHealsLegacyTrackedManifest proves Open untracks
// loadout.toml on a vault made before the manifest split.
func TestOpenHealsLegacyTrackedManifest(t *testing.T) {
	root := legacyVault(t)
	if out := runGitIn(t, root, "ls-files", "loadout.toml"); strings.TrimSpace(out) == "" {
		t.Fatal("test setup: loadout.toml must be tracked before Open heals it")
	}
	if _, err := vault.Open(root); err != nil {
		t.Fatal(err)
	}
	if out := runGitIn(t, root, "ls-files", "loadout.toml"); strings.TrimSpace(out) != "" {
		t.Fatal("Open must untrack loadout.toml on a legacy vault")
	}
}

// TestOpenHealsLegacyManifestExactlyOnce proves the heal runs once:
// after the first Open untracks the manifest, a second Open finds the
// tracking probe already empty and adds no history entry.
func TestOpenHealsLegacyManifestExactlyOnce(t *testing.T) {
	root := legacyVault(t)
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := vault.History(v, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(root); err != nil {
		t.Fatal(err)
	}
	after, err := vault.History(v, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("second Open must add no history entry, got %d before and %d after", len(before), len(after))
	}
}

// TestOpenMigrationKeepsOldVersionsInHistory proves the migration is
// forward-only: it adds a "split the manifest" commit, and the
// pre-split commit still holds the tracked loadout.toml.
func TestOpenMigrationKeepsOldVersionsInHistory(t *testing.T) {
	root := legacyVault(t)
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := vault.History(v, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Subject != "split the manifest" {
		t.Fatalf("History must show the split commit first, got %v", entries)
	}
	out := runGitIn(t, root, "show", "HEAD~1:loadout.toml")
	if !strings.Contains(out, "version") {
		t.Fatalf("the pre-split commit must still hold loadout.toml, git show gave %q", out)
	}
}

// TestOpenMigrationSkipsVaultWithNoHistory proves the tracking probe
// never fails Open on a vault whose .git directory is gone: that
// vault's own noHistoryErr path stays intact for later verbs instead.
func TestOpenMigrationSkipsVaultWithNoHistory(t *testing.T) {
	root := legacyVault(t)
	if err := exec.Command("rm", "-rf", filepath.Join(root, ".git")).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(root); err != nil {
		t.Fatalf("Open must not fail when history is missing: %v", err)
	}
}

// TestOpenSkipsMigrationWithEmbeddedSkillRepo proves the heal defers
// to Snapshot's own refusal: with an embedded skill repository
// present, Open leaves loadout.toml tracked rather than untracking it
// with no commit to record. Once the embedded repo is gone, the next
// Open migrates normally.
func TestOpenSkipsMigrationWithEmbeddedSkillRepo(t *testing.T) {
	root := legacyVault(t)
	embeddedGit := filepath.Join(root, "skills", "deploy-checks", ".git")
	if err := os.MkdirAll(embeddedGit, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := vault.Open(root); err != nil {
		t.Fatal(err)
	}
	if out := runGitIn(t, root, "ls-files", "loadout.toml"); strings.TrimSpace(out) == "" {
		t.Fatal("Open must not untrack loadout.toml while an embedded skill repo is present")
	}

	if err := os.RemoveAll(filepath.Dir(embeddedGit)); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if out := runGitIn(t, root, "ls-files", "loadout.toml"); strings.TrimSpace(out) != "" {
		t.Fatal("Open must migrate once the embedded skill repo is gone")
	}
	entries, err := vault.History(v, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Subject != "split the manifest" {
		t.Fatalf("History must show the split commit, got %v", entries)
	}
}

// TestSyncedSet pins the synced set's content: skills, memory, and
// the device roster. Everything else in the vault is device-local.
func TestSyncedSet(t *testing.T) {
	got := vault.SyncedSet()
	want := []string{"skills", "memory", "devices.toml"}
	if len(got) != len(want) {
		t.Fatalf("SyncedSet() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SyncedSet() = %v, want %v", got, want)
		}
	}
}
