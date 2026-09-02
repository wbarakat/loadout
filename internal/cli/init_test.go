package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// runHeadless runs "loadout init --yes" plus any extra args through
// the real Run/cmdInit entrypoint. It never touches os.Stdin: the
// whole point of the headless path is that it reads no prompts at
// all.
func runHeadless(t *testing.T, extra ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(&out, &errOut, append([]string{"init", "--yes"}, extra...))
	return out.String(), errOut.String(), code
}

// TestInitHeadlessInstallsUnattended proves the core headless path: no
// stdin is fed at all (setupInitEnv leaves os.Stdin untouched), yet
// "loadout init --yes" creates the vault, enables adapters for every
// detected tool, imports the fixture skill as a draft, and prints its
// closing summary exactly once — Task 3's review Minor (a): the
// summary must never appear twice, once from the wizard and once from
// the import report.
func TestInitHeadlessInstallsUnattended(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustMkdirAll(t, filepath.Join(home, ".codex"))
	mustWriteFixture(t, filepath.Join(home, ".claude", "skills", "mytool", "SKILL.md"),
		"---\nname: mytool\ndescription: run mytool's own checks before a commit\n---\n\nRun the mytool checks before every commit.\n")

	out, errOut, code := runHeadless(t)
	if code != 0 {
		t.Fatalf("init --yes failed: %s", errOut)
	}
	if !strings.Contains(out, "created the vault at "+vaultRoot) {
		t.Fatalf("must print the vault path, got %q", out)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatalf("vault must open: %v", err)
	}
	if !v.Manifest.Adapters["claude-code"].Enabled {
		t.Fatalf("claude-code adapter must be enabled, got %+v", v.Manifest.Adapters["claude-code"])
	}
	if !v.Manifest.Adapters["codex"].Enabled {
		t.Fatalf("codex adapter must be enabled, got %+v", v.Manifest.Adapters["codex"])
	}

	if _, err := os.Stat(filepath.Join(vaultRoot, "skills", "mytool", "SKILL.md")); err != nil {
		t.Fatalf("the imported skill must land in the vault as a draft: %v", err)
	}

	if n := strings.Count(out, "loadout sync --remote"); n != 1 {
		t.Fatalf("the next-steps summary must print exactly once, got it %d times in %q", n, out)
	}
}

// TestInitHeadlessNeverReadsStdin proves the deterministic, unattended
// contract directly: os.Stdin is a pipe whose write end is never
// closed and never written to, so any read from it blocks forever.
// "loadout init --yes" must still return promptly.
func TestInitHeadlessNeverReadsStdin(t *testing.T) {
	home, _, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = origStdin
		r.Close()
		w.Close()
	})

	done := make(chan struct {
		out, errOut string
		code        int
	}, 1)
	go func() {
		var out, errOut bytes.Buffer
		code := Run(&out, &errOut, []string{"init", "--yes"})
		done <- struct {
			out, errOut string
			code        int
		}{out.String(), errOut.String(), code}
	}()

	select {
	case res := <-done:
		if res.code != 0 {
			t.Fatalf("init --yes failed: %s", res.errOut)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loadout init --yes must never read stdin, but it blocked as if it did")
	}
}

