package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"loadout.dev/loadout/internal/vault"
)

func TestInitAndOpen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{v.SkillsDir(), v.MemoryDir(), v.RenderDir()} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Fatalf("missing directory %s", d)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatal("history repo is missing")
	}
	if _, err := vault.Init(root); err == nil {
		t.Fatal("second init must fail")
	}
	v2, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !v2.Manifest.Adapters["claude-code"].Enabled {
		t.Fatal("manifest did not load")
	}
}

func TestOpenMissingVault(t *testing.T) {
	if _, err := vault.Open(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("open must fail without a vault")
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Setenv("LOADOUT_HOME", "/tmp/lo")
	if got := vault.DefaultRoot(); got != "/tmp/lo" {
		t.Fatalf("got %q", got)
	}
}

func TestSnapshotRecordsChanges(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.MemoryDir(), "a.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.Snapshot(v, "add a fact"); err != nil {
		t.Fatal(err)
	}
	// A snapshot with no changes must not fail.
	if err := vault.Snapshot(v, "empty"); err != nil {
		t.Fatal(err)
	}
}

func TestInitCleansUpOnHistoryFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	// Save original PATH to restore after test.
	originalPath := os.Getenv("PATH")
	// Set PATH empty so git cannot be found.
	t.Setenv("PATH", "")
	_, err := vault.Init(root)
	if err == nil {
		t.Fatal("init must fail when git is not found")
	}
	// Verify manifest was cleaned up.
	if _, err := os.Stat(filepath.Join(root, "loadout.toml")); err == nil {
		t.Fatal("loadout.toml should be removed on history failure")
	}
	// Restore PATH so git can run.
	os.Setenv("PATH", originalPath)
	// A retry with git available should succeed.
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if v == nil {
		t.Fatal("init should return vault when it succeeds")
	}
}

func TestInitMakesRootAbsolute(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	v, err := vault.Init("relvault")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(v.Root) {
		t.Fatalf("root must be absolute, got %q", v.Root)
	}
}
