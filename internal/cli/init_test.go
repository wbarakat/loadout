package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loadout.dev/loadout/internal/remote"
	"loadout.dev/loadout/internal/vault"
)

// setupInitEnv builds a fresh temp HOME/LOADOUT_HOME/project directory
// for one "loadout init" wizard test, and points initLookPath at
// noBinsOnPath (detect_test.go) so this repo's own real dev machine —
// which can easily have several of loadout's supported tools
// (claude, codex, pi, hermes, ...) genuinely on PATH — can never leak
// into a test's detected set. Every override reverts via t.Cleanup;
// the real home is never touched.
func setupInitEnv(t *testing.T) (home, vaultRoot, projectDir string) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	vaultRoot = filepath.Join(base, "vault")
	projectDir = filepath.Join(base, "project")
	mustMkdirAll(t, home)
	mustMkdirAll(t, projectDir)

	t.Setenv("HOME", home)
	t.Setenv("LOADOUT_HOME", vaultRoot)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")

	origLookPath := initLookPath
	initLookPath = noBinsOnPath
	t.Cleanup(func() { initLookPath = origLookPath })

	// A clean, empty cwd for the import step's own default project
	// scope, so this repo's own checkout (whatever it happens to hold)
	// can never leak a stray AGENTS.md/.agents/skills into a scan.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origWD); err != nil {
			t.Fatal(err)
		}
	})

	return home, vaultRoot, projectDir
}

// mustWriteFixture creates path (and its parent directories) holding
// content.
func mustWriteFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runInitWithAnswers runs "loadout init" through the real Run/cmdInit
// entrypoint, with os.Stdin fed answers (pipeValue, from
// secret_internal_test.go) and restored once the test ends.
func runInitWithAnswers(t *testing.T, answers string) (stdout, stderr string, code int) {
	t.Helper()
	pipeValue(t, answers)
	var out, errOut bytes.Buffer
	code = Run(&out, &errOut, []string{"init"})
	return out.String(), errOut.String(), code
}

// TestInitWizardDetectsEnablesAndImports proves the wizard's full
// happy path: it detects the fixture claude-code and codex trees,
// prints the detected set, creates the vault, answering Y to "enable
// adapters" writes loadout.toml enabling both with the tools' own
// skills_dir/memory_file, and answering Y to "import" lands a draft
// skill in the vault. The summary names both next commands.
func TestInitWizardDetectsEnablesAndImports(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustMkdirAll(t, filepath.Join(home, ".codex"))
	mustWriteFixture(t, filepath.Join(home, ".claude", "skills", "mytool", "SKILL.md"),
		"---\nname: mytool\ndescription: run mytool's own checks before a commit\n---\n\nRun the mytool checks before every commit.\n")

	out, errOut, code := runInitWithAnswers(t, "y\ny\nn\n")
	if code != 0 {
		t.Fatalf("init failed: %s", errOut)
	}
	if !strings.Contains(out, "Found: claude-code, codex") {
		t.Fatalf("must print the detected set, got %q", out)
	}
	if !strings.Contains(out, "created the vault at "+vaultRoot) {
		t.Fatalf("must print the vault path, got %q", out)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatalf("vault must open: %v", err)
	}
	cc := v.Manifest.Adapters["claude-code"]
	if !cc.Enabled {
		t.Fatalf("claude-code adapter must be enabled, got %+v", cc)
	}
	if cc.SkillsDir != filepath.Join(home, ".claude", "skills") {
		t.Fatalf("claude-code SkillsDir = %q", cc.SkillsDir)
	}
	if cc.MemoryFile != filepath.Join(home, ".claude", "CLAUDE.md") {
		t.Fatalf("claude-code MemoryFile = %q", cc.MemoryFile)
	}
	cx := v.Manifest.Adapters["codex"]
	if !cx.Enabled {
		t.Fatalf("codex adapter must be enabled, got %+v", cx)
	}
	if cx.SkillsDir != filepath.Join(home, ".codex", "skills") {
		t.Fatalf("codex SkillsDir = %q", cx.SkillsDir)
	}
	if cx.MemoryFile != filepath.Join(home, ".codex", "AGENTS.md") {
		t.Fatalf("codex MemoryFile = %q", cx.MemoryFile)
	}

	if _, err := os.Stat(filepath.Join(vaultRoot, "skills", "mytool", "SKILL.md")); err != nil {
		t.Fatalf("the imported skill must land in the vault as a draft: %v", err)
	}
	if !strings.Contains(out, "loadout review") {
		t.Fatalf("summary must name loadout review, got %q", out)
	}
	if !strings.Contains(out, "loadout sync --remote") {
		t.Fatalf("summary must name loadout sync --remote, got %q", out)
	}
}

