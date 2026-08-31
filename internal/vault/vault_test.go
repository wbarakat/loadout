package vault_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestOpenRefusesRelativeManifestPath(t *testing.T) {
	root := t.TempDir()
	toml := "version = 1\n\n[adapters.agents-md]\nenabled = true\ntargets = [\"AGENTS.md\"]\n"
	if err := os.WriteFile(filepath.Join(root, "loadout.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := vault.Open(root)
	if err == nil {
		t.Fatal("Open must refuse a relative path in the manifest")
	}
	if !strings.Contains(err.Error(), "adapters.agents-md.targets") || !strings.Contains(err.Error(), "AGENTS.md") {
		t.Fatalf("error must name the key and the value, got %v", err)
	}
}

func TestOpenCorruptManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "loadout.toml"), []byte("not = [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := vault.Open(root)
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("a corrupt manifest must report it is unreadable, got %v", err)
	}
}

func TestOpenRecreatesStructuralDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(v.MemoryDir()); err != nil {
		t.Fatal(err)
	}
	v2, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(v2.MemoryDir()); err != nil || !fi.IsDir() {
		t.Fatal("Open must recreate a missing structural directory")
	}
}

func TestInitWritesGitignore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(v.Root, ".gitignore"))
	if err != nil {
		t.Fatal("Init must write a .gitignore file")
	}
	for _, entry := range []string{".DS_Store", "render/", "loadout.lock"} {
		if !strings.Contains(string(data), entry) {
			t.Fatalf(".gitignore missing %q, got %q", entry, string(data))
		}
	}
}

func TestInitSkipsRenderGitkeep(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(v.RenderDir(), ".gitkeep")); err == nil {
		t.Fatal("Init must not write render/.gitkeep; render is derived state")
	}
	for _, d := range []string{v.SkillsDir(), v.MemoryDir()} {
		if _, err := os.Stat(filepath.Join(d, ".gitkeep")); err != nil {
			t.Fatalf("missing .gitkeep in %s", d)
		}
	}
}

func TestOpenHealsMissingGitignore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	v, err := vault.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(v.Root, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal("Open must heal a missing .gitignore file")
	}
}

func TestOpenStoresManifestWarnings(t *testing.T) {
	root := t.TempDir()
	toml := "version = 1\nenable = true\n\n[adapters.claude-code]\nenabled = true\n"
	if err := os.WriteFile(filepath.Join(root, "loadout.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want := "the manifest key enable is unknown; loadout ignores it."
	if len(v.Warnings) != 1 || v.Warnings[0] != want {
		t.Fatalf("Warnings = %v, want [%q]", v.Warnings, want)
	}
}

func TestOpenRejectsFutureManifestVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "loadout.toml"), []byte("version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := vault.Open(root)
	want := "the vault manifest is version 2; this loadout build understands version 1. Fix: upgrade loadout."
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// TestGitignoreCoexistsWithTrackedRenderGitkeep guards the Task 3
// migration note: a vault made before this change may already have
// render/.gitkeep committed to history. The new .gitignore rule must
// not fight that; git leaves an already-tracked file tracked even
// once a rule ignores its directory, and Snapshot must not error.
func TestGitignoreCoexistsWithTrackedRenderGitkeep(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	for _, d := range []string{"skills", "memory", "render"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "render", ".gitkeep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "loadout.toml"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q", "-b", "main")
	runGit("add", "-A")
	runGit("-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "-m", "legacy vault")

	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal("Open must heal the missing .gitignore")
	}
	if err := vault.Snapshot(v, "sync"); err != nil {
		t.Fatalf("Snapshot must not fail alongside an already-tracked render/.gitkeep: %v", err)
	}
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "render/.gitkeep") {
		t.Fatal("an already-tracked render/.gitkeep must stay tracked")
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