// TestInitHeadlessNoImportSkipsImport proves --no-import imports
// nothing, even though a real candidate skill sits right there in the
// fixture — the headless mirror of
// TestInitWizardDeclineImportImportsNothing.
func TestInitHeadlessNoImportSkipsImport(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustWriteFixture(t, filepath.Join(home, ".claude", "skills", "mytool", "SKILL.md"),
		"---\nname: mytool\ndescription: x\n---\n\nbody\n")

	out, errOut, code := runHeadless(t, "--no-import")
	if code != 0 {
		t.Fatalf("init --yes --no-import failed: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(vaultRoot, "skills", "mytool")); !os.IsNotExist(err) {
		t.Fatalf("--no-import must import nothing, got err=%v", err)
	}
	if strings.Contains(out, "import preview") {
		t.Fatalf("--no-import must not even preview, got %q", out)
	}
}

// TestInitHeadlessToolsFilterEnablesOnlyNamed proves --tools narrows
// adapter-enabling to exactly the named tools, leaving every other
// detected tool's adapter untouched (disabled).
func TestInitHeadlessToolsFilterEnablesOnlyNamed(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustMkdirAll(t, filepath.Join(home, ".codex"))

	out, errOut, code := runHeadless(t, "--tools", "claude-code")
	if code != 0 {
		t.Fatalf("init --yes --tools claude-code failed: %s", errOut)
	}
	if !strings.Contains(out, "enabled adapters: claude-code") {
		t.Fatalf("must report only claude-code enabled, got %q", out)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Manifest.Adapters["claude-code"].Enabled {
		t.Fatalf("claude-code adapter must be enabled, got %+v", v.Manifest.Adapters["claude-code"])
	}
	if v.Manifest.Adapters["codex"].Enabled {
		t.Fatalf("codex adapter must stay disabled, got %+v", v.Manifest.Adapters["codex"])
	}
}

// TestInitHeadlessRemoteTokenFileNeverEchoed proves --remote +
// --token-file writes the remote configuration with the token read
// from the file, while the token value itself never appears anywhere
// in the headless path's printed output — the headless mirror of
// TestInitWizardRemoteTokenFileNeverEchoed.
func TestInitHeadlessRemoteTokenFileNeverEchoed(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	const secretToken = "sk-super-secret-loadoutd-token-9f31c2"
	tokenPath := filepath.Join(home, "token.txt")
	mustWriteFixture(t, tokenPath, secretToken+"\n")

	out, errOut, code := runHeadless(t, "--no-import", "--remote", "http://localhost:9999", "--token-file", tokenPath)
	if code != 0 {
		t.Fatalf("init --yes --remote failed: %s", errOut)
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

// TestInitHeadlessRemoteWithoutTokenFileErrors proves --remote with no
// --token-file is a clear error that leaves no partial remote
// configuration behind.
func TestInitHeadlessRemoteWithoutTokenFileErrors(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	out, errOut, code := runHeadless(t, "--no-import", "--remote", "http://localhost:9999")
	if code == 0 {
		t.Fatalf("--remote with no --token-file must fail, got exit 0, stdout=%q", out)
	}
	if !strings.Contains(errOut, "--token-file") {
		t.Fatalf("the error must name --token-file, got %q", errOut)
	}

	// The flags are rejected before init ever creates the vault, so no
	// vault — and so no partial remote config — exists at all.
	if _, err := os.Stat(filepath.Join(vaultRoot, "loadout.toml")); !os.IsNotExist(err) {
		t.Fatalf("a bad --remote/--token-file pairing must create no vault, got err=%v", err)
	}
}

// TestInitHeadlessBogusToolErrors proves an unknown --tools name is a
// clear error naming every valid (detected) tool, and changes nothing
// in the vault.
func TestInitHeadlessBogusToolErrors(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))
	mustMkdirAll(t, filepath.Join(home, ".codex"))

	out, errOut, code := runHeadless(t, "--tools", "bogus")
	if code == 0 {
		t.Fatalf("--tools bogus must fail, got exit 0, stdout=%q", out)
	}
	if !strings.Contains(errOut, "bogus") {
		t.Fatalf("the error must name the bad tool, got %q", errOut)
	}
	if !strings.Contains(errOut, "claude-code") || !strings.Contains(errOut, "codex") {
		t.Fatalf("the error must list the valid, detected tool names, got %q", errOut)
	}

	if _, err := os.Stat(filepath.Join(vaultRoot, "loadout.toml")); !os.IsNotExist(err) {
		t.Fatalf("a bad --tools name must create no vault, got err=%v", err)
	}
}

// TestInitHeadlessFreshVaultDisablesUndetectedAdapters proves
// Important-1's core fix: a fresh vault's chosen set is authoritative
// for the WHOLE manifest, not just an addition to it. On a home with
// only claude-code present, the manifest must enable claude-code and
// explicitly DISABLE every other adapter DefaultManifest pre-enables
// (pi, plus codex/gemini/cursor/hermes which start disabled anyway)
// — and a subsequent "loadout sync" must not create pi's skills
// directory at all, even though a skill exists to link.
func TestInitHeadlessFreshVaultDisablesUndetectedAdapters(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	out, errOut, code := runHeadless(t)
	if code != 0 {
		t.Fatalf("init --yes failed: %s", errOut)
	}
	if !strings.Contains(out, "enabled adapters: claude-code") {
		t.Fatalf("must report only claude-code enabled, got %q", out)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Manifest.Adapters["claude-code"].Enabled {
		t.Fatalf("claude-code must be enabled, got %+v", v.Manifest.Adapters["claude-code"])
	}
	for _, name := range []string{"pi", "codex", "gemini", "cursor", "hermes"} {
		if v.Manifest.Adapters[name].Enabled {
			t.Fatalf("%s must be disabled on a pi-less fresh install, got %+v", name, v.Manifest.Adapters[name])
		}
	}

	var addOut, addErr bytes.Buffer
	if code := Run(&addOut, &addErr, []string{"add", "skill", "deploy-checks"}); code != 0 {
		t.Fatalf("add skill failed: %s", addErr.String())
	}
	var syncOut, syncErr bytes.Buffer
	if code := Run(&syncOut, &syncErr, []string{"sync"}); code != 0 {
		t.Fatalf("sync failed: %s", syncErr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "skills")); !os.IsNotExist(err) {
		t.Fatalf("sync must not create ~/.pi/agent/skills for an undetected, disabled adapter, got err=%v", err)
	}
	if _, err := os.Readlink(filepath.Join(home, ".claude", "skills", "deploy-checks")); err != nil {
		t.Fatalf("sync must still link the skill into the enabled claude-code adapter: %v", err)
	}
}

// TestInitHeadlessRerunPreservesCustomSkillsDir proves the sub-rule
// paired with Important-1's fresh-vault fix: an EXISTING vault's
// enableAdapters call is additive only, and never resets a
// hand-customized skills_dir back to the detected default just
// because the tool is named again on a re-run.
func TestInitHeadlessRerunPreservesCustomSkillsDir(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	if _, errOut, code := runHeadless(t); code != 0 {
		t.Fatalf("first init --yes failed: %s", errOut)
	}

	v, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	customDir := filepath.Join(home, "somewhere-else", "skills")
	cc := v.Manifest.Adapters["claude-code"]
	cc.SkillsDir = customDir
	v.Manifest.Adapters["claude-code"] = cc
	if err := vault.SaveManifest(filepath.Join(vaultRoot, "loadout.toml"), v.Manifest); err != nil {
		t.Fatal(err)
	}

	if _, errOut, code := runHeadless(t); code != 0 {
		t.Fatalf("re-run init --yes failed: %s", errOut)
	}

	v2, err := vault.Open(vaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got := v2.Manifest.Adapters["claude-code"].SkillsDir; got != customDir {
		t.Fatalf("a re-run must preserve a customized skills_dir, got %q want %q", got, customDir)
	}
}

// TestInitHeadlessTokenFileMissingFailsFastNoVault proves Minor-(a):
// a token file that cannot even be opened must abort before runInit
// writes anything at all — no vault, no loadout.toml — rather than
// failing only after the vault, adapters, and import already ran.
func TestInitHeadlessTokenFileMissingFailsFastNoVault(t *testing.T) {
	home, vaultRoot, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	missing := filepath.Join(home, "does-not-exist.txt")
	out, errOut, code := runHeadless(t, "--remote", "http://localhost:9999", "--token-file", missing)
	if code == 0 {
		t.Fatalf("a missing token file must fail, got exit 0, stdout=%q", out)
	}
	if !strings.Contains(errOut, "--token-file") {
		t.Fatalf("the error must name --token-file, got %q", errOut)
	}
	if _, err := os.Stat(filepath.Join(vaultRoot, "loadout.toml")); !os.IsNotExist(err) {
		t.Fatalf("a bad token file must create no vault at all, got err=%v", err)
	}
}

// TestInitHeadlessTokenFileErrorNeverEchoesTheProvidedValue proves
// Minor-(b): a person who mistakenly passes the token's own VALUE
// where the --token-file PATH belongs must never see that value
// echoed back in the resulting error.
func TestInitHeadlessTokenFileErrorNeverEchoesTheProvidedValue(t *testing.T) {
	home, _, _ := setupInitEnv(t)
	mustMkdirAll(t, filepath.Join(home, ".claude"))

	const mistakenValue = "sk-this-looks-like-a-token-not-a-path-8f2c1a"
	out, errOut, code := runHeadless(t, "--remote", "http://localhost:9999", "--token-file", mistakenValue)
	if code == 0 {
		t.Fatalf("a nonexistent token-file path must fail, got exit 0, stdout=%q", out)
	}
	if strings.Contains(errOut, mistakenValue) {
		t.Fatalf("the error must never echo the provided --token-file value, got %q", errOut)
	}
}

// TestInitHelpNamesHeadlessFlags proves "loadout help" documents every
// headless flag, not just the wizard's own prose.
func TestInitHelpNamesHeadlessFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(&out, &errOut, []string{"help"})
	if code != 0 {
		t.Fatalf("help failed: %s", errOut.String())
	}
	text := out.String()
	for _, flag := range []string{"--yes", "--tools", "--no-import", "--remote", "--token-file", "--project-memory"} {
		if !strings.Contains(text, flag) {
			t.Fatalf("help must name %s, got %q", flag, text)
		}
	}
}