// TestInitWizardRerunKeepsExistingVault proves re-running "loadout
// init" on an already-initialized vault reports the vault already
// exists, never destroys existing content, and still runs the
// adapters step.
func TestInitWizardRerunKeepsExistingVault(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	if _, errOut, code := runInitWithAnswers(t, "y\nn\nn\n"); code != 0 {
		t.Fatalf("first init failed: %s", errOut)
	}

	seedPath := filepath.Join(vaultRoot, "skills", "keepme", "SKILL.md")
	mustWriteFixture(t, seedPath, "---\nname: keepme\ndescription: a pre-seeded skill\n---\n\nkeep this.\n")

	out, errOut, code := runInitWithAnswers(t, "y\nn\nn\n")
	if code != 0 {
		t.Fatalf("re-run init failed: %s", errOut)
	}
	if !strings.Contains(out, "vault already exists at "+vaultRoot) {
		t.Fatalf("re-run must report the existing vault, got %q", out)
	}
	if data, err := os.ReadFile(seedPath); err != nil {
		t.Fatalf("the pre-seeded skill must survive a re-run: %v", err)
	} else if !strings.Contains(string(data), "keep this.") {
		t.Fatalf("the pre-seeded skill's content must be unchanged, got %q", data)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Manifest.Adapters["claude-code"].Enabled {
		t.Fatalf("a re-run must still update adapters, got %+v", v.Manifest.Adapters["claude-code"])
	}
}

// TestInitWizardDeclineImportImportsNothing proves answering "n" to
// the import prompt imports nothing at all, even though a real
// candidate skill sits right there in the fixture.
func TestInitWizardDeclineImportImportsNothing(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustWriteFixture(t, filepath.Join(home, ".claude", "skills", "mytool", "SKILL.md"),
		"---\nname: mytool\ndescription: x\n---\n\nbody\n")

	out, errOut, code := runInitWithAnswers(t, "y\nn\nn\n")
	if code != 0 {
		t.Fatalf("init failed: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(vaultRoot, "skills", "mytool")); !os.IsNotExist(err) {
		t.Fatalf("declining import must import nothing, got err=%v", err)
	}
	if strings.Contains(out, "import preview") {
		t.Fatalf("declining import must not even preview, got %q", out)
	}
}

// TestInitWizardRemoteTokenFileNeverEchoed proves the remote step
// reads the token from a file path and writes it to the vault's
// remote configuration, while the token value itself never appears
// anywhere in the wizard's printed output.
func TestInitWizardRemoteTokenFileNeverEchoed(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	const secretToken = "sk-super-secret-loadoutd-token-9f31c2"
	tokenPath := filepath.Join(home, "token.txt")
	mustWriteFixture(t, tokenPath, secretToken+"\n")

	answers := "n\nn\ny\nhttp://localhost:9999\n" + tokenPath + "\n"
	out, errOut, code := runInitWithAnswers(t, answers)
	if code != 0 {
		t.Fatalf("init failed: %s", errOut)
	}
	combined := out + errOut
	if strings.Contains(combined, secretToken) {
		t.Fatalf("the token must never appear in output, got %q", combined)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := remote.Load(v)
	if err != nil {
		t.Fatalf("remote config must load: %v", err)
	}
	if cfg.URL != "http://localhost:9999" {
		t.Fatalf("bad remote url: %q", cfg.URL)
	}
	if cfg.Token != secretToken {
		t.Fatalf("the remote config must hold the real token read from the file, got %q", cfg.Token)
	}
}

// TestInitWizardNoInputOnlyCreatesTheVault proves the safety
// invariant every other bare "loadout init" call across this whole
// test suite relies on: with no stdin at all (immediate EOF on the
// very first prompt), the wizard takes no consequential default
// action — it enables no *new* adapter, runs no import, connects no
// remote — even though a fixture tool is genuinely present. It only
// ever creates the vault and says so. codex (not claude-code or pi,
// which vault.DefaultManifest already enables regardless of any
// detection) is the fixture here precisely because it starts
// disabled, so enabling it can only be this wizard's own doing.
func TestInitWizardNoInputOnlyCreatesTheVault(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".codex"))
	mustWriteFixture(t, filepath.Join(home, ".codex", "skills", "mytool", "SKILL.md"),
		"---\nname: mytool\ndescription: x\n---\n\nbody\n")

	out, errOut, code := runInitWithAnswers(t, "")
	if code != 0 {
		t.Fatalf("init failed: %s", errOut)
	}
	if !strings.Contains(out, "created the vault at "+vaultRoot) {
		t.Fatalf("must still create the vault, got %q", out)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	if v.Manifest.Adapters["codex"].Enabled {
		t.Fatalf("no input must enable no adapter, got %+v", v.Manifest.Adapters["codex"])
	}
	if _, err := os.Stat(filepath.Join(vaultRoot, "skills", "mytool")); !os.IsNotExist(err) {
		t.Fatalf("no input must import nothing, got err=%v", err)
	}
	if _, err := remote.Load(v); err == nil {
		t.Fatalf("no input must connect no remote")
	}
}
