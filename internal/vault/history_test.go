package vault_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestRecentSubjects(t *testing.T) {
	v := newVault(t)
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add a fact"); err != nil {
		t.Fatal(err)
	}
	subjects, err := vault.RecentSubjects(v, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 2 {
		t.Fatalf("want 2 subjects, got %v", subjects)
	}
	if subjects[0] != "add a fact" || subjects[1] != "init the vault" {
		t.Fatalf("bad subjects: %v", subjects)
	}
}

func TestRecentSubjectsCapsAtN(t *testing.T) {
	v := newVault(t)
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(v.MemoryDir(), name), []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := vault.Snapshot(v, "add "+name); err != nil {
			t.Fatal(err)
		}
	}
	subjects, err := vault.RecentSubjects(v, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 1 || subjects[0] != "add b.md" {
		t.Fatalf("bad subjects: %v", subjects)
	}
}

// TestRecentSubjectsOnMissingGitGivesFixedError proves that a vault
// whose .git directory is gone gets a fixed, friendly error instead
// of a raw git failure.
func TestRecentSubjectsOnMissingGitGivesFixedError(t *testing.T) {
	v := newVault(t)
	if err := os.RemoveAll(filepath.Join(v.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	_, err := vault.RecentSubjects(v, 3)
	if err == nil {
		t.Fatal("RecentSubjects on a vault with no history must fail")
	}
	want := "the vault at " + v.Root + " has no history. Fix: run loadout doctor."
	if err.Error() != want {
		t.Fatalf("bad error: got %q want %q", err.Error(), want)
	}
}

func TestSnapshotRefusesEmbeddedGitRepo(t *testing.T) {
	v := newVault(t)
	dir := filepath.Join(v.SkillsDir(), "deploy-checks")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := vault.Snapshot(v, "add skill deploy-checks")
	if err == nil {
		t.Fatal("Snapshot must refuse a skill folder that is a git repository")
	}
	if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "git repository") {
		t.Fatalf("error must name the skill folder, got %v", err)
	}
}
